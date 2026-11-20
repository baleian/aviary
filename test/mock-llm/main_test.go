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
)

const apiKey = "the-key"

// sent is what a harness sends the model: what it tells the model of itself,
// the messages of the user so far and, if the model called the tool last,
// what the tool returned.
type sent struct {
	system     string
	said       []string
	toolOutput string
	// noTool is set for a harness that offers the model no tool.
	noTool bool
	// sandboxed is set for a harness whose tool keeps a command in a sandbox
	// unless the model asks otherwise, as the tool of Codex does.
	sandboxed bool
}

// parameters returns the parameters of the tool of the harness, of which
// command is the one that takes the command.
func (s sent) parameters(command string) map[string]any {
	properties := map[string]any{command: map[string]any{}}
	if s.sandboxed {
		properties["sandbox_permissions"], properties["justification"] = map[string]any{}, map[string]any{}
	}
	return map[string]any{"properties": properties}
}

// reply is what the model answered with: a text, or the call of a tool with
// a command, or a refusal.
type reply struct {
	status  int
	refusal string
	text    string
	tool    string
	command string
	// others holds what else the call asks of the tool.
	others map[string]string
	usage  map[string]float64
}

// events sends a request to the model and returns the status of the answer
// and the events of its stream, or the refusal.
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
			t.Fatalf("the refusal is %s: %v", answer, err)
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
			t.Fatalf("the stream has the line %q: %v", lines.Text(), err)
		}
		stream = append(stream, event)
	}
	return response.StatusCode, stream, ""
}

// at returns what is at the path in the JSON value: the names of fields and
// the indexes of list items.
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

// called takes in the arguments of a tool call: the command, whichever name
// the tool gives it, and the others.
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
				"input": usage["prompt_tokens"].(float64), "output": usage["completion_tokens"].(float64),
				"total": usage["total_tokens"].(float64),
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
			usage := at(event, "response", "usage").(map[string]any)
			answer.usage = map[string]float64{
				"input": usage["input_tokens"].(float64), "output": usage["output_tokens"].(float64),
				"total": usage["total_tokens"].(float64),
			}
		}
	}
	return answer
}

// The script is the same for the harnesses that speak either API.
func TestScript(t *testing.T) {
	for api, ask := range map[string]func(*testing.T, string, sent) reply{"chat completions": chat, "responses": respond} {
		t.Run(api, func(t *testing.T) {
			t.Parallel()
			model := httptest.NewServer(newMock(apiKey))
			defer model.Close()
			for _, test := range []struct {
				name string
				sent sent
				// The answer is the text, or the call of the tool with the
				// command, which may ask to run it outside the sandbox.
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
				{name: "what the tool returned", sent: sent{said: []string{"run ls -a"}, toolOutput: ". .. notes.txt"}, text: ". .. notes.txt"},
				// Only a tool that keeps commands in a sandbox is asked to
				// run one outside it, and only when the script says so.
				{name: "escalate", sent: sent{said: []string{"escalate ls -a"}, sandboxed: true}, command: "ls -a", outside: true},
				{name: "escalate without a sandbox", sent: sent{said: []string{"escalate ls -a"}}, command: "ls -a"},
				{name: "run in a sandbox", sent: sent{said: []string{"run ls -a"}, sandboxed: true}, command: "ls -a"},
				{
					name: "escalate without a tool", sent: sent{said: []string{"escalate ls -a"}, noTool: true},
					text: "mock: the harness offers no tool that runs a command",
				},
				{
					name: "what the tool returned outside the sandbox",
					sent: sent{said: []string{"escalate ls -a"}, toolOutput: ". .. notes.txt", sandboxed: true}, text: ". .. notes.txt",
				},
				{name: "recall at first", sent: sent{said: []string{"recall"}}, text: "nothing"},
				{
					// What a harness adds to the conversation is no message
					// of the user.
					name: "recall", sent: sent{said: []string{"say one", "<context>today</context>", "run ls", "escalate ls", "recall"}},
					text: "say one\nrun ls\nescalate ls",
				},
				{
					name: "find what the harness tells the model",
					sent: sent{system: "The word of the day is plum.", said: []string{"find plum"}}, text: "present",
				},
				{
					name: "find what the harness adds to the conversation",
					sent: sent{said: []string{"<context>plum</context>", "find plum"}}, text: "present",
				},
				{
					name: "find what was not sent",
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
					t.Errorf("%s: the model answered with %+v", test.name, answer)
				}
				// Every answer reports the same usage.
				if answer.usage["input"] != 11 || answer.usage["output"] != 7 || answer.usage["total"] != 18 {
					t.Errorf("%s: the model reported the usage %v", test.name, answer.usage)
				}
			}

			refused := ask(t, model.URL, sent{said: []string{"fail the model is out of order"}})
			if refused.status != http.StatusUnauthorized || refused.refusal != "the model is out of order" {
				t.Errorf("fail: the model answered with %+v", refused)
			}

			// A request is refused once, and answered when it comes again.
			busy := sent{said: []string{"flaky later " + api}}
			if first := ask(t, model.URL, busy); first.status != http.StatusServiceUnavailable {
				t.Errorf("flaky: the model answered at first with %+v", first)
			}
			for range 2 {
				if again := ask(t, model.URL, busy); again.status != http.StatusOK || again.text != "later "+api {
					t.Errorf("flaky: the model then answered with %+v", again)
				}
			}
		})
	}
}

func TestDrawl(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	began := time.Now()
	answer := chat(t, model.URL, sent{said: []string{"drawl one two"}})
	if answer.text != "one two" || time.Since(began) < 2*drawlPause {
		t.Errorf("the model answered with %q after %s", answer.text, time.Since(began))
	}
}

func TestRequests(t *testing.T) {
	t.Parallel()
	model := httptest.NewServer(newMock(apiKey))
	defer model.Close()
	messages := []map[string]any{{"role": "user", "content": "say hello"}}
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		// The model answers the Egress Proxy, which holds its key, and not a
		// sandbox, which holds the token of its session.
		for _, key := range []string{"", "a-session-token"} {
			status, _, _ := events(t, model.URL+path, key, map[string]any{"stream": true, "messages": messages})
			if status != http.StatusUnauthorized {
				t.Errorf("%s with the key %q was answered with %d", path, key, status)
			}
		}
		if status, _, _ := events(t, model.URL+path, apiKey, map[string]any{"messages": messages}); status != http.StatusBadRequest {
			t.Errorf("%s without a stream was answered with %d", path, status)
		}
	}
	if response, err := http.Get(model.URL + "/healthz"); err != nil || response.StatusCode != http.StatusOK {
		t.Errorf("the model answered whether it is up with %v: %v", response, err)
	}
}

// As a forward proxy mock-llm lets nothing through.
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
		t.Errorf("a request through the proxy was answered with %v: %v", response, err)
	}
	// A request over TLS asks the proxy for a tunnel first.
	if response, err := through.Get("https://example.com/"); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("a tunnel through the proxy was answered with %v: %v", response, err)
	}
}

// mock-llm serves the wheel of a Python package, without a key.
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
		t.Fatalf("the package was answered with %d: %v", response.StatusCode, err)
	}
	archive, err := zip.NewReader(bytes.NewReader(served), int64(len(served)))
	if err != nil {
		t.Fatalf("the package is no wheel: %v", err)
	}
	var names []string
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	if !slices.Equal(names, []string{"aviary_probe/__init__.py", "aviary_probe-1.0.dist-info/METADATA",
		"aviary_probe-1.0.dist-info/WHEEL", "aviary_probe-1.0.dist-info/RECORD"}) {
		t.Errorf("the wheel holds %v", names)
	}
}
