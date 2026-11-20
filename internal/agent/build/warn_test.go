package build

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func tree(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestMissingKeys(t *testing.T) {
	dir := tree(t, map[string]string{
		"index.js":          "const k = process.env.STRIPE_KEY; const p = process.env.PORT; const e = process.env['ALSO']\n",
		"main.go":           "package main\nimport \"os\"\nvar _ = os.Getenv(\"DB\")\n",
		"app.py":            "import os\nx = os.environ.get('X')\ny = os.getenv(\"DB\")\n",
		"node_modules/a.js": "process.env.HIDDEN\n",
		"vendor/v/v.go":     "os.Getenv(\"HIDDEN2\")\n",
		"README.md":         "process.env.NOT_CODE\n",
	})
	got := Warnings(dir, []string{"DB"}, "production", "generate")
	want := []string{
		"The code reads ALSO, which is not set in production.",
		"The code reads STRIPE_KEY, which is not set in production.",
		"The code reads X, which is not set in production.",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestLanguageVersion(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		uses  string
		want  []string
	}{
		{"engines vs dockerfile", map[string]string{"package.json": `{"engines":{"node":">=18 <19"}}`, "Dockerfile": "FROM node:22-slim\n"}, "dockerfile",
			[]string{"package.json asks for Node 18; the build uses Node 22."}},
		{"engines at least", map[string]string{"package.json": `{"engines":{"node":">=18"}}`, "Dockerfile": "FROM node:22-slim\n"}, "dockerfile", nil},
		{"nvmrc vs engines, detected", map[string]string{"package.json": `{"engines":{"node":"18"},"scripts":{"start":"x"}}`, ".nvmrc": "20\n"}, "generate",
			[]string{"package.json asks for Node 18; the build uses Node 20."}},
		{"python", map[string]string{".python-version": "3.11.4\n", "Dockerfile": "FROM python:3.12-slim\n"}, "dockerfile",
			[]string{".python-version asks for Python 3.11; the build uses Python 3.12."}},
		{"go older build", map[string]string{"go.mod": "module x\n\ngo 1.23\n", "Dockerfile": "FROM golang:1.21 AS build\n"}, "dockerfile",
			[]string{"go.mod asks for Go 1.23; the build uses Go 1.21."}},
		{"go newer build is fine", map[string]string{"go.mod": "module x\n\ngo 1.21\n", "Dockerfile": "FROM golang:1.23\n"}, "dockerfile", nil},
		{"detected go", map[string]string{"go.mod": "module x\n\ngo 1.21\n"}, "generate", nil},
		{"railpack", map[string]string{"package.json": `{"engines":{"node":"18"}}`, "Dockerfile": "FROM node:22-slim\n"}, "railpack", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Warnings(tree(t, c.files), nil, "production", c.uses); !slices.Equal(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestKeysFromViteCode(t *testing.T) {
	dir := tree(t, map[string]string{
		"src/App.tsx":  "const a = import.meta.env.VITE_API; const m = import.meta.env.MODE; import.meta.env.DEV; import.meta.env.PROD; import.meta.env.BASE_URL; import.meta.env.SSR\n",
		"src/page.jsx": "const x = process.env.NEXT_PUBLIC_X\n",
	})
	got := Warnings(dir, nil, "production", "generate")
	want := []string{
		"The code reads NEXT_PUBLIC_X, which is not set in production.",
		"The code reads VITE_API, which is not set in production.",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestViteNamesOnlyFromImportMeta(t *testing.T) {
	dir := tree(t, map[string]string{"index.js": "const u = process.env.BASE_URL; const m = import.meta.env.MODE\n", "app.py": "import os\nos.getenv(\"MODE\")\n"})
	got := Warnings(dir, nil, "production", "generate")
	want := []string{"The code reads BASE_URL, which is not set in production.", "The code reads MODE, which is not set in production."}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestWarningsSkipLinks(t *testing.T) {
	outside := tree(t, map[string]string{"package.json": `{"engines":{"node":"18"}}`, "out.js": "process.env.OUTSIDE\n"})
	dir := tree(t, map[string]string{"Dockerfile": "FROM node:22-slim\n", "index.js": "process.env.INSIDE\n"})
	for link, to := range map[string]string{"x.js": "/dev/zero", "package.json": filepath.Join(outside, "package.json"), "y.js": filepath.Join(outside, "out.js")} {
		if err := os.Symlink(to, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan []string)
	go func() { done <- Warnings(dir, nil, "production", "dockerfile") }()
	select {
	case got := <-done:
		if want := []string{"The code reads INSIDE, which is not set in production."}; !slices.Equal(got, want) {
			t.Fatalf("got %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Warnings hung on a link to /dev/zero")
	}
	if read(dir, "package.json") != "" {
		t.Fatal("read followed a link out of the clone")
	}
}
