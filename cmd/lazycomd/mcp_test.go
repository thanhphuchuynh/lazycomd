package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

// mcpSession feeds lines to the server and returns one decoded response per
// line it answered.
func mcpSession(t *testing.T, lines ...string) []rpcResponse {
	t.Helper()
	var out bytes.Buffer
	if code := serveMCP(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); code != 0 {
		t.Fatalf("serveMCP = %d, want 0", code)
	}
	var resps []rpcResponse
	dec := json.NewDecoder(&out)
	for dec.More() {
		var r rpcResponse
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resps = append(resps, r)
	}
	return resps
}

// toolText pulls the text payload out of a tools/call result.
func toolText(t *testing.T, r rpcResponse) (string, bool) {
	t.Helper()
	blob, err := json.Marshal(r.Result)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(blob, &body); err != nil {
		t.Fatalf("result is not tool content: %v\n%s", err, blob)
	}
	if len(body.Content) != 1 {
		t.Fatalf("content = %+v, want one entry", body.Content)
	}
	return body.Content[0].Text, body.IsError
}

func TestMCPHandshakeAndToolList(t *testing.T) {
	resps := mcpSession(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	// The notification is not answered, so two requests make two responses.
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2: %+v", len(resps), resps)
	}

	blob, _ := json.Marshal(resps[1].Result)
	var list struct{ Tools []toolDef }
	if err := json.Unmarshal(blob, &list); err != nil {
		t.Fatal(err)
	}
	want := []string{"list_commands", "get_logs", "start_command", "stop_command", "restart_command", "who_owns_port", "create_command", "doctor"}
	if len(list.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(list.Tools), len(want))
	}
	for i, name := range want {
		if list.Tools[i].Name != name {
			t.Fatalf("tool %d = %q, want %q", i, list.Tools[i].Name, name)
		}
		if list.Tools[i].Description == "" {
			t.Fatalf("%s has no description, so an agent cannot tell when to use it", name)
		}
	}
}

func TestMCPCallsTools(t *testing.T) {
	testDaemon(t, map[string]config.Command{"api": {Cmd: []string{"sleep", "30"}}})

	resps := mcpSession(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_commands","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"who_owns_port","arguments":{"port":65534}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"doctor","arguments":{}}}`,
	)
	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3", len(resps))
	}

	text, isErr := toolText(t, resps[0])
	if isErr || !strings.Contains(text, `"name": "api"`) {
		t.Fatalf("list_commands = %q (isError=%v)", text, isErr)
	}
	text, isErr = toolText(t, resps[1])
	if isErr || !strings.Contains(text, `"listening": false`) {
		t.Fatalf("who_owns_port = %q (isError=%v)", text, isErr)
	}
	if text, isErr := toolText(t, resps[2]); isErr {
		t.Fatalf("doctor = %q", text)
	}
}

func TestMCPReportsToolFailuresAsContent(t *testing.T) {
	testDaemon(t, map[string]config.Command{"api": {Cmd: []string{"sleep", "30"}}})

	resps := mcpSession(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_logs","arguments":{"name":"ghost"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"who_owns_port","arguments":{"port":0}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
	)
	for i, r := range resps {
		text, isErr := toolText(t, r)
		if !isErr {
			t.Fatalf("response %d should be a tool error, got %q", i, text)
		}
		// A tool failure is content, never a JSON-RPC error: the session
		// stays usable.
		if r.Error != nil {
			t.Fatalf("response %d carried a protocol error: %+v", i, r.Error)
		}
	}
}

func TestMCPUnknownMethodAndBadLine(t *testing.T) {
	resps := mcpSession(t,
		`not json at all`,
		`{"jsonrpc":"2.0","id":1,"method":"nope/nope"}`,
	)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1 (the bad line has no id to answer)", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != -32601 {
		t.Fatalf("error = %+v, want method not found", resps[0].Error)
	}
}

func TestMCPCreateCommandWritesTheConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("# demo\ncommands:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testDaemonWithConfig(t, path)

	resps := mcpSession(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_command","arguments":`+
			`{"name":"api","cmd":["npm","run","dev"],"cwd":"`+dir+`","port":3000,"restart":"on-failure","env":{"LOG":"debug"}}}}`,
		// A spec the daemon rejects comes back as a tool error, not a write.
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_command","arguments":{"name":"bad","cmd":["x"],"health":"ftp://nope"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_command","arguments":{"name":"empty"}}}`,
	)
	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3", len(resps))
	}
	if text, isErr := toolText(t, resps[0]); isErr {
		t.Fatalf("create_command failed: %s", text)
	}
	for _, i := range []int{1, 2} {
		if text, isErr := toolText(t, resps[i]); !isErr {
			t.Fatalf("response %d should be a tool error, got %s", i, text)
		}
	}

	// The command is in the file, with the fields the tool was given.
	file, err := config.ParseFile(path)
	if err != nil {
		t.Fatalf("the written config no longer parses: %v", err)
	}
	got, ok := file.Commands["api"]
	if !ok {
		t.Fatalf("commands = %v", file.Commands)
	}
	if got.Port != 3000 || got.Restart != config.RestartOnFailure || got.Env["LOG"] != "debug" {
		t.Fatalf("spec = %+v", got)
	}
	if _, bad := file.Commands["bad"]; bad {
		t.Fatal("a rejected spec was written anyway")
	}
}
