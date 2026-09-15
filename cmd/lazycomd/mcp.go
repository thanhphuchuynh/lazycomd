package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/client"
	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

// protocolVersion is what this server speaks when a client does not name a
// version of its own.
const protocolVersion = "2024-11-05"

// rpcRequest is one JSON-RPC message. A notification has no id, and gets no
// response.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolDef is one tool as tools/list reports it.
type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// runMCP serves the Model Context Protocol over stdin/stdout, so an agent can
// drive the daemon as tools rather than by shelling out and parsing prose.
//
// The transport is line-delimited JSON-RPC, which is small enough to speak
// directly: a dependency for four methods would be more code than this.
func runMCP(args []string) int {
	if len(args) > 0 {
		return usageErr("mcp")
	}
	return serveMCP(os.Stdin, os.Stdout)
}

func serveMCP(in io.Reader, out io.Writer) int {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), maxMCPLine)
	enc := json.NewEncoder(out)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			// A malformed line has no id to answer, so there is nobody to
			// tell. Log it and keep the session alive.
			fmt.Fprintf(os.Stderr, "lazycomd mcp: %v\n", err)
			continue
		}
		resp, send := handleMCP(req)
		if !send {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fail(err)
		}
	}
	if err := sc.Err(); err != nil {
		return fail(err)
	}
	return 0
}

// maxMCPLine caps one message. Log output is the only thing that gets near it.
const maxMCPLine = 4 << 20

func handleMCP(req rpcRequest) (rpcResponse, bool) {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "lazycomd", "version": version},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		resp.Result = callTool(req.Params)
	case "ping":
		resp.Result = map[string]any{}
	default:
		// A notification — no id — is never answered, not even to complain.
		if len(req.ID) == 0 {
			return resp, false
		}
		resp.Error = &rpcError{Code: -32601, Message: "unknown method: " + req.Method}
	}
	if len(req.ID) == 0 {
		return resp, false
	}
	return resp, true
}

// schema is a tiny JSON Schema builder: every tool here takes flat arguments.
func schema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func str(desc string) map[string]any   { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any   { return map[string]any{"type": "number", "description": desc} }
func flag_(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

func mcpTools() []toolDef {
	return []toolDef{
		{
			Name:        "list_commands",
			Description: "Every command lazycomd knows, with its state, PID, uptime, restart count and health. Start here to see what exists and what is running.",
			InputSchema: schema(map[string]any{}),
		},
		{
			Name:        "get_logs",
			Description: "The tail of one command's output. Use it after a command fails or restarts to find out why.",
			InputSchema: schema(map[string]any{
				"name": str("command name, e.g. app:api"),
				"tail": num("how many lines (default 100)"),
			}, "name"),
		},
		{
			Name:        "start_command",
			Description: "Start a command. With wait_sec it blocks until the command is ready — its health URL answers or its port accepts — so the next step can assume the service is up.",
			InputSchema: schema(map[string]any{
				"name":      str("command name"),
				"with_deps": flag_("start its dependencies first (default true)"),
				"wait_sec":  num("block up to this many seconds for readiness (default 0, no wait)"),
			}, "name"),
		},
		{
			Name:        "stop_command",
			Description: "Stop a running command.",
			InputSchema: schema(map[string]any{"name": str("command name")}, "name"),
		},
		{
			Name:        "restart_command",
			Description: "Restart a command, for example after changing a config file it reads at boot.",
			InputSchema: schema(map[string]any{"name": str("command name")}, "name"),
		},
		{
			Name:        "who_owns_port",
			Description: "Which process is listening on a port, and whether it is one of lazycomd's commands. Use it when something reports a port already in use.",
			InputSchema: schema(map[string]any{"port": num("TCP port, 1-65535")}, "port"),
		},
		{
			Name:        "create_command",
			Description: "Register a new command in the config, so it survives restarts and shows up in the TUI. A namespaced name (app:api) writes into that project's lazycomd.yaml; a bare name writes into the global config. Use it after scaffolding a service.",
			InputSchema: schema(map[string]any{
				"name":       str("command name; app:api puts it in the app project"),
				"cmd":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": `argv, e.g. ["npm","run","dev"]`},
				"cwd":        str("folder it runs in (default: the daemon's)"),
				"env":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "environment variables"},
				"shell":      flag_("run cmd[0] through sh -c instead of exec (needed for pipes, globs and $VARS)"),
				"restart":    str("no, on-failure or always (default no)"),
				"autostart":  flag_("start it when the daemon starts"),
				"port":       num("the port it binds, so lazycomd can tell who owns it"),
				"health":     str("http URL polled for readiness, e.g. http://localhost:8080/healthz"),
				"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "commands to start first"},
			}, "name", "cmd"),
		},
		{
			Name:        "doctor",
			Description: "Check the catalog for what will fail to start: two commands claiming one port, a depends_on naming nothing, a cwd that is gone. Run it before a long task.",
			InputSchema: schema(map[string]any{}),
		},
	}
}

// toolArgs is every argument any tool takes; each tool reads the ones it owns.
type toolArgs struct {
	Name     string  `json:"name"`
	Tail     int     `json:"tail"`
	WithDeps *bool   `json:"with_deps"`
	WaitSec  float64 `json:"wait_sec"`
	Port     int     `json:"port"`

	// create_command's spec. Port doubles as the port field, since no tool
	// needs both meanings at once.
	Cmd       []string          `json:"cmd"`
	Cwd       string            `json:"cwd"`
	Env       map[string]string `json:"env"`
	Shell     bool              `json:"shell"`
	Restart   string            `json:"restart"`
	Autostart bool              `json:"autostart"`
	Health    string            `json:"health"`
	DependsOn []string          `json:"depends_on"`
}

type toolCall struct {
	Name      string   `json:"name"`
	Arguments toolArgs `json:"arguments"`
}

// callTool runs one tool and returns MCP content. A failure is reported as
// isError content, not as a JSON-RPC error: the agent should read it and
// decide, not treat the session as broken.
func callTool(params json.RawMessage) map[string]any {
	var call toolCall
	if err := json.Unmarshal(params, &call); err != nil {
		return toolError(err)
	}
	result, err := dispatchTool(call)
	if err != nil {
		return toolError(err)
	}
	blob, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return toolError(err)
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(blob)}}}
}

func toolError(err error) map[string]any {
	return map[string]any{
		"isError": true,
		"content": []map[string]any{{"type": "text", "text": err.Error()}},
	}
}

func dispatchTool(call toolCall) (any, error) {
	c, err := client.Default()
	if err != nil {
		return nil, err
	}
	a := call.Arguments

	// Every tool but the two that take no name works on one command, and a
	// bare name resolves the same way it does on the command line.
	resolve := func() (string, error) { return c.Resolve(a.Name) }

	switch call.Name {
	case "list_commands":
		return c.List()

	case "get_logs":
		name, err := resolve()
		if err != nil {
			return nil, err
		}
		tail := a.Tail
		if tail <= 0 {
			tail = 100
		}
		lines, err := c.Logs(name, tail)
		if err != nil {
			return nil, err
		}
		if lines == nil {
			lines = []string{}
		}
		return map[string]any{"name": name, "lines": lines}, nil

	case "start_command":
		name, err := resolve()
		if err != nil {
			return nil, err
		}
		deps := true
		if a.WithDeps != nil {
			deps = *a.WithDeps
		}
		return c.StartWait(name, deps, time.Duration(a.WaitSec*float64(time.Second)))

	case "stop_command":
		name, err := resolve()
		if err != nil {
			return nil, err
		}
		return c.Stop(name)

	case "restart_command":
		name, err := resolve()
		if err != nil {
			return nil, err
		}
		return c.Restart(name)

	case "who_owns_port":
		if a.Port < 1 || a.Port > 65535 {
			return nil, fmt.Errorf("port %d is out of range 1-65535", a.Port)
		}
		snap, err := c.System()
		if err != nil {
			return nil, err
		}
		ports := filterPort(snap.Ports, a.Port)
		sortPorts(ports)
		if len(ports) == 0 {
			return map[string]any{"port": a.Port, "listening": false}, nil
		}
		return map[string]any{"port": a.Port, "listening": true, "owners": ports}, nil

	case "create_command":
		if strings.TrimSpace(a.Name) == "" {
			return nil, errors.New("name is required")
		}
		if len(a.Cmd) == 0 {
			return nil, errors.New("cmd is required, as an array of arguments")
		}
		// The daemon validates the spec and picks the file the name belongs
		// in — the global config, or a project's lazycomd.yaml.
		return c.Create(a.Name, config.Command{
			Cmd:       a.Cmd,
			Cwd:       a.Cwd,
			Env:       a.Env,
			Shell:     a.Shell,
			Restart:   config.Restart(a.Restart),
			Autostart: a.Autostart,
			Port:      a.Port,
			Health:    a.Health,
			DependsOn: a.DependsOn,
		})

	case "doctor":
		return runDoctorFindings(c)

	default:
		return nil, fmt.Errorf("unknown tool: %s", call.Name)
	}
}
