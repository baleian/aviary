// Command mock-llm stands in for a model server in tests. It speaks the two
// protocols that the harnesses use, the Chat Completions API and the
// Responses API of OpenAI, both as streams. Instead of a model, a script
// answers: the last message of the user is an instruction.
//
//	say <text>     replies with the text
//	drawl <text>   replies with the text slowly, a word every half second,
//	               which leaves time to stop the turn in the middle of it
//	run <command>  runs the command with the harness's shell tool, then
//	               replies with what the tool returned
//	escalate <command>
//	               does what run does, and asks for the command to be run
//	               outside the sandbox of the harness, if its shell tool
//	               takes such a request. A harness that asks a person
//	               before it acts asks then
//	recall         replies with the instructions that the user gave before,
//	               one per line, or "nothing" if there are none
//	find <text>    replies "present" if the harness sent the text along with
//	               the conversation, as it does with the instructions of an
//	               agent, and "absent" if it did not
//	fail <text>    refuses the request with the text as the error, the way
//	               a model server refuses a caller that it does not know
//	flaky <text>   refuses the request once, the way a model server does that
//	               cannot take it right now, and replies with the text when
//	               the harness asks again
//
// A message that is not an instruction gets the reply "mock".
//
// A request must carry the API key that mock-llm was started with, which is
// in the variable MOCK_LLM_API_KEY. Every answer reports the same token
// usage, 11 tokens in and 7 out.
//
// mock-llm is also the forward proxy of the tests that run a harness without
// a cluster. As a proxy it passes nothing on: it refuses the request and logs
// the host that was asked for, which tells a test what went out.
package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// The token usage that every answer reports.
const (
	inputTokens  = 11
	outputTokens = 7
)

// drawlPause is how long a slow answer waits after each of its pieces.
const drawlPause = 500 * time.Millisecond

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	apiKey := os.Getenv("MOCK_LLM_API_KEY")
	if apiKey == "" {
		slog.Error("MOCK_LLM_API_KEY is not set")
		os.Exit(1)
	}
	mock := newMock(apiKey)
	server := &http.Server{Addr: ":8080", Handler: mock, ReadHeaderTimeout: 10 * time.Second}
	failed := make(chan error, 2)
	slog.Info("listening", "addr", server.Addr)
	go func() { failed <- server.ListenAndServe() }()
	// Over TLS too, with the certificate that is given, as a model endpoint
	// on the internet answers.
	if certificate := os.Getenv("MOCK_LLM_TLS_CERTIFICATE"); certificate != "" {
		secured := &http.Server{Addr: ":8443", Handler: mock, ReadHeaderTimeout: 10 * time.Second}
		slog.Info("listening", "addr", secured.Addr, "tls", true)
		go func() { failed <- secured.ListenAndServeTLS(certificate, os.Getenv("MOCK_LLM_TLS_KEY")) }()
	}
	slog.Error("mock-llm stopped", "error", <-failed)
	os.Exit(1)
}

// mock is the model server. It keeps nothing of a conversation: a harness
// sends all of it with every request.
type mock struct {
	mu sync.Mutex
	// refused holds the texts of the flaky instructions that were refused
	// once.
	refused map[string]bool
}

// newMock returns the model server, which answers the requests that carry
// the API key.
func newMock(apiKey string) http.Handler {
	m := &mock{refused: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /v1/chat/completions", withKey(apiKey, m.chatCompletions))
	mux.HandleFunc("POST /v1/responses", withKey(apiKey, m.responses))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A request for another host takes mock-llm for a forward proxy.
		if r.Method == http.MethodConnect || r.URL.Host != "" {
			slog.Info("asked for", "host", r.URL.Hostname())
			refuse(w, http.StatusForbidden, "mock-llm passes nothing on")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// next is what the model does next in the conversation.
func (m *mock) next(c conversation) answer {
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
	return answer{failure: "the model cannot take the request right now", status: http.StatusServiceUnavailable}
}

// conversation is what a harness sends, in the terms of the script.
type conversation struct {
	// said holds the messages of the user, oldest first.
	said []string
	// sent holds what else the harness sent of its own: what it tells the
	// model before the conversation, and what it adds to the conversation.
	sent []string
	// toolOutput is what the tool returned, if the conversation ends with it.
	toolOutput *string
	// shell is the harness's tool that runs a command, if it offers one.
	shell *shellTool
}

type shellTool struct {
	name string
	// argument is the name of the parameter that takes the command.
	argument string
	// sandboxed is set for a tool that keeps a command in a sandbox unless
	// the model asks otherwise.
	sandboxed bool
}

// outside is what the model asks a sandboxed tool with to run a command
// outside its sandbox. These are the parameters of Codex's tool.
var outside = map[string]string{
	"sandbox_permissions": "require_escalated",
	"justification":       "the script says so",
}

// arguments returns the arguments of the call that has the tool do what the
// answer says.
func (s shellTool) arguments(a answer) string {
	arguments := map[string]string{s.argument: a.command}
	if a.outside && s.sandboxed {
		maps.Copy(arguments, outside)
	}
	encoded, _ := json.Marshal(arguments)
	return string(encoded)
}

// answer is what the model does next: it calls the shell tool with command,
// which it asks to be run outside the sandbox if outside is set, or else it
// says text, with a pause after each piece. If failure is set there is no
// answer: the request is refused with it and with the status. An answer
// that is flaky is given only when it was refused once.
type answer struct {
	text    string
	pause   time.Duration
	command string
	outside bool
	failure string
	status  int
	flaky   bool
}

// wait pauses after a piece of the answer. It reports false if the harness
// has gone meanwhile.
func (a answer) wait(r *http.Request) bool {
	select {
	case <-time.After(a.pause):
		return true
	case <-r.Context().Done():
		return false
	}
}

// hear takes in a message with the role of the user. A harness adds such
// messages of its own, which are no instructions to the script.
func (c *conversation) hear(message string) {
	c.said = append(c.said, message)
	if _, _, found := instruction(message); !found {
		c.sent = append(c.sent, message)
	}
}

func (c conversation) next() answer {
	if c.toolOutput != nil {
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
		// Both harnesses give up at once on this status and report it as an
		// error. An error of another kind one of them repeats for a while,
		// and one ends the turn with the error as its answer.
		return answer{failure: rest, status: http.StatusUnauthorized}
	case word == "flaky":
		return answer{text: rest, flaky: true}
	case (word == "run" || word == "escalate") && c.shell == nil:
		return answer{text: "mock: the harness offers no tool that runs a command"}
	case word == "run" || word == "escalate":
		return answer{command: rest, outside: word == "escalate"}
	case word == "find" && strings.Contains(strings.Join(c.sent, "\n"), rest):
		return answer{text: "present"}
	case word == "find":
		return answer{text: "absent"}
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

// instruction splits a message of the user into the word of the script that
// it begins with and the rest. Harnesses add messages of their own to a
// conversation, which begin with no such word.
func instruction(message string) (word, rest string, found bool) {
	word, rest, _ = strings.Cut(strings.TrimSpace(message), " ")
	switch word {
	case "say", "drawl", "run", "escalate", "recall", "find", "fail", "flaky":
		return word, strings.TrimSpace(rest), true
	}
	return "", "", false
}

// tool is a tool that a harness offers the model.
type tool struct {
	Name       string `json:"name"`
	Parameters struct {
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"parameters"`
}

// findShell picks the tool that runs a command: the first that takes one.
func findShell(tools []tool) *shellTool {
	for _, tool := range tools {
		for _, argument := range []string{"command", "cmd"} {
			if _, found := tool.Parameters.Properties[argument]; found {
				_, sandboxed := tool.Parameters.Properties["sandbox_permissions"]
				return &shellTool{name: tool.Name, argument: argument, sandboxed: sandboxed}
			}
		}
	}
	return nil
}

// text reads content that is either a string or a list of parts with text.
func text(content json.RawMessage) string {
	var whole string
	if json.Unmarshal(content, &whole) == nil {
		return whole
	}
	var parts []struct {
		Text string `json:"text"`
	}
	json.Unmarshal(content, &parts)
	var joined strings.Builder
	for _, part := range parts {
		joined.WriteString(part.Text)
	}
	return joined.String()
}

// pieces cuts a text into the pieces in which it is streamed.
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

// send writes one event. The Responses API names its events and the Chat
// Completions API does not.
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
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": message, "type": "invalid_request_error"},
	})
}

// withKey passes on the requests that carry the API key as a bearer token.
func withKey(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			slog.Info("refused", "path", r.URL.Path)
			refuse(w, http.StatusUnauthorized, "the request does not carry the API key of mock-llm")
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
		refuse(w, http.StatusBadRequest, "the request is not JSON: "+err.Error())
		return
	}
	if !request.Stream {
		refuse(w, http.StatusBadRequest, "mock-llm only streams; set stream to true")
		return
	}
	var c conversation
	for i, message := range request.Messages {
		switch message.Role {
		case "user":
			c.hear(text(message.Content))
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
	c.shell = findShell(tools)
	next := m.next(c)
	slog.Info("answered", "api", "chat completions", "said", len(c.said), "command", next.command, "text", next.text, "failure", next.failure)
	if next.failure != "" {
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
	if next.command == "" {
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
			"function": map[string]string{"name": c.shell.name, "arguments": ""},
		}), nil)
		chunk(call(map[string]any{
			"index": 0, "function": map[string]string{"arguments": c.shell.arguments(next)},
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
		refuse(w, http.StatusBadRequest, "the request is not JSON: "+err.Error())
		return
	}
	if !request.Stream {
		refuse(w, http.StatusBadRequest, "mock-llm only streams; set stream to true")
		return
	}
	// The input is a text or a list of items: messages, tool calls and their
	// outputs.
	var items []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Output  json.RawMessage `json:"output"`
	}
	c := conversation{sent: []string{request.Instructions}}
	if json.Unmarshal(request.Input, &items) != nil {
		c.said = []string{text(request.Input)}
	}
	for i, item := range items {
		switch {
		case item.Type == "message" && item.Role == "user":
			c.hear(text(item.Content))
		case item.Type == "message" && (item.Role == "system" || item.Role == "developer"):
			c.sent = append(c.sent, text(item.Content))
		case item.Type == "function_call_output" && i == len(items)-1:
			output := text(item.Output)
			c.toolOutput = &output
		}
	}
	c.shell = findShell(request.Tools)
	next := m.next(c)
	slog.Info("answered", "api", "responses", "said", len(c.said), "command", next.command, "text", next.text, "failure", next.failure)
	if next.failure != "" {
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
	if next.command == "" {
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
		arguments := c.shell.arguments(next)
		call := func(status, arguments string) map[string]any {
			return map[string]any{
				"id": id, "type": "function_call", "status": status,
				"call_id": callID, "name": c.shell.name, "arguments": arguments,
			}
		}
		item = call("completed", arguments)
		send("response.output_item.added", map[string]any{"output_index": 0, "item": call("in_progress", "")})
		send("response.function_call_arguments.delta", map[string]any{"item_id": id, "output_index": 0, "delta": arguments})
		send("response.function_call_arguments.done", map[string]any{"item_id": id, "output_index": 0, "name": c.shell.name, "arguments": arguments})
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
