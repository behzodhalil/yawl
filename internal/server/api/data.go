package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const (
	maxSQL   = 16 << 10
	pageRows = 100

	agentQueryLimit         = 35 * time.Second
	waitPastAgentQueryLimit = agentQueryLimit + 25*time.Second
)

var browsable = map[string]string{"postgres": "Postgres", "mysql": "MySQL", "mariadb": "MariaDB"}

func init() {
	for _, p := range []string{"tables", "rows", "query", "write"} {
		reads["POST /api/services/{project}/{env}/{name}/data/"+p] = true
	}
}

func (h *handler) routesData(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/data/tables", h.dataTables)
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/data/rows", h.dataRows)
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/data/query", h.dataQuery)
	mux.HandleFunc("POST /api/services/{project}/{env}/{name}/data/write", h.dataQuery)
}

func (h *handler) dataTables(w http.ResponseWriter, r *http.Request) {
	q := `SELECT table_schema, table_name FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog', 'information_schema') ` +
		`ORDER BY table_schema <> 'public', table_schema, table_name`
	if k := h.kindOf(r); k == "mysql" || k == "mariadb" {
		q = `SELECT '' AS table_schema, table_name FROM information_schema.tables WHERE table_schema = DATABASE() ORDER BY table_name`
	}
	data, ok := h.runSQL(w, r, q, false, "")
	if !ok {
		return
	}
	type table struct {
		Schema string `json:"schema"`
		Name   string `json:"name"`
	}
	out := []table{}
	for _, row := range data.Rows {
		if len(row) == 2 && row[0] != nil && row[1] != nil {
			out = append(out, table{*row[0], *row[1]})
		}
	}
	writeJSON(w, map[string]any{"tables": out})
}

func (h *handler) dataRows(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Schema, Table string
		Page          int
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil || in.Table == "" || in.Page < 0 || in.Page > 1_000_000 {
		http.Error(w, `give {"schema", "table", "page"}: a table and a page from 0`, http.StatusBadRequest)
		return
	}
	quote, name := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }, in.Table
	if k := h.kindOf(r); k == "mysql" || k == "mariadb" {
		quote = func(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
	}
	from := quote(in.Table)
	if in.Schema != "" {
		from, name = quote(in.Schema)+"."+from, in.Schema+"."+in.Table
	}
	q := fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d", from, pageRows+1, in.Page*pageRows)
	data, ok := h.runSQL(w, r, q, false, "read table "+name+" of "+where(r))
	if !ok {
		return
	}
	if len(data.Rows) > pageRows {
		data.Rows, data.More = data.Rows[:pageRows], true
	}
	writeJSON(w, data)
}

func (h *handler) dataQuery(w http.ResponseWriter, r *http.Request) {
	var in struct{ SQL string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxSQL)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "bad JSON, or SQL past 16 KB", http.StatusBadRequest)
		return
	}
	switch {
	case strings.TrimSpace(in.SQL) == "":
		http.Error(w, "Write some SQL to run.", http.StatusBadRequest)
		return
	case len(in.SQL) > maxSQL:
		http.Error(w, "SQL here is at most 16 KB; load a bigger script as an import.", http.StatusBadRequest)
		return
	}
	write := strings.HasSuffix(r.Pattern, "/write")
	text := "ran SQL on " + where(r) + ": " + in.SQL
	if write {
		text = "ran SQL with writes on " + where(r) + ": " + in.SQL
	}
	if data, ok := h.runSQLRaw(w, r, in.SQL, write, text); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

func where(r *http.Request) string {
	return r.PathValue("project") + "/" + r.PathValue("env") + "/" + r.PathValue("name")
}

func (h *handler) kindOf(r *http.Request) string {
	svc, err := h.a.St.Q.GetServiceByName(r.Context(), db.GetServiceByNameParams{
		Project: r.PathValue("project"), Env: r.PathValue("env"), Service: r.PathValue("name")})
	if err != nil {
		return ""
	}
	return svc.Kind
}

func (h *handler) runSQL(w http.ResponseWriter, r *http.Request, q string, write bool, audit string) (proto.QueryData, bool) {
	var data proto.QueryData
	b, ok := h.runSQLRaw(w, r, q, write, audit)
	if ok && json.Unmarshal(b, &data) != nil {
		http.Error(w, "the agent's answer is not a result", http.StatusBadGateway)
		return data, false
	}
	return data, ok
}

func (h *handler) runSQLRaw(w http.ResponseWriter, r *http.Request, q string, write bool, audit string) ([]byte, bool) {
	svc, ok := h.service(w, r)
	if !ok {
		return nil, false
	}
	if _, found := browsable[svc.Kind]; !found {
		if svc.Kind == "web" || svc.Kind == "worker" || svc.Kind == "cron" {
			http.Error(w, svc.Name+" is not a database.", http.StatusBadRequest)
		} else {
			http.Error(w, "Yawl does not browse "+map[string]string{"valkey": "Valkey", "mongodb": "MongoDB", "clickhouse": "ClickHouse"}[svc.Kind]+" yet; use the Terminal tab.", http.StatusBadRequest)
		}
		return nil, false
	}
	pw, err := app.DatabaseVar(r.Context(), h.a.St.Q, svc.EnvironmentID, svc.ID, "PASSWORD")
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, svc.Name+" has not been created yet. Deploy "+svc.Name+" first.", http.StatusConflict)
		return nil, false
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	if audit != "" {
		err := app.Event(r.Context(), h.a.St.Q, h.a.Now().UnixMilli(), "audit", r.Pattern, actor(r),
			map[string]any{"text": audit, "path": strings.TrimPrefix(r.Pattern, r.Method+" ")})
		if err != nil {
			http.Error(w, "could not write the audit line, so the query did not run: "+err.Error(), http.StatusInternalServerError)
			return nil, false
		}
	}
	cmd, _ := proto.NewCommand("query:"+store.NewID("q_"), proto.CmdQuery, proto.QueryArgs{Kind: svc.Kind, Project: svc.Project, Env: svc.Env,
		Name: svc.Name, Version: int(svc.Version), Password: pw, SQL: q, Write: write})
	ctx, cancel := context.WithTimeout(r.Context(), waitPastAgentQueryLimit)
	defer cancel()
	res, err := h.gw.Send(ctx, svc.ServerID, cmd)
	switch {
	case err != nil:
		http.Error(w, "the agent did not answer: "+err.Error(), http.StatusBadGateway)
		return nil, false
	case res.Cause == "unknown command "+proto.CmdQuery:
		srv, _ := h.a.St.Q.GetServer(r.Context(), svc.ServerID)
		http.Error(w, "The agent on "+srv.Name+" is older than the panel; update it to browse data.", http.StatusBadGateway)
		return nil, false
	case !res.OK:
		http.Error(w, res.Cause, http.StatusUnprocessableEntity)
		return nil, false
	}
	return res.Data, true
}
