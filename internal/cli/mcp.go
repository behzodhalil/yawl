package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
)

func mcp(ctx context.Context) error {
	c, loginErr := newClient()
	in := bufio.NewReaderSize(stdin, 64<<10)
	enc := json.NewEncoder(stdout)
	for {
		line, long, rerr := readLine(in, mcpLineMax)
		switch {
		case long:
			enc.Encode(rpcError(nil, -32600, fmt.Sprintf("a message over %d bytes", mcpLineMax)))
		case len(bytes.TrimSpace(line)) == 0:
		case bytes.TrimSpace(line)[0] == '[': // MCP 2025-06-18 has no batches
			enc.Encode(rpcError(nil, -32600, "one message a line; batches are not taken"))
		default:
			mcpMessage(ctx, enc, line, c, loginErr)
		}
		if rerr == io.EOF {
			return nil
		} else if rerr != nil {
			return rerr
		}
	}
}

var mcpLineMax = 16 << 20

func readLine(r *bufio.Reader, max int) (line []byte, long bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !long {
			line = append(line, chunk...)
			if len(line) > max+1 {
				line, long = nil, true
			}
		}
		if err != bufio.ErrBufferFull {
			return bytes.TrimRight(line, "\r\n"), long, err
		}
	}
}

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

func mcpMessage(ctx context.Context, enc *json.Encoder, line []byte, c *client, loginErr error) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		enc.Encode(rpcError(nil, -32700, "not JSON"))
		return
	}
	if m.ID == nil || m.Method == "" { // a notification, or a client's response: never answered
		return
	}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(m.Params, &p)
		v := mcpVersions[0]
		if slices.Contains(mcpVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		enc.Encode(rpcResult(m.ID, map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]string{"name": "yawl", "version": "1"}}))
	case "ping":
		enc.Encode(rpcResult(m.ID, map[string]any{}))
	case "tools/list":
		enc.Encode(rpcResult(m.ID, map[string]any{"tools": mcpTools}))
	case "tools/call":
		var p struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			enc.Encode(rpcError(m.ID, -32602, "params are a name and an object of arguments"))
			return
		}
		args := map[string]string{}
		for k, raw := range p.Arguments {
			var v string
			if json.Unmarshal(raw, &v) != nil {
				v = string(raw)
			}
			args[k] = v
		}
		call, ok := mcpCalls[p.Name]
		if !ok {
			enc.Encode(rpcError(m.ID, -32602, "no tool "+p.Name))
			return
		}
		text, err := "", loginErr
		if err == nil {
			text, err = call(ctx, c, args)
		}
		if err != nil {
			enc.Encode(rpcResult(m.ID, toolText(err.Error(), true)))
			return
		}
		enc.Encode(rpcResult(m.ID, toolText(text, false)))
	default:
		enc.Encode(rpcError(m.ID, -32601, "no method "+m.Method))
	}
}

func rpcResult(id json.RawMessage, r any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": r}
}

func rpcError(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func toolText(s string, isErr bool) map[string]any {
	const max = 50_000
	if len(s) > max {
		s = s[:max] + "\n… cut at 50,000 characters"
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": s}}, "isError": isErr}
}

var (
	mcpName   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	mcpID     = regexp.MustCompile(`^[a-z]+_[a-z0-9]+$`)
	mcpCommit = regexp.MustCompile(`^[0-9a-f]{0,40}$`)
)

func schema(required []string, props map[string]string) map[string]any {
	if required == nil {
		required = []string{} // null is not an array: the MCP SDK would drop every tool (review C1)
	}
	p := map[string]any{}
	for k, d := range props {
		p[k] = map[string]string{"type": "string", "description": d}
	}
	return map[string]any{"type": "object", "properties": p, "required": required}
}

var svcArgs = map[string]string{"project": "the project's name", "env": "the environment's name, like production", "service": "the service's name"}

var (
	readOnly = map[string]bool{"readOnlyHint": true}
	writes   = map[string]bool{"readOnlyHint": false}
)

var mcpTools = []map[string]any{
	{"annotations": readOnly, "name": "list_services", "description": "Every project, environment and service, with its state.", "inputSchema": schema(nil, nil)},
	{"annotations": readOnly, "name": "service", "description": "One service: its settings, domains and health.", "inputSchema": schema([]string{"project", "env", "service"}, svcArgs)},
	{"annotations": readOnly, "name": "deploys", "description": "A service's recent deploys.", "inputSchema": schema([]string{"project", "env", "service"}, svcArgs)},
	{"annotations": readOnly, "name": "deploy_log", "description": "One deploy's steps and their output.", "inputSchema": schema([]string{"id"}, map[string]string{"id": "the deploy's id, like dep_abc123"})},
	{"annotations": readOnly, "name": "logs", "description": "A service's recent log lines.", "inputSchema": schema([]string{"project", "env", "service"},
		map[string]string{"project": svcArgs["project"], "env": svcArgs["env"], "service": svcArgs["service"], "lines": "how many, up to 1000", "search": "only lines with this text"})},
	{"annotations": readOnly, "name": "incidents", "description": "Open incidents.", "inputSchema": schema(nil, nil)},
	{"annotations": writes, "name": "deploy", "description": "Deploy a service at its branch's head, or at a commit.", "inputSchema": schema([]string{"project", "env", "service"},
		map[string]string{"project": svcArgs["project"], "env": svcArgs["env"], "service": svcArgs["service"], "commit": "a commit SHA; empty for the branch's head"})},
	{"annotations": writes, "name": "rollback", "description": "Put a service's previous version back.", "inputSchema": schema([]string{"project", "env", "service"}, svcArgs)},
}

func svcPath(a map[string]string) (string, error) {
	for _, k := range []string{"project", "env", "service"} {
		if !mcpName.MatchString(a[k]) {
			return "", fmt.Errorf("%s %q is not a name", k, a[k])
		}
	}
	return fmt.Sprintf("/api/services/%s/%s/%s", a["project"], a["env"], a["service"]), nil
}

func get(ctx context.Context, c *client, path string) (string, error) {
	var raw json.RawMessage
	if err := c.do(ctx, "GET", path, nil, &raw); err != nil {
		return "", err
	}
	return string(raw), nil
}

func post(ctx context.Context, c *client, path string, body any) (string, error) {
	b, _ := json.Marshal(body)
	var raw json.RawMessage
	if err := c.do(ctx, "POST", path, b, &raw); err != nil {
		return "", err
	}
	return string(raw), nil
}

var mcpCalls = map[string]func(context.Context, *client, map[string]string) (string, error){
	"list_services": func(ctx context.Context, c *client, _ map[string]string) (string, error) {
		return get(ctx, c, "/api/projects")
	},
	"incidents": func(ctx context.Context, c *client, _ map[string]string) (string, error) {
		return get(ctx, c, "/api/incidents")
	},
	"service": func(ctx context.Context, c *client, a map[string]string) (string, error) {
		p, err := svcPath(a)
		if err != nil {
			return "", err
		}
		return get(ctx, c, p)
	},
	"deploys": func(ctx context.Context, c *client, a map[string]string) (string, error) {
		p, err := svcPath(a)
		if err != nil {
			return "", err
		}
		return get(ctx, c, p+"/deploys")
	},
	"deploy_log": func(ctx context.Context, c *client, a map[string]string) (string, error) {
		if !mcpID.MatchString(a["id"]) {
			return "", fmt.Errorf("%q is not a deploy id", a["id"])
		}
		return get(ctx, c, "/api/deploys/"+a["id"])
	},
	"logs": func(ctx context.Context, c *client, a map[string]string) (string, error) {
		p, err := svcPath(a)
		if err != nil {
			return "", err
		}
		q := url.Values{}
		if n, err := strconv.Atoi(a["lines"]); err == nil && n > 0 {
			q.Set("n", strconv.Itoa(min(n, 1000)))
		}
		if a["search"] != "" {
			q.Set("q", a["search"])
		}
		return get(ctx, c, p+"/logs?"+q.Encode())
	},
	"deploy": func(ctx context.Context, c *client, a map[string]string) (string, error) {
		p, err := svcPath(a)
		if err != nil {
			return "", err
		}
		if !mcpCommit.MatchString(a["commit"]) {
			return "", fmt.Errorf("%q is not a commit SHA", a["commit"])
		}
		return post(ctx, c, p+"/deploys", map[string]string{"commit": a["commit"]})
	},
	"rollback": func(ctx context.Context, c *client, a map[string]string) (string, error) {
		p, err := svcPath(a)
		if err != nil {
			return "", err
		}
		return post(ctx, c, p+"/rollback", map[string]string{})
	},
}
