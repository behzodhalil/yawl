package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const mod = "github.com/behzodhalil/yawl/"

func imports(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	err := filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && p != ".." {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		dir, _ := filepath.Rel("..", filepath.Dir(p))
		for _, im := range f.Imports {
			v, _ := strconv.Unquote(im.Path.Value)
			out[filepath.ToSlash(dir)] = append(out[filepath.ToSlash(dir)], v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func under(dir, prefix string) bool {
	return prefix == "." || dir == prefix || strings.HasPrefix(dir, prefix+"/")
}

func forbid(t *testing.T, from string, bad func(imp string) bool) {
	t.Helper()
	for dir, ims := range imports(t) {
		if !under(dir, from) {
			continue
		}
		for _, im := range ims {
			if bad(im) {
				t.Errorf("%s imports %s", dir, im)
			}
		}
	}
}

func TestNoOsExec(t *testing.T) {
	forbid(t, ".", func(im string) bool { return im == "os/exec" })
}

func TestServerAgentApart(t *testing.T) {
	forbid(t, "internal/server", func(im string) bool { return strings.HasPrefix(im, mod+"internal/agent") })
	forbid(t, "internal/agent", func(im string) bool { return strings.HasPrefix(im, mod+"internal/server") })
}

func TestPureLayers(t *testing.T) {
	forbid(t, "internal/cron", func(im string) bool { return strings.HasPrefix(im, mod+"internal/") })
	forbid(t, "internal/spec", func(im string) bool {
		return strings.HasPrefix(im, mod+"internal/") && im != mod+"internal/cron"
	})
	forbid(t, "internal/proto", func(im string) bool {
		return strings.HasPrefix(im, mod+"internal/") && im != mod+"internal/spec"
	})
}

func TestCLINotServer(t *testing.T) {
	forbid(t, "internal/cli", func(im string) bool { return strings.HasPrefix(im, mod+"internal/server") })
}

func TestAdaptersNoProto(t *testing.T) {
	for _, a := range []string{"internal/agent/runtime", "internal/agent/proxy", "internal/agent/build", "internal/agent/backup", "internal/agent/systemd"} {
		forbid(t, a, func(im string) bool { return im == mod+"internal/proto" })
	}
}

func TestServerNeverSeals(t *testing.T) {
	all := imports(t)
	var reach func(im string, seen map[string]bool) bool
	reach = func(im string, seen map[string]bool) bool {
		if im == mod+"internal/seal" || im == "golang.org/x/crypto/nacl/box" {
			return true
		}
		dir, ours := strings.CutPrefix(im, mod)
		if !ours || seen[dir] {
			return false
		}
		seen[dir] = true
		return slices.ContainsFunc(all[dir], func(next string) bool { return reach(next, seen) })
	}
	forbid(t, "internal/server", func(im string) bool { return reach(im, map[string]bool{}) })
}

func TestCloudOverHTTP(t *testing.T) {
	files, _ := filepath.Glob("cloud/*.go")
	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			if v := strings.Trim(im.Path.Value, `"`); strings.HasPrefix(v, mod+"internal/server") || strings.HasPrefix(v, mod+"internal/agent") {
				t.Errorf("%s imports %s", p, v)
			}
		}
	}
	if len(files) == 0 {
		t.Fatal("no cloud package")
	}
}
