package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func mcpRun(t *testing.T, lines ...string) ([]map[string]any, []string) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization")+" "+string(b))
		switch {
		case r.URL.Path == "/api/services/acme/production/api/deploys" && r.Method == "POST":
			io.WriteString(w, `{"deploy_id":"dep_9"}`)
		case r.URL.Path == "/api/services/acme/production/api/rollback":
			http.Error(w, "a Viewer cannot roll back", http.StatusForbidden)
		case r.URL.Path == "/api/projects":
			io.WriteString(w, `[{"name":"acme"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YAWL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	stdout, stdin = &bytes.Buffer{}, strings.NewReader("")
	Run(context.Background(), "login", []string{srv.URL, "--token", "tok"})
	calls = nil
	out := &bytes.Buffer{}
	stdout, stdin = out, strings.NewReader(strings.Join(lines, "\n")+"\n")
	if err := Run(context.Background(), "mcp", nil); err != nil {
		t.Fatal(err)
	}
	var answers []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON on stdout: %q", l)
		}
		answers = append(answers, m)
	}
	return answers, calls
}

func result(m map[string]any) map[string]any { r, _ := m["result"].(map[string]any); return r }

func text(m map[string]any) string {
	c, _ := result(m)["content"].([]any)
	if len(c) == 0 {
		return ""
	}
	return c[0].(map[string]any)["text"].(string)
}

func TestMCP(t *testing.T) {
	answers, calls := mcpRun(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"deploy","arguments":{"project":"acme","env":"production","service":"api"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"service","arguments":{"project":"acme","env":"production","service":"../x"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"rollback","arguments":{"project":"acme","env":"production","service":"api"}}}`,
		`not json`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"reveal","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"list_services","arguments":{}}}`,
	)
	if len(answers) != 8 {
		t.Fatalf("%d answers: %v", len(answers), answers)
	}
	init := result(answers[0])
	if init["protocolVersion"] != "2025-06-18" || init["capabilities"].(map[string]any)["tools"] == nil || init["serverInfo"].(map[string]any)["name"] != "yawl" {
		t.Fatalf("initialize %v", init)
	}
	var names []string
	for _, tl := range result(answers[1])["tools"].([]any) {
		m := tl.(map[string]any)
		if req, ok := m["inputSchema"].(map[string]any)["required"].([]any); !ok || req == nil { // review C1: null makes the SDK drop every tool
			t.Fatalf("required is not an array: %v", m)
		}
		names = append(names, m["name"].(string))
	}
	if strings.Join(names, ",") != "list_services,service,deploys,deploy_log,logs,incidents,deploy,rollback" {
		t.Fatalf("tools %v", names)
	}
	if !strings.Contains(text(answers[2]), "dep_9") || result(answers[2])["isError"] == true {
		t.Fatalf("deploy %v", answers[2])
	}
	if result(answers[3])["isError"] != true {
		t.Fatalf("a path in an argument: %v", answers[3])
	}
	if result(answers[4])["isError"] != true || !strings.Contains(text(answers[4]), "a Viewer cannot roll back") {
		t.Fatalf("403: %v", answers[4])
	}
	if e, _ := answers[5]["error"].(map[string]any); e == nil || e["code"].(float64) != -32700 || answers[5]["id"] != nil {
		t.Fatalf("parse error %v", answers[5])
	}
	if e, _ := answers[6]["error"].(map[string]any); e == nil {
		t.Fatalf("unknown tool %v", answers[6])
	}
	if text(answers[7]) != `[{"name":"acme"}]` {
		t.Fatalf("list %v", answers[7])
	}
	all := strings.Join(calls, "\n")
	if !strings.Contains(all, `POST /api/services/acme/production/api/deploys Bearer tok {"commit":""}`) || strings.Contains(all, "../x") || strings.Contains(all, "reveal") {
		t.Fatalf("calls:\n%s", all)
	}
}

func TestMCPMinors(t *testing.T) {
	old := mcpLineMax
	mcpLineMax = 1 << 10
	defer func() { mcpLineMax = old }()
	answers, calls := mcpRun(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"logs","arguments":{"project":"acme","env":"production","service":"api","lines":20}}}`,
		`[{"jsonrpc":"2.0","id":3,"method":"ping"}]`,
		`{"jsonrpc":"2.0","id":4,"result":{}}`,
		`{"jsonrpc":"2.0","id":5,"method":"ping","params":{"pad":"`+strings.Repeat("x", 2<<10)+`"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`,
	)
	if len(answers) != 6 {
		t.Fatalf("%d answers: %v", len(answers), answers)
	}
	if v := result(answers[0])["protocolVersion"]; v != "2025-03-26" {
		t.Fatalf("asked 2025-03-26, got %v", v)
	}
	if answers[1]["error"] != nil || !strings.Contains(strings.Join(calls, "\n"), "/logs?n=20") {
		t.Fatalf("a number: %v %v", answers[1], calls)
	}
	if e, _ := answers[2]["error"].(map[string]any); e == nil || e["code"].(float64) != -32600 {
		t.Fatalf("a batch: %v", answers[2])
	}
	if e, _ := answers[3]["error"].(map[string]any); e == nil || e["code"].(float64) != -32600 {
		t.Fatalf("a long line: %v", answers[3])
	}
	if v := result(answers[4])["protocolVersion"]; v != "2025-06-18" {
		t.Fatalf("an unknown version: %v", v)
	}
	for _, tl := range result(answers[5])["tools"].([]any) {
		m := tl.(map[string]any)
		ro, _ := m["annotations"].(map[string]any)["readOnlyHint"].(bool)
		if write := m["name"] == "deploy" || m["name"] == "rollback"; ro == write {
			t.Fatalf("%v", m)
		}
	}
}
