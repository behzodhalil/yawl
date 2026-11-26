package api

import (
	"encoding/json"
	"strings"
	"testing"
)

type auditRow struct {
	At        int64  `json:"at"`
	Actor     string `json:"actor"`
	Text      string `json:"text"`
	Kind      string `json:"kind"`
	Container string `json:"container"`
}

func auditRows(t *testing.T, b []byte) []auditRow {
	var v struct{ Rows []auditRow }
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return v.Rows
}

func audits(t *testing.T) []string {
	rows, err := testDB.Query(`SELECT actor, body_json FROM events WHERE kind = 'audit' ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var actor, body string
		rows.Scan(&actor, &body)
		var b struct{ Text string }
		json.Unmarshal([]byte(body), &b)
		out = append(out, actor+": "+b.Text)
	}
	return out
}

func TestAuditRecordsChange(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	if code, b := put(t, srv, tok, "/api/services/acme/production/api/env/API_KEY", sealed()); code != 200 && code != 204 {
		t.Fatalf("%d %s", code, b)
	}
	got := audits(t)
	if len(got) == 0 || got[len(got)-1] != "API token test: set variable API_KEY on acme/production/api" {
		t.Fatalf("%q", got)
	}
}

func TestAuditNeverBodies(t *testing.T) {
	srv, tok := ready(t, true)
	setAgentKey(t)
	put(t, srv, tok, "/api/services/acme/production/api/env/API_KEY", `{"agent_box":"`+box+`","recovery_box":"`+box+`","note":"PLANTED-hunter2"}`)
	var n int
	testDB.QueryRow(`SELECT count(*) FROM events WHERE body_json LIKE '%PLANTED%' OR body_json LIKE ?`, "%"+box+"%").Scan(&n)
	if n != 0 {
		t.Fatalf("%d events carry the body", n)
	}
	_, csv := call(t, srv, tok, "GET", "/api/audit.csv", "")
	if strings.Contains(string(csv), "PLANTED") || strings.Contains(string(csv), box) {
		t.Fatal("the CSV carries the body")
	}
}

func TestAuditSkipsFailuresAndReads(t *testing.T) {
	srv, tok := ready(t, true)
	before := len(audits(t))
	call(t, srv, tok, "PUT", "/api/services/acme/production/api/env/BAD", `{"value":"plain"}`)
	call(t, srv, tok, "GET", "/api/services/acme/production/api", "")
	call(t, srv, tok, "POST", "/api/plan", yaml)
	if got := audits(t); len(got) != before {
		t.Fatalf("%q", got[before:])
	}
}

func TestAuditFallback(t *testing.T) {
	srv, tok := ready(t, true)
	call(t, srv, tok, "POST", "/api/join-token", "")
	got := audits(t)
	if got[len(got)-1] != "API token test: POST /api/join-token" {
		t.Fatalf("%q", got)
	}
}

func TestAuditFilterAndCSV(t *testing.T) {
	srv, tok := ready(t, true)
	call(t, srv, tok, "POST", "/api/tokens", `{"name":"ci"}`)
	_, b := call(t, srv, tok, "GET", "/api/audit?q=token", "")
	rows := auditRows(t, b)
	if len(rows) != 1 || rows[0].Text != "created API token ci" || rows[0].Actor != "API token test" {
		t.Fatalf("%+v", rows)
	}
	if _, b := call(t, srv, tok, "GET", "/api/audit?actor=nobody", ""); len(auditRows(t, b)) != 0 {
		t.Fatalf("%s", b)
	}
	_, csv := call(t, srv, tok, "GET", "/api/audit.csv?q=token", "")
	lines := strings.Split(strings.TrimSpace(string(csv)), "\n")
	if lines[0] != "at,actor,text" || len(lines) != 2 || !strings.HasSuffix(lines[1], ",API token test,created API token ci") {
		t.Fatalf("%q", lines)
	}
}

func TestAuditOnlyTokens(t *testing.T) {
	srv, tok := ready(t, true)
	testDB.Exec(`INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES
		('evt_t1','audit','x','API token laptop','{"text":"deployed"}',1,1),
		('evt_t2','audit','x','owner@acme.uz','{"text":"applied"}',2,2),
		('evt_t3','audit','x','API tokens fan','{"text":"near miss"}',3,3)`)
	_, b := call(t, srv, tok, "GET", "/api/audit?from=0&tokens=1", "")
	for _, r := range auditRows(t, b) {
		if !strings.HasPrefix(r.Actor, "API token ") {
			t.Fatalf("%+v", r)
		}
	}
	if rows := auditRows(t, b); len(rows) == 0 || rows[len(rows)-1].Actor != "API token laptop" {
		t.Fatalf("%s", b)
	}
	if _, b := call(t, srv, tok, "GET", "/api/audit?from=0", ""); !strings.Contains(string(b), "owner@acme.uz") {
		t.Fatalf("%s", b)
	}
}

func TestAuditCSVNoFormulas(t *testing.T) {
	srv, tok := ready(t, true)
	testDB.Exec(`INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES ('evt_f','audit','x','@evil','{"text":"=HYPERLINK(\"x\")"}',1,1)`)
	_, csv := call(t, srv, tok, "GET", "/api/audit.csv?from=0", "")
	if !strings.Contains(string(csv), `,'@evil,"'=HYPERLINK(""x"")"`) {
		t.Fatalf("%s", csv)
	}
}

func TestAuditSearchIsLiteral(t *testing.T) {
	srv, tok := ready(t, true)
	call(t, srv, tok, "POST", "/api/tokens", `{"name":"a_b"}`)
	call(t, srv, tok, "POST", "/api/tokens", `{"name":"axb"}`)
	_, b := call(t, srv, tok, "GET", "/api/audit?q=a_b", "")
	if rows := auditRows(t, b); len(rows) != 1 || rows[0].Text != "created API token a_b" {
		t.Fatalf("%+v", rows)
	}
	if _, b := call(t, srv, tok, "GET", "/api/audit?q=%25", ""); len(auditRows(t, b)) != 0 {
		t.Fatalf("%s", b)
	}
}

func TestAuditFindsAServicesTerminalSessions(t *testing.T) {
	srv, tok := ready(t, true)
	testDB.Exec(`INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES ('evt_t1','terminal.opened','svc_a','dilnoza','{"container":"yawl-web-1","service":"svc_a"}',5,5)`)
	testDB.Exec(`INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES ('evt_t2','terminal.opened','svc_b','dilnoza','{"container":"yawl-api-1","service":"svc_b"}',6,6)`)
	_, b := call(t, srv, tok, "GET", `/api/audit?from=0&q=%22service%22:%22svc_a%22`, "")
	rows := auditRows(t, b)
	if len(rows) != 1 || rows[0].Kind != "terminal.opened" || rows[0].Container != "yawl-web-1" || rows[0].Actor != "dilnoza" {
		t.Fatalf("%+v", rows)
	}
}
