// Command mock-llm stands in for a model server in tests. It speaks the two
// protocols that the harnesses use, the Chat Completions API and the
// Responses API of OpenAI, both as streams. Instead of a model, a script
// answers: the last message of the user is an instruction.
//
//	say <text>     replies with the text
//	run <command>  runs the command with the harness's shell tool, then
//	               replies with what the tool returned
//	recall         replies with the instructions that the user gave before,
//	               one per line, or "nothing" if there are none
//
// A message that is not an instruction gets the reply "mock".
package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /v1/chat/completions", chatCompletions)
	mux.HandleFunc("POST /v1/responses", responses)
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	slog.Info("listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil {
		slog.Error("mock-llm stopped", "error", err)
		os.Exit(1)
	}
}

// conversation is what a harness sends, in the terms of the script.
type conversation struct {
	// said holds the messages of the user, oldest first.
	said []string
	// toolOutput is what the tool returned, if the conversation ends with it.
	toolOutput *string
	// shell is the harness's tool that runs a command, if it offers one.
	shell *shellTool
}

type shellTool struct {
	name string
	// argument is the name of the parameter that takes the command.
	argument string
}

// answer is what the model does next: it calls the shell tool with command,
// or else it says text.
type answer struct {
	text    string
	command string
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
	case word == "run" && c.shell == nil:
		return answer{text: "mock: the harness offers no tool that runs a command"}
	case word == "run":
		return answer{command: rest}
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
	case "say", "run", "recall":
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
				return &shellTool{name: tool.Name, argument: argument}
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

func refuse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": message, "type": "invalid_request_error"},
	})
}

// chatCompletions serves the Chat Completions API, which OpenCode uses.
func chatCompletions(w http.ResponseWriter, r *http.Request) {
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
		refuse(w, "the request is not JSON: "+err.Error())
		return
	}
	if !request.Stream {
		refuse(w, "mock-llm only streams; set stream to true")
		return
	}
	var c conversation
	for i, message := range request.Messages {
		switch message.Role {
		case "user":
			c.said = append(c.said, text(message.Content))
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
	next := c.next()
	slog.Info("answered", "api", "chat completions", "said", len(c.said), "command", next.command, "text", next.text)

	id, created := newID("chatcmpl-"), time.Now().Unix()
	out := newStream(w)
	chunk := func(delta map[string]any, finish any) {
		data := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": request.Model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		if finish != nil {
			data["usage"] = map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
		}
		out.send("", data)
	}
	chunk(map[string]any{"role": "assistant", "content": ""}, nil)
	if next.command == "" {
		for _, piece := range pieces(next.text) {
			chunk(map[string]any{"content": piece}, nil)
		}
		chunk(map[string]any{}, "stop")
	} else {
		arguments, _ := json.Marshal(map[string]string{c.shell.argument: next.command})
		call := func(function map[string]any) map[string]any {
			return map[string]any{"tool_calls": []map[string]any{function}}
		}
		chunk(call(map[string]any{
			"index": 0, "id": newID("call_"), "type": "function",
			"function": map[string]string{"name": c.shell.name, "arguments": ""},
		}), nil)
		chunk(call(map[string]any{
			"index": 0, "function": map[string]string{"arguments": string(arguments)},
		}), nil)
		chunk(map[string]any{}, "tool_calls")
	}
	out.send("", "[DONE]")
}

// responses serves the Responses API, which Codex uses.
func responses(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model  string          `json:"model"`
		Stream bool            `json:"stream"`
		Input  json.RawMessage `json:"input"`
		Tools  []tool          `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		refuse(w, "the request is not JSON: "+err.Error())
		return
	}
	if !request.Stream {
		refuse(w, "mock-llm only streams; set stream to true")
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
	var c conversation
	if json.Unmarshal(request.Input, &items) != nil {
		c.said = []string{text(request.Input)}
	}
	for i, item := range items {
		switch {
		case item.Type == "message" && item.Role == "user":
			c.said = append(c.said, text(item.Content))
		case item.Type == "function_call_output" && i == len(items)-1:
			output := text(item.Output)
			c.toolOutput = &output
		}
	}
	c.shell = findShell(request.Tools)
	next := c.next()
	slog.Info("answered", "api", "responses", "said", len(c.said), "command", next.command, "text", next.text)

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
		}
		send("response.output_text.done", at(map[string]any{"text": next.text}))
		send("response.content_part.done", at(map[string]any{"part": part(next.text)}))
		item = map[string]any{"id": id, "type": "message", "status": "completed", "role": "assistant", "content": []any{part(next.text)}}
	} else {
		id, callID := newID("fc_"), newID("call_")
		arguments, _ := json.Marshal(map[string]string{c.shell.argument: next.command})
		call := func(status, arguments string) map[string]any {
			return map[string]any{
				"id": id, "type": "function_call", "status": status,
				"call_id": callID, "name": c.shell.name, "arguments": arguments,
			}
		}
		item = call("completed", string(arguments))
		send("response.output_item.added", map[string]any{"output_index": 0, "item": call("in_progress", "")})
		send("response.function_call_arguments.delta", map[string]any{"item_id": id, "output_index": 0, "delta": string(arguments)})
		send("response.function_call_arguments.done", map[string]any{"item_id": id, "output_index": 0, "name": c.shell.name, "arguments": string(arguments)})
	}
	send("response.output_item.done", map[string]any{"output_index": 0, "item": item})
	response["status"] = "completed"
	response["output"] = []any{item}
	response["usage"] = map[string]any{
		"input_tokens": 0, "input_tokens_details": map[string]int{"cached_tokens": 0},
		"output_tokens": 0, "output_tokens_details": map[string]int{"reasoning_tokens": 0},
		"total_tokens": 0,
	}
	send("response.completed", map[string]any{"response": response})
}
