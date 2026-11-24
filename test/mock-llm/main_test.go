package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const apiKey = "the-key"

// sent is what a harness sends the model: its instructions, the user messages
// so far and, if the model last called the tool, the tool output.
type sent struct {
	system     string
	said       []string
	toolOutput string
	// noTool is set for a harness that offers the model no tool.
	noTool bool
	// sandboxed is set for a harness whose tool runs commands in a sandbox
	// unless the model asks otherwise, as Codex's does.
	sandboxed bool
}

// parameters returns the parameters of the harness's tool, with command as
// the name of the one that takes the command.
func (s sent) parameters(command string) map[string]any {
	properties := map[string]any{command: map[string]any{}}
	if s.sandboxed {
		properties["sandbox_permissions"], properties["justification"] = map[string]any{}, map[string]any{}
	}
	return map[string]any{"properties": properties}
}

// reply is the model's answer: text, a tool call with a command, or a
// refusal.
type reply struct {
	status  int
	refusal string
	text    string
	tool    string
	command string
	// others holds the other arguments of the tool call.
	others map[string]string
	usage  map[string]float64
}

// events sends a request to the model and returns the response status with
// either the events of the stream or the refusal message.
func events(t *testing.T, url, key string, request any) (int, []map[string]any, string) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	post, err := http.NewRequest("POST", url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var refusal struct{ Error struct{ Message string } }
		answer, _ := io.ReadAll(response.Body)
		if err := json.Unmarshal(answer, &refusal); err != nil {
			t.Fatalf("decode error response %s: %v", answer, err)
		}
		return response.StatusCode, nil, refusal.Error.Message
	}
	var stream []map[string]any
	lines := bufio.NewScanner(response.Body)
	lines.Buffer(nil, 1<<20)
	for lines.Scan() {
		data, found := strings.CutPrefix(lines.Text(), "data: ")
		if !found || data == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("decode stream line %q: %v", lines.Text(), err)
		}
		stream = append(stream, event)
	}
	return response.StatusCode, stream, ""
}

// at returns the value at path in a JSON value, given as field names and
// list indexes, or nil.
func at(value any, path ...any) any {
	for _, step := range path {
		switch step := step.(type) {
		case string:
			object, _ := value.(map[string]any)
			value = object[step]
		case int:
			list, _ := value.([]any)
			if step >= len(list) {
				return nil
			}
			value = list[step]
		}
	}
	return value
}

func str(value any) string {
	text, _ := value.(string)
	return text
}

func num(value any) float64 {
	number, _ := value.(float64)
	return number
}

// called parses the arguments of a tool call into the command, under either
// parameter name, and the others.
func (r *reply) called(arguments string) {
	json.Unmarshal([]byte(arguments), &r.others)
	for _, name := range []string{"command", "cmd"} {
		r.command += r.others[name]
		delete(r.others, name)
	}
}

// chat talks to the model as OpenCode does, with the Chat Completions API.
func chat(t *testing.T, url string, conversation sent) reply {
	t.Helper()
	messages := []map[string]any{{"role": "system", "content": conversation.system}}
	for _, message := range conversation.said {
		messages = append(messages, map[string]any{"role": "user", "content": message})
	}
	if conversation.toolOutput != "" {
		messages = append(messages, map[string]any{"role": "assistant", "content": ""},
			map[string]any{"role": "tool", "content": conversation.toolOutput})
	}
	request := map[string]any{"model": "mock", "stream": true, "messages": messages}
	if !conversation.noTool {
		request["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "bash", "parameters": conversation.parameters("command"),
		}}}
	}
	status, stream, refusal := events(t, url+"/v1/chat/completions", apiKey, request)
	answer := reply{status: status, refusal: refusal}
	var arguments string
	for _, chunk := range stream {
		answer.text += str(at(chunk, "choices", 0, "delta", "content"))
		call := at(chunk, "choices", 0, "delta", "tool_calls", 0, "function")
		answer.tool += str(at(call, "name"))
		arguments += str(at(call, "arguments"))
		if usage, reported := chunk["usage"].(map[string]any); reported {
			answer.usage = map[string]float64{
				"input": num(usage["prompt_tokens"]), "output": num(usage["completion_tokens"]),
				"total": num(usage["total_tokens"]),
			}
		}
	}
	answer.called(arguments)
	return answer
}

// respond talks to the model as Codex does, with the Responses API.
func respond(t *testing.T, url string, conversation sent) reply {
	t.Helper()
	input := []map[string]any{}
	for _, message := range conversation.said {
		input = append(input, map[string]any{
			"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": message}},
		})
	}
	if conversation.toolOutput != "" {
		input = append(input, map[string]any{"type": "function_call", "name": "shell", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "output": conversation.toolOutput})
	}
	request := map[string]any{"model": "mock", "stream": true, "instructions": conversation.system, "input": input}
	if !conversation.noTool {
		request["tools"] = []any{map[string]any{
			"type": "function", "name": "shell", "parameters": conversation.parameters("cmd"),
		}}
	}
	status, stream, refusal := events(t, url+"/v1/responses", apiKey, request)
	answer := reply{status: status, refusal: refusal}
	for _, event := range stream {
		switch event["type"] {
		case "response.output_text.delta":
			answer.text += str(event["delta"])
		case "response.function_call_arguments.done":
			answer.tool = str(event["name"])
			answer.called(str(event["arguments"]))
		case "response.completed":
			usage, _ := at(event, "response", "usage").(map[string]any)
			answer.usage = map[string]float64{
				"input": num(usage["input_tokens"]), "output": num(usage["output_tokens"]),
				"total": num(usage["total_tokens"]),
			}
		}
	}
	return answer
}

// message talks to the model as Claude Code does, with the Messages API.
func message(t *testing.T, url string, conversation sent) reply {
	t.Helper()
	text := func(text string) []any { return []any{map[string]any{"type": "text", "text": text}} }
	messages := []map[string]any{}
	for _, said := range conversation.said {
		messages = append(messages, map[string]any{"role": "user", "content": text(said)})
	}
	if conversation.toolOutput != "" {
		messages = append(messages,
			map[string]any{"role": "assistant", "content": []any{map[string]any{
				"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{},
			}}},
			map[string]any{"role": "user", "content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": "toolu_1", "content": conversation.toolOutput,
			}}})
	}
	request := map[string]any{"model": "mock", "stream": true, "system": text(conversation.system), "messages": messages}
	if !conversation.noTool {
		request["tools"] = []any{map[string]any{"name": "Bash", "input_schema": conversation.parameters("command")}}
	}
	status, stream, refusal := events(t, url+"/v1/messages", apiKey, request)
	answer := reply{status: status, refusal: refusal}
	var arguments string
	for _, event := range stream {
		switch event["type"] {
		case "message_start":
			answer.usage = map[string]float64{"input": num(at(event, "message", "usage", "input_tokens"))}
		case "content_block_start":
			answer.tool += str(at(event, "content_block", "name"))
		case "content_block_delta":
			answer.text += str(at(event, "delta", "text"))
			arguments += str(at(event, "delta", "partial_json"))
		case "message_delta":
			answer.usage["output"] = num(at(event, "usage", "output_tokens"))
			answer.usage["total"] = answer.usage["input"] + answer.usage["output"]
		}
	}
	answer.called(arguments)
	return answer
}

// The script works the same on every API.
func TestScript(t *testing.T) {
	for api, ask := range map[string]func(*testing.T, string, sent) reply{"chat completions": chat, "responses": respond, "messages": message} {
		t.Run(api, func(t *testing.T) {
			t.Parallel()
			model := httptest.NewServer(newMock(apiKey))
			defer model.Close()
			for _, test := range []struct {
				name string
				sent sent
				// The expected text, or the command of the tool call and
				// whether it asks to run outside the sandbox.
				text, command string
				outside       bool
			}{
				{name: "say", sent: sent{said: []string{"say hello there"}}, text: "hello there"},
				{name: "no instruction", sent: sent{said: []string{"hello"}}, text: "mock"},
				{name: "run", sent: sent{said: []string{"run ls -a"}}, command: "ls -a"},
				{
					name: "run without a tool", sent: sent{said: []string{"run ls -a"}, noTool: true},
					text: "mock: the harness offers no tool that runs a command",
				},
				{name: "tool output", sent: sent{said: []string{"run ls -a"}, toolOutput: ". .. notes.txt"}, text: ". .. notes.txt"},
				// Only a sandboxed tool is asked to run a command outside its
				// sandbox, and only by escalate.
				{name: "escalate", sent: sent{said: []string{"escalate ls -a"}, sandboxed: true}, command: "ls -a", outside: true},
				{name: "escalate without a sandbox", sent: sent{said: []string{"escalate ls -a"}}, command: "ls -a"},
				{name: "run in a sandbox", sent: sent{said: []string{"run ls -a"}, sandboxed: true}, command: "ls -a"},
				{
					name: "escalate without a tool", sent: sent{said: []string{"escalate ls -a"}, noTool: true},
					text: "mock: the harness offers no tool that runs a command",
				},
				{
					name: "tool output outside the sandbox",
					sent: sent{said: []string{"escalate ls -a"}, toolOutput: ". .. notes.txt", sandboxed: true}, text: ". .. notes.txt",
				},
				{name: "recall with no history", sent: sent{said: []string{"recall"}}, text: "nothing"},
				{
					// Messages that the harness adds are not instructions.
					name: "recall", sent: sent{said: []string{"say one", "<context>today</context>", "run ls", "escalate ls", "recall"}},
					text: "say one\nrun ls\nescalate ls",
				},
				{
					name: "find in the instructions",
					sent: sent{system: "The word of the day is plum.", said: []string{"find plum"}}, text: "present",
				},
				{
					name: "find in a message added by the harness",
					sent: sent{said: []string{"<context>plum</context>", "find plum"}}, text: "present",
				},
				{
					name: "find text that was not sent",
					sent: sent{system: "The word of the day is plum.", said: []string{"say cherry", "find cherry"}}, text: "absent",
				},
			} {
				answer := ask(t, model.URL, test.sent)
				others := map[string]string{}
				if test.outside {
					others = map[string]string{"sandbox_permissions": "require_escalated", "justification": "the script says so"}
				}
				if answer.status != http.StatusOK || answer.text != test.text || answer.command != test.command ||
					(test.command != "") != (answer.tool != "") || !maps.Equal(answer.others, others) {
					t.Errorf("%s: got %+v", test.name, answer)
				}
				if answer.usage["input"] != 11 || answer.usage["output"] != 7 || answer.usage["total"] != 18 {
					t.Errorf("%s: usage = %v, want 11 input, 7 output, 18 total", test.name, answer.usage)
				}
			}

			refused := ask(t, model.URL, sent{said: []string{"fail the model is out of order"}})
			if refused.status != http.StatusUnauthorized || refused.refusal != "the model is out of order" {
				t.Errorf("fail: got %+v, want 401 with the text as the error", refused)
			}
			if bare := ask(t, model.URL, sent{said: []string{"fail"}}); bare.status != http.StatusUnauthorized {
				t.Errorf("fail without text: got %+v, want 401", bare)
			}

			busy := sent{said: []string{"flaky later " + api}}
			if first := ask(t, model.URL, busy); first.status != http.StatusServiceUnavailable {
				t.Errorf("flaky: first answer = %+v, want 503", first)
			}
			for range 2 {
				if again := ask(t, model.URL, busy); again.status != http.StatusOK || again.text != "later "+api {
					t.Errorf("flaky: later answer = %+v, want the text", again)
				}
			}
		})
	}
}

// Conversations of the Messages API in the shapes that Claude Code sends.
func TestMessages(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	toolUse := map[string]any{"role": "assistant", "content": []any{map[string]any{
		"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{},
	}}}
	for _, test := range []struct {
		name     string
		messages []any
		text     string
	}{
		{
			name: "system message in the conversation",
			messages: []any{
				map[string]any{"role": "user", "content": "say hello"},
				map[string]any{"role": "system", "content": []any{map[string]any{"type": "text", "text": "Today is plum day."}}},
				map[string]any{"role": "assistant", "content": "hello"},
				map[string]any{"role": "user", "content": "find plum"},
			},
			text: "present",
		},
		{
			// As after a refused tool call.
			name: "message after a tool result",
			messages: []any{
				map[string]any{"role": "user", "content": "run ls"},
				toolUse,
				map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "refused"},
					map[string]any{"type": "text", "text": "say next"},
				}},
			},
			text: "next",
		},
	} {
		status, stream, _ := events(t, model.URL+"/v1/messages", apiKey, map[string]any{"stream": true, "messages": test.messages})
		var said string
		for _, event := range stream {
			said += str(at(event, "delta", "text"))
		}
		if status != http.StatusOK || said != test.text {
			t.Errorf("%s: got status %d with text %q, want %q", test.name, status, said, test.text)
		}
	}
}

func TestDrawl(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	began := time.Now()
	answer := chat(t, model.URL, sent{said: []string{"drawl one two"}})
	if answer.text != "one two" || time.Since(began) < 2*drawlPause {
		t.Errorf("drawl: got %q after %s", answer.text, time.Since(began))
	}
}

func TestRequests(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	messages := []map[string]any{{"role": "user", "content": "say hello"}}
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		// Only the Egress Proxy holds the key. A sandbox has only its session
		// token.
		for _, key := range []string{"", "a-session-token"} {
			status, _, _ := events(t, model.URL+path, key, map[string]any{"stream": true, "messages": messages})
			if status != http.StatusUnauthorized {
				t.Errorf("%s with key %q: got status %d, want 401", path, key, status)
			}
		}
		if status, _, _ := events(t, model.URL+path, apiKey, map[string]any{"messages": messages}); status != http.StatusBadRequest {
			t.Errorf("%s without stream: got status %d, want 400", path, status)
		}
	}
	// A refused caller is told not to try again.
	refused, err := http.Post(model.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	refused.Body.Close()
	if again := refused.Header.Get("X-Should-Retry"); refused.StatusCode != http.StatusUnauthorized || again != "false" {
		t.Errorf("request without the key: got %d with X-Should-Retry %q, want 401 with false", refused.StatusCode, again)
	}
	if response, err := http.Get(model.URL + "/healthz"); err != nil || response.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz: got %v: %v", response, err)
	}
	// The models are listed for a caller with the key.
	for key, status := range map[string]int{apiKey: http.StatusOK, "a-session-token": http.StatusUnauthorized} {
		request, err := http.NewRequest("GET", model.URL+"/v1/models", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+key)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		listed, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != status || (status == http.StatusOK && !strings.Contains(string(listed), `"id": "mock"`)) {
			t.Errorf("GET /v1/models with key %q: got %d %s, want %d", key, response.StatusCode, listed, status)
		}
	}
}

// As a forward proxy, mock-llm refuses every request.
func TestProxy(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	proxy, err := url.Parse(model.URL)
	if err != nil {
		t.Fatal(err)
	}
	through := http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	if response, err := through.Get("http://example.com/v1/chat/completions"); err != nil || response.StatusCode != http.StatusForbidden {
		t.Errorf("request through the proxy: got %v: %v, want 403", response, err)
	}
	// An HTTPS request asks for a tunnel first, whose refusal is an error.
	if response, err := through.Get("https://example.com/"); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("tunnel through the proxy: got %v: %v, want a Forbidden error", response, err)
	}
}

// The wheel is served without the API key.
func TestPackage(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	response, err := http.Get(model.URL + "/packages/" + probeWheel)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	served, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("GET package: got status %d: %v", response.StatusCode, err)
	}
	archive, err := zip.NewReader(bytes.NewReader(served), int64(len(served)))
	if err != nil {
		t.Fatalf("package is not a zip archive: %v", err)
	}
	var names []string
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	if !slices.Equal(names, []string{"aviary_probe/__init__.py", "aviary_probe-1.0.dist-info/METADATA",
		"aviary_probe-1.0.dist-info/WHEEL", "aviary_probe-1.0.dist-info/RECORD"}) {
		t.Errorf("wheel files = %v", names)
	}
}

// use calls the first tool whose name names the MCP server and ends with the
// tool's, in each API's shape of a tool list, and replies with its output.
// read calls the tool of the harness that reads a file, not one that takes a
// path for something else, with the argument by which that tool names the
// file.
// What a harness puts after an instruction in its message, as it does with
// the links to the files that the message carries, is attached to the
// instruction and not of it, on every API.
func TestAttached(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	// Each API with how it shapes a part of a user's message, and where the
	// model's text is in the events of its stream.
	for api, shape := range map[string]struct {
		part string
		text []any
	}{
		"/v1/chat/completions": {"text", []any{"choices", 0, "delta", "content"}},
		"/v1/responses":        {"input_text", []any{"delta"}},
		"/v1/messages":         {"text", []any{"delta", "text"}},
	} {
		// ask sends the user's messages, each of them its parts, and returns
		// what the model says.
		ask := func(messages ...[]string) string {
			var conversation []any
			for _, parts := range messages {
				var content []any
				for _, part := range parts {
					content = append(content, map[string]any{"type": shape.part, "text": part})
				}
				conversation = append(conversation, map[string]any{"type": "message", "role": "user", "content": content})
			}
			request := map[string]any{"model": "mock", "stream": true, "messages": conversation, "input": conversation}
			status, stream, refusal := events(t, model.URL+api, apiKey, request)
			if status != http.StatusOK {
				t.Fatalf("%s: got status %d %s", api, status, refusal)
			}
			var said string
			for _, event := range stream {
				if api != "/v1/responses" || event["type"] == "response.output_text.delta" {
					said += str(at(event, shape.text...))
				}
			}
			return said
		}
		links := []string{"[@notes-k.txt](file:///home/.attachments/1/notes-k.txt)", "[@notes-a.txt](file:///home/.attachments/1/notes-a.txt)"}
		with := func(parts ...string) []string { return append(parts, links...) }
		for _, test := range []struct {
			name     string
			messages [][]string
			text     string
		}{
			{"what follows the instruction", [][]string{with("attached")}, strings.Join(links, "\n")},
			{"not what the harness puts before it", [][]string{with("<system-reminder>be kind</system-reminder>", "attached")}, strings.Join(links, "\n")},
			{"nothing", [][]string{{"attached"}}, "nothing"},
			{"an instruction is not what follows it", [][]string{with("say hello")}, "hello"},
			{"a message has only what follows its own instruction", [][]string{with("say hello"), {"attached"}}, "nothing"},
			{"what follows was sent along", [][]string{with("find notes-a.txt")}, "present"},
			{"and is found only then", [][]string{{"find notes-a.txt"}}, "absent"},
		} {
			if said := ask(test.messages...); said != test.text {
				t.Errorf("%s, %s: the model said %q, want %q", api, test.name, said, test.text)
			}
		}
	}
}

func TestRead(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	// takes returns the parameters of a tool: those that it needs, then
	// those that it takes besides.
	takes := func(required []string, optional ...string) map[string]any {
		properties := map[string]any{}
		for _, argument := range slices.Concat(required, optional) {
			properties[argument] = map[string]any{}
		}
		return map[string]any{"properties": properties, "required": required}
	}
	for _, test := range []struct {
		api   string
		tools []any
		// The tool that is called, and its arguments.
		name, arguments string
	}{
		{
			api: "/v1/chat/completions", name: "read", arguments: `{"filePath":"/home/notes.txt"}`,
			tools: []any{
				map[string]any{"type": "function", "function": map[string]any{"name": "bash", "parameters": takes([]string{"command"})}},
				map[string]any{"type": "function", "function": map[string]any{"name": "write", "parameters": takes([]string{"filePath", "content"})}},
				map[string]any{"type": "function", "function": map[string]any{"name": "list", "parameters": takes(nil, "path", "ignore")}},
				map[string]any{"type": "function", "function": map[string]any{"name": "read", "parameters": takes([]string{"filePath"}, "offset", "limit")}},
			},
		},
		{
			api: "/v1/responses", name: "view_image", arguments: `{"path":"/home/notes.txt"}`,
			tools: []any{
				map[string]any{"type": "function", "name": "exec_command", "parameters": takes([]string{"cmd"}, "workdir")},
				map[string]any{"type": "function", "name": "view_image", "parameters": takes([]string{"path"})},
			},
		},
		{
			api: "/v1/messages", name: "Read", arguments: `{"file_path":"/home/notes.txt"}`,
			tools: []any{
				map[string]any{"name": "Grep", "input_schema": takes([]string{"pattern"}, "path")},
				map[string]any{"name": "Edit", "input_schema": takes([]string{"file_path", "old_string", "new_string"})},
				map[string]any{"name": "Read", "input_schema": takes([]string{"file_path"}, "offset", "limit", "pages", "allow_large")},
			},
		},
		// A harness without such a tool is told so.
		{
			api:   "/v1/chat/completions",
			tools: []any{map[string]any{"type": "function", "function": map[string]any{"name": "bash", "parameters": takes([]string{"command"})}}},
		},
	} {
		request := map[string]any{"model": "mock", "stream": true, "tools": test.tools,
			"messages": []any{map[string]any{"role": "user", "content": "read /home/notes.txt"}},
			"input":    []any{map[string]any{"type": "message", "role": "user", "content": "read /home/notes.txt"}},
		}
		status, stream, _ := events(t, model.URL+test.api, apiKey, request)
		var name, arguments, said string
		for _, event := range stream {
			name += str(at(event, "choices", 0, "delta", "tool_calls", 0, "function", "name")) + str(at(event, "content_block", "name"))
			arguments += str(at(event, "choices", 0, "delta", "tool_calls", 0, "function", "arguments")) + str(at(event, "delta", "partial_json"))
			said += str(at(event, "choices", 0, "delta", "content"))
			if event["type"] == "response.output_item.done" {
				name, arguments = str(at(event, "item", "name")), str(at(event, "item", "arguments"))
			}
		}
		if status != http.StatusOK || name != test.name || arguments != test.arguments {
			t.Errorf("%s: got %d with %q %q, want %q %q", test.api, status, name, arguments, test.name, test.arguments)
		}
		if test.name == "" && !strings.Contains(said, "no tool that reads a file") {
			t.Errorf("%s: a harness without a tool that reads a file was told %q", test.api, said)
		}
	}
}

func TestUse(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	shell := map[string]any{"properties": map[string]any{"command": map[string]any{}}}
	user := func(text string) map[string]any { return map[string]any{"role": "user", "content": text} }
	for _, test := range []struct {
		api   string
		tools []any
		// The namespace and the name of the tool that is called.
		namespace, name string
	}{
		{
			api: "/v1/chat/completions", name: "web_headers",
			tools: []any{
				map[string]any{"type": "function", "function": map[string]any{"name": "bash", "parameters": shell}},
				map[string]any{"type": "function", "function": map[string]any{"name": "webhooks_headers"}},
				map[string]any{"type": "function", "function": map[string]any{"name": "webheaders"}},
				map[string]any{"type": "function", "function": map[string]any{"name": "web_headers"}},
			},
		},
		{
			api: "/v1/responses", namespace: "mcp__web", name: "headers",
			tools: []any{
				map[string]any{"type": "function", "name": "shell", "parameters": shell},
				map[string]any{"type": "namespace", "name": "mcp__webhooks", "tools": []any{
					map[string]any{"type": "function", "name": "headers"},
				}},
				map[string]any{"type": "namespace", "name": "mcp__web", "tools": []any{
					map[string]any{"type": "function", "name": "headers"},
				}},
			},
		},
		{
			api: "/v1/messages", name: "mcp__web__headers",
			tools: []any{
				map[string]any{"name": "Bash", "input_schema": shell},
				map[string]any{"name": "mcp__web__headers"},
			},
		},
	} {
		request := map[string]any{"model": "mock", "stream": true, "tools": test.tools,
			"messages": []any{user(`use web headers {"names": ["x-aviary-mcp"]}`)},
			"input":    []any{map[string]any{"type": "message", "role": "user", "content": `use web headers {"names": ["x-aviary-mcp"]}`}},
		}
		status, stream, _ := events(t, model.URL+test.api, apiKey, request)
		var namespace, name, arguments string
		for _, event := range stream {
			name += str(at(event, "choices", 0, "delta", "tool_calls", 0, "function", "name")) + str(at(event, "content_block", "name"))
			arguments += str(at(event, "choices", 0, "delta", "tool_calls", 0, "function", "arguments")) + str(at(event, "delta", "partial_json"))
			if event["type"] == "response.output_item.done" {
				namespace, name, arguments = str(at(event, "item", "namespace")), str(at(event, "item", "name")), str(at(event, "item", "arguments"))
			}
		}
		if status != http.StatusOK || namespace != test.namespace || name != test.name || arguments != `{"names": ["x-aviary-mcp"]}` {
			t.Errorf("%s: got %d with %q %q %q, want %q %q", test.api, status, namespace, name, arguments, test.namespace, test.name)
		}
	}

	for said, want := range map[string]string{
		"use web":          "mock: use takes the name of an MCP server and of its tool",
		"use docs headers": "mock: the harness offers no tool headers of the MCP server docs, only bash",
		"use web other":    "mock: the harness offers no tool other of the MCP server web, only bash",
	} {
		answer := chat(t, model.URL, sent{said: []string{said}})
		if answer.text != want {
			t.Errorf("%s: got %+v, want %q", said, answer, want)
		}
	}
}

// The MCP server answers by both transports, to callers with the key, and
// its tool reports the headers of the request that began the session.
func TestMcpServer(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	for _, transport := range []func(string, *http.Client) mcp.Transport{
		func(url string, client *http.Client) mcp.Transport {
			return &mcp.StreamableClientTransport{Endpoint: url + "/mcp", HTTPClient: client}
		},
		func(url string, client *http.Client) mcp.Transport {
			return &mcp.SSEClientTransport{Endpoint: url + "/sse", HTTPClient: client}
		},
	} {
		connect := func(headers map[string]string) (*mcp.ClientSession, error) {
			client := &http.Client{Transport: headed{headers}}
			return mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).
				Connect(t.Context(), transport(model.URL, client), nil)
		}
		if session, err := connect(map[string]string{"Authorization": "Bearer a-session-token"}); err == nil {
			session.Close()
			t.Errorf("%T: connected without the key", transport(model.URL, nil))
		}
		session, err := connect(map[string]string{"Authorization": "Bearer " + apiKey, "X-Aviary-Mcp": "1"})
		if err != nil {
			t.Fatalf("%T: connect: %v", transport(model.URL, nil), err)
		}
		listed, err := session.ListTools(t.Context(), nil)
		if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "headers" {
			t.Errorf("%T: tools = %+v, %v", transport(model.URL, nil), listed, err)
		}
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "headers", Arguments: map[string]any{"names": []string{"x-aviary-mcp", "x-other"}},
		})
		if err != nil || len(result.Content) != 1 {
			t.Fatalf("%T: call: %+v, %v", transport(model.URL, nil), result, err)
		}
		if said := result.Content[0].(*mcp.TextContent).Text; said != "x-aviary-mcp: 1\nx-other: none" {
			t.Errorf("%T: headers said %q", transport(model.URL, nil), said)
		}
		session.Close()
	}
}

// headed is a transport that adds headers to every request.
type headed struct{ headers map[string]string }

func (h headed) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	for name, value := range h.headers {
		request.Header.Set(name, value)
	}
	return http.DefaultTransport.RoundTrip(request)
}

// A harness that offers the tools of MCP servers only after a search, as
// Claude Code does, has the model search for the tool first.
func TestUseAfterASearch(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	use := map[string]any{"role": "user", "content": `use web headers {"names": ["x-aviary-mcp"]}`}
	search := map[string]any{"name": "ToolSearch"}
	// call returns the name and the arguments of the tool that the model calls.
	call := func(messages []any, tools ...any) (string, string) {
		t.Helper()
		status, stream, refusal := events(t, model.URL+"/v1/messages", apiKey, map[string]any{"stream": true, "messages": messages, "tools": tools})
		if status != http.StatusOK {
			t.Fatalf("got %d %s", status, refusal)
		}
		var name, arguments string
		for _, event := range stream {
			name += str(at(event, "content_block", "name"))
			arguments += str(at(event, "delta", "partial_json"))
		}
		return name, arguments
	}
	if name, arguments := call([]any{use}, search); name != "ToolSearch" || arguments != `{"query": "web headers"}` {
		t.Errorf("before the search: called %q %q, want the search", name, arguments)
	}
	searched := []any{use,
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "ToolSearch", "input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "found"}}},
	}
	if name, arguments := call(searched, search, map[string]any{"name": "mcp__web__headers"}); name != "mcp__web__headers" || arguments != `{"names": ["x-aviary-mcp"]}` {
		t.Errorf("after the search: called %q %q, want the tool", name, arguments)
	}
	// A search that did not find the tool is not done again.
	if name, _ := call(searched, search); name != "" {
		t.Errorf("after a search that found nothing: called %q", name)
	}
}

// The locked server refuses every request, and says where its OAuth
// metadata is.
func TestLockedMcpServer(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	for _, path := range []string{"/locked/mcp", "/locked/sse"} {
		request, err := http.NewRequest("POST", model.URL+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+apiKey)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		want := `Bearer resource_metadata="http://` + strings.TrimPrefix(model.URL, "http://") + `/.well-known/oauth-protected-resource` + path + `"`
		if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") != want {
			t.Errorf("%s: got %d with %q", path, response.StatusCode, response.Header.Get("WWW-Authenticate"))
		}
	}
}
