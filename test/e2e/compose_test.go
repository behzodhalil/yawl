//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	gitconfig "github.com/go-git/go-git/v5/config"
)

func TestE2ECompose(t *testing.T) {
	b := setupApp(t, "pgnote")
	dropDBs(t)
	t.Cleanup(func() { dropDBs(t) })
	if _, err := run(t, "", "recovery-key", "init"); err != nil {
		t.Fatal(err)
	}
	b.repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{"file://" + b.repoDir}})
	os.WriteFile(filepath.Join(b.repoDir, "yawl.yaml"), []byte("project: e2e\nenvironment: production\nserver: e2e\nservices: []\n"), 0o644)
	os.WriteFile(filepath.Join(b.repoDir, "docker-compose.yml"), []byte(`services:
  hello:
    build: .
    ports: ["8080:3000"]
    environment:
      DATABASE_URL: postgres://postgres:secret@notes-db:5432/postgres
    depends_on: [notes-db]
  notes-db:
    image: postgres:17
    environment: {POSTGRES_PASSWORD: secret}
`), 0o644)
	if out, err := run(t, "", "compose", "import", "--apply", "--yes"); err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if out, err := run(t, "", "deploy", "notes-db"); err != nil {
		t.Fatalf("deploy notes-db: %v\n%s", err, out)
	}
	if err := b.deploy(); err != nil {
		t.Fatal(err)
	}
	if v, err := get(); v != "no notes" {
		t.Fatalf("app served %q (%v)", v, err)
	}
}
