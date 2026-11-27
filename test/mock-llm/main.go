// Command mock-llm is a model server for tests. It serves the APIs that the
// harnesses use, streaming: OpenAI's Chat Completions and Responses, and
// Anthropic's Messages. Instead of a model, a script answers: the user's last
// message is an instruction.
//
//	say <text>     replies with the text
//	drawl <text>   replies with the text slowly, a word every half second,
//	               which leaves time to stop the turn in the middle of it
//	run <command>  runs the command with the harness's shell tool, then
//	               replies with the tool's output
//	read <path>    reads the file with the harness's tool that reads files,
//	               then replies with the tool's output
//	use <server> <tool> [<arguments>]
//	               calls a tool of an MCP server with the JSON arguments,
//	               then replies with the tool's output. The tool is the first
//	               that the harness offers whose name ends with the server's
//	               and the tool's, as in "server_tool" or "mcp__server__tool"
//	attached       replies with what follows the instruction in its message,
//	               as the links to the files that a message carries do, a
//	               part per line, or "nothing" if nothing does
//	pictures       replies with the media types of the pictures that the
//	               harness gave the model in the conversation, with a
//	               message or as what a tool read, one per line, or "none"
//	               if it gave none
//	recall         replies with the user's earlier instructions, one per
//	               line, or "nothing" if there are none
//	find <text>    replies "present" if the harness sent the text along with
//	               the conversation, as it does with an agent's
//	               instructions, and "absent" if not
//	fail <text>    refuses the request with the text as the error, as a
//	               model server refuses an unknown caller
//	flaky <text>   refuses the request once, as an overloaded model server
//	               does, and replies with the text when the harness retries
//
// Any other message gets the reply "mock". An instruction is a part of its
// message by itself: what the harness puts before or after it in the message
// is not of it.
//
// Requests must carry the API key given in MOCK_LLM_API_KEY. Every response
// reports the same token usage, 11 input and 7 output tokens. GET /v1/models
// lists one model, "mock". Asked as the model "mock-text", it refuses a
// request that carries a picture, as a model does that takes only text.
//
// mock-llm is also the forward proxy of the harness tests that run without a
// cluster. As a proxy it forwards nothing: it refuses each request and logs
// the requested host, so that a test can see what went out.
//
// It is also an MCP server, with the same API key, at /mcp by Streamable
// HTTP and at /sse by SSE. Its one tool, headers, reports the values of the
// headers that it is given by name, as the request that began the session
// had them, so that a test can see what reached the server. At /locked/mcp
// and /locked/sse, it refuses every request with 401, as a server does whose
// token is not valid.
//
// It also serves a small Python package, aviary-probe, for tests to install
// with pip: GET /packages/aviary_probe-1.0-py3-none-any.whl.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	inputTokens  = 11
	outputTokens = 7
)

// drawlPause is the pause after each word of a drawl answer.
const drawlPause = 500 * time.Millisecond

// textOnly is the name of the model that takes no pictures.
const textOnly = "mock-text"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	apiKey := os.Getenv("MOCK_LLM_API_KEY")
	if apiKey == "" {
		slog.Error("MOCK_LLM_API_KEY is not set")
		os.Exit(1)
	}
	server := &http.Server{Addr: ":8080", Handler: newMock(apiKey), ReadHeaderTimeout: 10 * time.Second}
	slog.Info("listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil {
		slog.Error("mock-llm stopped", "error", err)
		os.Exit(1)
	}
}

// mock is the model server. It keeps no conversation state: a harness sends
// the whole conversation with every request.
type mock struct {
	mu sync.Mutex
	// refused holds the texts of flaky instructions that were refused once.
	refused map[string]bool
}

// newMock returns the model server, which requires apiKey on model requests.
func newMock(apiKey string) http.Handler {
	m := &mock{refused: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /v1/chat/completions", withKey(apiKey, m.chatCompletions))
	mux.HandleFunc("POST /v1/responses", withKey(apiKey, m.responses))
	mux.HandleFunc("POST /v1/messages", withKey(apiKey, m.messages))
	mux.HandleFunc("GET /v1/models", withKey(apiKey, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object": "list", "data": [{"id": "mock", "object": "model"}]}`+"\n")
	}))
	probe := wheel()
	mux.HandleFunc("GET /packages/"+probeWheel, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(probe)
	})
	session := func(r *http.Request) *mcp.Server { return mcpServer(r.Header.Clone()) }
	mux.HandleFunc("/mcp", withKey(apiKey, mcp.NewStreamableHTTPHandler(session, nil).ServeHTTP))
	mux.HandleFunc("/sse", withKey(apiKey, mcp.NewSSEHandler(session, nil).ServeHTTP))
	// A server that refuses every caller, as one does whose token is not
	// valid, and says where its OAuth metadata is, as MCP servers do.
	locked := func(w http.ResponseWriter, r *http.Request) {
		slog.Info("refused MCP request", "method", r.Method, "path", r.URL.Path)
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="http://%s/.well-known/oauth-protected-resource%s"`, r.Host, r.URL.Path))
		refuse(w, http.StatusUnauthorized, "the token is not valid for this server")
	}
	mux.HandleFunc("/locked/mcp", locked)
	mux.HandleFunc("/locked/sse", locked)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A request for another host is a forward proxy request.
		if r.Method == http.MethodConnect || r.URL.Host != "" {
			slog.Info("refused proxy request", "host", r.URL.Hostname())
			refuse(w, http.StatusForbidden, "mock-llm does not forward requests")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// probeWheel is the file name of the aviary-probe wheel.
const probeWheel = "aviary_probe-1.0-py3-none-any.whl"

// wheel builds the aviary-probe wheel. Its module defines only its name.
func wheel() []byte {
	var made bytes.Buffer
	archive := zip.NewWriter(&made)
	add := func(name, content string) {
		file, err := archive.Create(name)
		if err == nil {
			_, err = io.WriteString(file, content)
		}
		if err != nil {
			// Writing to memory does not fail.
			panic(err)
		}
	}
	var record strings.Builder
	for _, file := range [][2]string{
		{"aviary_probe/__init__.py", "name = \"aviary-probe\"\n"},
		{"aviary_probe-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: aviary-probe\nVersion: 1.0\n"},
		{"aviary_probe-1.0.dist-info/WHEEL", "Wheel-Version: 1.0\nGenerator: mock-llm\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
	} {
		add(file[0], file[1])
		sum := sha256.Sum256([]byte(file[1]))
		fmt.Fprintf(&record, "%s,sha256=%s,%d\n", file[0], base64.RawURLEncoding.EncodeToString(sum[:]), len(file[1]))
	}
	record.WriteString("aviary_probe-1.0.dist-info/RECORD,,\n")
	add("aviary_probe-1.0.dist-info/RECORD", record.String())
	if err := archive.Close(); err != nil {
		panic(err)
	}
	return made.Bytes()
}

// headersInput is the input of the headers tool.
type headersInput struct {
	Names []string `json:"names" jsonschema:"the names of the headers to report"`
}

// mcpServer returns the MCP server of a session that a request with the
// headers began.
func mcpServer(begun http.Header) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "mock-llm", Version: "1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "headers",
		Description: "Reports the values of the headers with the given names, one per line.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input headersInput) (*mcp.CallToolResult, any, error) {
		var lines []string
		for _, name := range input.Names {
			value := begun.Get(name)
			if value == "" {
				value = "none"
			}
			lines = append(lines, name+": "+value)
		}
		slog.Info("called MCP tool", "tool", "headers", "headers", lines)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Join(lines, "\n")}}}, nil, nil
	})
	return server
}

// next returns the model's answer to the conversation. A flaky instruction is
// refused the first time.
func (m *mock) next(c conversation) answer {
	if c.model == textOnly && len(c.pictures) > 0 {
		return answer{failure: "the model takes no pictures", status: http.StatusBadRequest}
	}
	next := c.next()
	if !next.flaky {
		return next
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refused[next.text] {
		return answer{text: next.text}
	}
	m.refused[next.text] = true
	return answer{failure: "model is overloaded; try again later", status: http.StatusServiceUnavailable}
}

// conversation is a model request as the script sees it.
type conversation struct {
	// model is the model that the request asks.
	model string
	// said holds the user messages, oldest first.
	said []string
	// attached holds what followed the last instruction in its message.
	attached []string
	// pictures holds the media types of the pictures of the conversation.
	pictures []string
	// sent holds the text that the harness added itself: its instructions to
	// the model, and the messages that it added to the conversation.
	sent []string
	// toolOutput is the tool output that ends the conversation, if any, and
	// searched is set if it is the output of a tool search.
	toolOutput *string
	searched   bool
	// tools are the tools that the harness offers, and shell is the one that
	// runs commands, if there is one.
	tools []tool
	shell *shellTool
}

type shellTool struct {
	name string
	// argument is the name of the parameter that takes the command.
	argument string
}

// call returns the call that runs the command.
func (s shellTool) call(command string) *call {
	encoded, _ := json.Marshal(map[string]string{s.argument: command})
	return &call{name: s.name, arguments: string(encoded)}
}

// call is a call of a tool: its name, the namespace in which the harness
// offers it, if any, and its JSON arguments.
type call struct {
	name, namespace, arguments string
}

// answer is the model's next move: a tool call, or else text, with pause
// after each word. If status is set, the request is refused with status and
// failure instead. A flaky answer is given only after it was refused once.
type answer struct {
	text    string
	pause   time.Duration
	call    *call
	failure string
	status  int
	flaky   bool
}

// called says what tool call the answer is, for the log.
func (a answer) called() string {
	if a.call == nil {
		return ""
	}
	return a.call.namespace + a.call.name + " " + a.call.arguments
}

// wait pauses after a word of the answer. It reports false if the request was
// cancelled meanwhile.
func (a answer) wait(r *http.Request) bool {
	select {
	case <-time.After(a.pause):
		return true
	case <-r.Context().Done():
		return false
	}
}

// hear adds a user message, part by part. A part that is not an instruction
// is taken to be added by the harness, and also goes to sent. Those that
// follow an instruction in its message are attached to it, and are no
// messages of their own.
func (c *conversation) hear(parts ...string) {
	instructed := false
	for _, part := range parts {
		_, _, found := instruction(part)
		switch {
		case found:
			instructed, c.attached = true, nil
			c.said = append(c.said, part)
		case instructed:
			c.attached = append(c.attached, part)
			c.sent = append(c.sent, part)
		default:
			c.said = append(c.said, part)
			c.sent = append(c.sent, part)
		}
	}
}

func (c conversation) next() answer {
	// After a tool search, the instruction is taken up again.
	if c.toolOutput != nil && !c.searched {
		return answer{text: *c.toolOutput}
	}
	if len(c.said) == 0 {
		return answer{text: "mock"}
	}
	word, rest, found := instruction(c.said[len(c.said)-1])
	switch {
	case !found:
		return answer{text: "mock"}
	case word == "say":
		return answer{text: rest}
	case word == "drawl":
		return answer{text: rest, pause: drawlPause}
	case word == "fail":
		// The harnesses report this status as an error at once, Claude Code
		// only because the refusal says not to retry. On other statuses, a
		// harness may retry for a while, or end the turn with the error as its
		// answer.
		return answer{failure: rest, status: http.StatusUnauthorized}
	case word == "flaky":
		return answer{text: rest, flaky: true}
	case word == "run" && c.shell == nil:
		return answer{text: "mock: the harness offers no tool that runs a command"}
	case word == "run":
		return answer{call: c.shell.call(rest)}
	case word == "read":
		if reader := findReader(c.tools); reader != nil {
			return answer{call: reader.call(rest)}
		}
		return answer{text: "mock: the harness offers no tool that reads a file"}
	case word == "use":
		return c.use(rest)
	case word == "find" && strings.Contains(strings.Join(c.sent, "\n"), rest):
		return answer{text: "present"}
	case word == "find":
		return answer{text: "absent"}
	case word == "attached" && len(c.attached) == 0:
		return answer{text: "nothing"}
	case word == "attached":
		return answer{text: strings.Join(c.attached, "\n")}
	case word == "pictures" && len(c.pictures) == 0:
		return answer{text: "none"}
	case word == "pictures":
		return answer{text: strings.Join(c.pictures, "\n")}
	}
	var before []string
	for _, message := range c.said[:len(c.said)-1] {
		if _, _, found := instruction(message); found {
			before = append(before, strings.TrimSpace(message))
		}
	}
	if len(before) == 0 {
		return answer{text: "nothing"}
	}
	return answer{text: strings.Join(before, "\n")}
}

// use calls a tool of an MCP server. rest is the server's name, the tool's
// name and, if the tool takes any, the JSON arguments.
func (c conversation) use(rest string) answer {
	server, rest, _ := strings.Cut(rest, " ")
	name, arguments, _ := strings.Cut(strings.TrimSpace(rest), " ")
	if server == "" || name == "" {
		return answer{text: "mock: use takes the name of an MCP server and of its tool"}
	}
	called := findTool(c.tools, server, name)
	// A harness that offers the tools of MCP servers only once the model has
	// searched for them offers a tool that searches.
	searches := slices.ContainsFunc(c.tools, func(offered tool) bool { return offered.Name == toolSearch })
	if called == nil && searches && !c.searched {
		return answer{call: &call{name: toolSearch, arguments: fmt.Sprintf(`{"query": %q}`, server+" "+name)}}
	}
	if called == nil {
		var offered []string
		for _, tool := range c.tools {
			offered = append(offered, tool.Name)
		}
		return answer{text: fmt.Sprintf("mock: the harness offers no tool %s of the MCP server %s, only %s", name, server, strings.Join(offered, ", "))}
	}
	called.arguments = strings.TrimSpace(arguments)
	if called.arguments == "" {
		called.arguments = "{}"
	}
	return answer{call: called}
}

// instruction splits a user message into its script word and the rest. found
// is false for other messages, such as those that harnesses add.
func instruction(message string) (word, rest string, found bool) {
	word, rest, _ = strings.Cut(strings.TrimSpace(message), " ")
	switch word {
	case "say", "drawl", "run", "read", "use", "attached", "pictures", "recall", "find", "fail", "flaky":
		return word, strings.TrimSpace(rest), true
	}
	return "", "", false
}

// tool is a tool that a harness offers. The Responses API also takes
// namespaces, which hold tools.
type tool struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Parameters schema `json:"parameters"`
	Tools      []tool `json:"tools"`
}

// toolSearch is the name of the tool with which Claude Code has the model
// search for the tools of MCP servers before it offers them.
const toolSearch = "ToolSearch"

// findTool returns a call of the first tool whose name ends with the server's
// name and the tool's, set apart by "_", "." or ":" from each other and from
// what comes before, or nil. The name of a tool in a namespace begins with
// the namespace's.
func findTool(tools []tool, server, name string) *call {
	named := func(full string) bool {
		before, found := strings.CutSuffix(full, name)
		apart := strings.TrimRight(before, "_.:")
		if !found || apart == before {
			return false
		}
		before, found = strings.CutSuffix(apart, server)
		return found && (before == "" || strings.ContainsAny(before[len(before)-1:], "_.:"))
	}
	for _, offered := range tools {
		if offered.Type != "namespace" {
			if named(offered.Name) {
				return &call{name: offered.Name}
			}
			continue
		}
		for _, inner := range offered.Tools {
			if named(offered.Name + "." + inner.Name) {
				return &call{name: inner.Name, namespace: offered.Name}
			}
		}
	}
	return nil
}

// schema is the JSON schema of a tool's parameters.
type schema struct {
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required"`
}

// findShell returns the first tool that takes a command, or nil.
func findShell(tools []tool) *shellTool {
	for _, tool := range tools {
		for _, argument := range []string{"command", "cmd"} {
			if _, found := tool.Parameters.Properties[argument]; found {
				return &shellTool{name: tool.Name, argument: argument}
			}
		}
	}
	return nil
}

// readTool is a tool that reads a file, and the argument that names the file.
type readTool struct {
	name, argument string
}

// call returns the call that reads the file at the path.
func (r readTool) call(path string) *call {
	encoded, _ := json.Marshal(map[string]string{r.argument: path})
	return &call{name: r.name, arguments: string(encoded)}
}

// findReader returns the first tool that needs the path of a file and
// nothing else, or nil. A tool that needs more, such as a pattern or new
// content, does something else with the path.
func findReader(tools []tool) *readTool {
	for _, tool := range tools {
		for _, argument := range []string{"file_path", "filePath", "path"} {
			if slices.Equal(tool.Parameters.Required, []string{argument}) {
				return &readTool{name: tool.Name, argument: argument}
			}
		}
	}
	return nil
}

// text returns content that is either a string or a list of text parts.
func text(content json.RawMessage) string {
	return strings.Join(texts(content), "")
}

// texts returns the text parts of content that is either a string or a list
// of parts.
func texts(content json.RawMessage) []string {
	var whole string
	if json.Unmarshal(content, &whole) == nil {
		return []string{whole}
	}
	var parts []struct {
		Text string `json:"text"`
	}
	json.Unmarshal(content, &parts)
	texts := make([]string, len(parts))
	for i, part := range parts {
		texts[i] = part.Text
	}
	return texts
}

// pictures returns the media types of the pictures in a part of a request,
// in their order, in whichever shape an API has for a picture: with a
// message or in the output of a tool, as a data URL or as its data.
func pictures(part json.RawMessage) []string {
	var value any
	json.Unmarshal(part, &value)
	var found []string
	var walk func(value any)
	walk = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, inner := range value {
				walk(inner)
			}
		case map[string]any:
			kind, _ := value["type"].(string)
			// A data URL is the address of Chat Completions, in an object,
			// and of Responses.
			address, _ := value["image_url"].(string)
			if inner, nested := value["image_url"].(map[string]any); nested {
				address, _ = inner["url"].(string)
			}
			source, _ := value["source"].(map[string]any)
			media, _ := source["media_type"].(string)
			switch {
			case (kind == "image_url" || kind == "input_image") && strings.HasPrefix(address, "data:"):
				media, _, _ = strings.Cut(strings.TrimPrefix(address, "data:"), ";")
				found = append(found, media)
			case kind == "image" && media != "":
				found = append(found, media)
			default:
				// What holds pictures deeper down, such as the result of a
				// tool. Fields are walked by name: a list of parts is one
				// field, and keeps its own order.
				for _, name := range slices.Sorted(maps.Keys(value)) {
					walk(value[name])
				}
			}
		}
	}
	walk(value)
	return found
}

// pieces splits text into the words in which it is streamed.
func pieces(text string) []string {
	return strings.SplitAfter(text, " ")
}

func newID(prefix string) string {
	return prefix + strings.ToLower(rand.Text())
}

// stream writes server-sent events to a response.
type stream struct {
	w http.ResponseWriter
}

func newStream(w http.ResponseWriter) stream {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	return stream{w}
}

// send writes one event. The Responses and Messages APIs name their events,
// and the Chat Completions API does not.
func (s stream) send(event string, data any) {
	if event != "" {
		fmt.Fprintf(s.w, "event: %s\n", event)
	}
	if encoded, isRaw := data.(string); isRaw {
		fmt.Fprintf(s.w, "data: %s\n\n", encoded)
	} else {
		encoded, _ := json.Marshal(data)
		fmt.Fprintf(s.w, "data: %s\n\n", encoded)
	}
	http.NewResponseController(s.w).Flush()
}

func refuse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	// A refused caller is told not to try again, in the header that the
	// OpenAI and Anthropic clients read.
	if status == http.StatusUnauthorized {
		w.Header().Set("X-Should-Retry", "false")
	}
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": message, "type": "invalid_request_error"},
	})
}

// withKey passes on only the requests that carry apiKey as a bearer token.
func withKey(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			slog.Info("unauthorized request", "path", r.URL.Path)
			refuse(w, http.StatusUnauthorized, "missing or invalid API key")
			return
		}
		next(w, r)
	}
}

// chatCompletions serves the Chat Completions API, which OpenCode uses.
func (m *mock) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function tool `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		refuse(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !request.Stream {
		refuse(w, http.StatusBadRequest, "mock-llm only streams; set stream to true")
		return
	}
	c := conversation{model: request.Model}
	for i, message := range request.Messages {
		c.pictures = append(c.pictures, pictures(message.Content)...)
		switch message.Role {
		case "user":
			c.hear(texts(message.Content)...)
		case "system", "developer":
			c.sent = append(c.sent, text(message.Content))
		case "tool":
			if i == len(request.Messages)-1 {
				output := text(message.Content)
				c.toolOutput = &output
			}
		}
	}
	var tools []tool
	for _, offered := range request.Tools {
		tools = append(tools, offered.Function)
	}
	c.tools, c.shell = tools, findShell(tools)
	next := m.next(c)
	slog.Info("answered", "api", "chat completions", "said", len(c.said), "call", next.called(), "text", next.text, "failure", next.failure)
	if next.status != 0 {
		refuse(w, next.status, next.failure)
		return
	}

	id, created := newID("chatcmpl-"), time.Now().Unix()
	out := newStream(w)
	chunk := func(delta map[string]any, finish any) {
		data := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": request.Model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		if finish != nil {
			data["usage"] = map[string]int{
				"prompt_tokens": inputTokens, "completion_tokens": outputTokens,
				"total_tokens": inputTokens + outputTokens,
			}
		}
		out.send("", data)
	}
	chunk(map[string]any{"role": "assistant", "content": ""}, nil)
	if next.call == nil {
		for _, piece := range pieces(next.text) {
			chunk(map[string]any{"content": piece}, nil)
			if !next.wait(r) {
				return
			}
		}
		chunk(map[string]any{}, "stop")
	} else {
		call := func(function map[string]any) map[string]any {
			return map[string]any{"tool_calls": []map[string]any{function}}
		}
		chunk(call(map[string]any{
			"index": 0, "id": newID("call_"), "type": "function",
			"function": map[string]string{"name": next.call.name, "arguments": ""},
		}), nil)
		chunk(call(map[string]any{
			"index": 0, "function": map[string]string{"arguments": next.call.arguments},
		}), nil)
		chunk(map[string]any{}, "tool_calls")
	}
	out.send("", "[DONE]")
}

// responses serves the Responses API, which Codex uses.
func (m *mock) responses(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model        string          `json:"model"`
		Stream       bool            `json:"stream"`
		Instructions string          `json:"instructions"`
		Input        json.RawMessage `json:"input"`
		Tools        []tool          `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		refuse(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !request.Stream {
		refuse(w, http.StatusBadRequest, "mock-llm only streams; set stream to true")
		return
	}
	// The input is a string or a list of items: messages, tool calls and
	// their outputs.
	var items []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Output  json.RawMessage `json:"output"`
	}
	c := conversation{model: request.Model, sent: []string{request.Instructions}, pictures: pictures(request.Input)}
	if json.Unmarshal(request.Input, &items) != nil {
		c.said = []string{text(request.Input)}
	}
	for i, item := range items {
		switch {
		case item.Type == "message" && item.Role == "user":
			c.hear(texts(item.Content)...)
		case item.Type == "message" && (item.Role == "system" || item.Role == "developer"):
			c.sent = append(c.sent, text(item.Content))
		case item.Type == "function_call_output" && i == len(items)-1:
			output := text(item.Output)
			c.toolOutput = &output
		}
	}
	c.tools, c.shell = request.Tools, findShell(request.Tools)
	next := m.next(c)
	slog.Info("answered", "api", "responses", "said", len(c.said), "call", next.called(), "text", next.text, "failure", next.failure)
	if next.status != 0 {
		refuse(w, next.status, next.failure)
		return
	}

	response := map[string]any{
		"id": newID("resp_"), "object": "response", "created_at": time.Now().Unix(),
		"status": "in_progress", "model": request.Model, "output": []any{},
	}
	out := newStream(w)
	sequence := 0
	send := func(event string, fields map[string]any) {
		fields["type"] = event
		fields["sequence_number"] = sequence
		sequence++
		out.send(event, fields)
	}
	send("response.created", map[string]any{"response": response})
	send("response.in_progress", map[string]any{"response": response})

	var item map[string]any
	if next.call == nil {
		id := newID("msg_")
		part := func(text string) map[string]any {
			return map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		}
		at := func(fields map[string]any) map[string]any {
			fields["item_id"], fields["output_index"], fields["content_index"] = id, 0, 0
			return fields
		}
		item = map[string]any{"id": id, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
		send("response.output_item.added", map[string]any{"output_index": 0, "item": item})
		send("response.content_part.added", at(map[string]any{"part": part("")}))
		for _, piece := range pieces(next.text) {
			send("response.output_text.delta", at(map[string]any{"delta": piece}))
			if !next.wait(r) {
				return
			}
		}
		send("response.output_text.done", at(map[string]any{"text": next.text}))
		send("response.content_part.done", at(map[string]any{"part": part(next.text)}))
		item = map[string]any{"id": id, "type": "message", "status": "completed", "role": "assistant", "content": []any{part(next.text)}}
	} else {
		id, callID := newID("fc_"), newID("call_")
		arguments := next.call.arguments
		call := func(status, arguments string) map[string]any {
			item := map[string]any{
				"id": id, "type": "function_call", "status": status,
				"call_id": callID, "name": next.call.name, "arguments": arguments,
			}
			if next.call.namespace != "" {
				item["namespace"] = next.call.namespace
			}
			return item
		}
		item = call("completed", arguments)
		send("response.output_item.added", map[string]any{"output_index": 0, "item": call("in_progress", "")})
		send("response.function_call_arguments.delta", map[string]any{"item_id": id, "output_index": 0, "delta": arguments})
		send("response.function_call_arguments.done", map[string]any{"item_id": id, "output_index": 0, "name": next.call.name, "arguments": arguments})
	}
	send("response.output_item.done", map[string]any{"output_index": 0, "item": item})
	response["status"] = "completed"
	response["output"] = []any{item}
	response["usage"] = map[string]any{
		"input_tokens": inputTokens, "input_tokens_details": map[string]int{"cached_tokens": 0},
		"output_tokens": outputTokens, "output_tokens_details": map[string]int{"reasoning_tokens": 0},
		"total_tokens": inputTokens + outputTokens,
	}
	send("response.completed", map[string]any{"response": response})
}

// messages serves the Messages API, which Claude Code uses.
func (m *mock) messages(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model    string          `json:"model"`
		Stream   bool            `json:"stream"`
		System   json.RawMessage `json:"system"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name        string `json:"name"`
			InputSchema schema `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		refuse(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !request.Stream {
		refuse(w, http.StatusBadRequest, "mock-llm only streams; set stream to true")
		return
	}
	c := conversation{model: request.Model, sent: []string{text(request.System)}}
	// The names of the tools that the model called, by the IDs of the calls.
	called := map[string]string{}
	for i, message := range request.Messages {
		c.pictures = append(c.pictures, pictures(message.Content)...)
		// A harness can add instructions in the conversation, too.
		if message.Role == "system" {
			c.sent = append(c.sent, text(message.Content))
		}
		// The content is a string or a list of blocks, such as text, tool
		// calls and their results.
		var blocks []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Content   json.RawMessage `json:"content"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			ToolUseID string          `json:"tool_use_id"`
		}
		if message.Role == "assistant" && json.Unmarshal(message.Content, &blocks) == nil {
			for _, block := range blocks {
				if block.Type == "tool_use" {
					called[block.ID] = block.Name
				}
			}
		}
		if message.Role != "user" {
			continue
		}
		if json.Unmarshal(message.Content, &blocks) != nil {
			c.hear(text(message.Content))
			continue
		}
		var parts []string
		for _, block := range blocks {
			switch {
			case block.Type == "text":
				parts = append(parts, block.Text)
				// The user's next message can follow a tool result in the
				// same message, as after a refused tool call.
				if _, _, found := instruction(block.Text); found {
					c.toolOutput = nil
				}
			case block.Type == "tool_result" && i == len(request.Messages)-1:
				output := text(block.Content)
				c.toolOutput, c.searched = &output, called[block.ToolUseID] == toolSearch
			}
		}
		c.hear(parts...)
	}
	var tools []tool
	for _, offered := range request.Tools {
		tools = append(tools, tool{Name: offered.Name, Parameters: offered.InputSchema})
	}
	c.tools, c.shell = tools, findShell(tools)
	next := m.next(c)
	slog.Info("answered", "api", "messages", "said", len(c.said), "call", next.called(), "text", next.text, "failure", next.failure)
	if next.status != 0 {
		refuse(w, next.status, next.failure)
		return
	}

	out := newStream(w)
	send := func(event string, fields map[string]any) {
		fields["type"] = event
		out.send(event, fields)
	}
	send("message_start", map[string]any{"message": map[string]any{
		"id": newID("msg_"), "type": "message", "role": "assistant", "model": request.Model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": inputTokens, "output_tokens": 0},
	}})
	stop := "end_turn"
	if next.call == nil {
		send("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		for _, piece := range pieces(next.text) {
			send("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": piece}})
			if !next.wait(r) {
				return
			}
		}
	} else {
		stop = "tool_use"
		send("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{
			"type": "tool_use", "id": newID("toolu_"), "name": next.call.name, "input": map[string]any{},
		}})
		send("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{
			"type": "input_json_delta", "partial_json": next.call.arguments,
		}})
	}
	send("content_block_stop", map[string]any{"index": 0})
	// The usage of a message_delta event is the total so far.
	send("message_delta", map[string]any{
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": outputTokens},
	})
	send("message_stop", map[string]any{})
}
