package app

import (
	"context"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

func TestImportRefusals(t *testing.T) {
	st, web := seed(t)
	a, ctx := New(st), context.Background()
	for _, k := range []string{"postgres", "valkey"} {
		st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_" + k, EnvironmentID: "env_1", Name: k, Kind: k, Version: 17, CreatedAt: 1, UpdatedAt: 1})
	}
	for _, c := range []struct{ svc, src, ref, want string }{
		{web, "path", "d.sql", "a database"},
		{"svc_valkey", "path", "d.sql", "Valkey"},
		{"svc_postgres", "url", "ftp://x/d.sql", "http or https"},
		{"svc_postgres", "path", "../etc/passwd", "a file name"},
		{"svc_postgres", "disk", "d.sql", "upload, url or path"},
	} {
		if _, err := a.Import(ctx, c.svc, c.src, c.ref, "", "owner"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if _, err := a.Import(ctx, "svc_postgres", "url", "https://bucket.example/d.sql?X-Amz-Signature=hush", "", "owner"); err != nil {
		t.Fatal(err)
	}
	var data string
	st.DB.QueryRow(`SELECT body_json FROM events WHERE kind = 'import.queued'`).Scan(&data)
	if !strings.Contains(data, "bucket.example/d.sql") || strings.Contains(data, "hush") {
		t.Fatalf("%s", data)
	}
}

func TestImportNotSuperseded(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	st.Q.UpsertService(ctx, db.UpsertServiceParams{ID: "svc_pg", EnvironmentID: "env_1", Name: "pg", Kind: "postgres", Version: 17, CreatedAt: 1, UpdatedAt: 1})
	imp, err := a.Import(ctx, "svc_pg", "path", "d.sql", "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Deploy(ctx, "svc_pg", "", "owner"); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Q.GetDeploy(ctx, imp); d.State != "queued" {
		t.Fatalf("a redeploy took the import: %s", d.State)
	}
	if err := a.Delete(ctx, "service", "svc_pg", "owner"); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Q.GetDeploy(ctx, imp); d.State != "superseded" {
		t.Fatalf("a deleted database's import: %s", d.State)
	}
}
