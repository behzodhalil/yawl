package agentskill

import (
	"reflect"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

func TestBodyNamesEveryYAMLField(t *testing.T) {
	for _, v := range []any{spec.File{}, spec.Service{}, spec.Volume{}, spec.Keep{}} {
		typ := reflect.TypeOf(v)
		for i := range typ.NumField() {
			tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
			if tag == "" || tag == "-" {
				continue
			}
			if !strings.Contains(Body, "`"+tag+"`") && !strings.Contains(Body, tag+",") && !strings.Contains(Body, tag+"}") {
				t.Errorf("skill.md does not name %s.%s (%q)", typ.Name(), typ.Field(i).Name, tag)
			}
		}
	}
}

func TestRenderFrontMatter(t *testing.T) {
	for _, agent := range []string{"claude", "cursor"} {
		out := string(Render(agent, nil))
		if !strings.HasPrefix(out, "---\n") || !strings.Contains(out, "\n---\n\n# Yawl\n") {
			t.Errorf("%s: %.80q", agent, out)
		}
	}
	if !strings.Contains(string(Render("claude", nil)), "name: yawl\n") {
		t.Error("claude skill has no name")
	}
}

func TestRenderCodexKeepsTheRestOfAgentsMD(t *testing.T) {
	first := Render("codex", []byte("# Repo\n\nRun make test.\n"))
	if !strings.HasPrefix(string(first), "# Repo\n\nRun make test.\n\n"+sectionStart+"\n## Yawl\n") {
		t.Fatalf("%.120q", first)
	}
	if strings.Contains(string(first), "\n# ") {
		t.Fatal("a top-level heading inside the Yawl section")
	}
	again := Render("codex", append(first, "\nMore notes.\n"...))
	if strings.Count(string(again), sectionStart) != 1 || !strings.HasSuffix(string(again), sectionEnd+"\n\nMore notes.\n") {
		t.Fatalf("%q", again[len(again)-80:])
	}
	if got := Render("codex", nil); !strings.HasPrefix(string(got), sectionStart) {
		t.Fatalf("%.40q", got)
	}
}
