//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2EImport(t *testing.T) {
	b := setup(t)
	b.withDB()
	pw, err := run(t, "", "env", "reveal", "notes-db", "PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	var z bytes.Buffer
	w := gzip.NewWriter(&z)
	w.Write([]byte("CREATE TABLE notes (body text);\nINSERT INTO notes VALUES ('from heroku'), ('second');\n"))
	w.Close()
	dump := filepath.Join(b.dir, "heroku.sql.gz")
	os.WriteFile(dump, z.Bytes(), 0o600)
	out, err := run(t, "", "db", "import", "notes-db", dump)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "snapshot") || !strings.Contains(out, "import") {
		t.Fatalf("%s", out)
	}
	if n := pgOut(t, "notes-db", strings.TrimSpace(pw), "select count(*) from notes"); n != "2" {
		t.Fatalf("rows %q", n)
	}
	if left, _ := os.ReadDir(filepath.Join(b.dir, "data", "imports")); len(left) != 0 {
		t.Fatalf("the panel kept %d uploads", len(left))
	}
}
