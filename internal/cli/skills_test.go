package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsAddWritesAtTheRepoRoot(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	sub := filepath.Join(root, "apps", "web")
	os.MkdirAll(sub, 0o755)
	t.Chdir(sub)
	out := &bytes.Buffer{}
	stdout = out
	for _, agent := range []string{"claude", "cursor", "codex"} {
		if err := Run(context.Background(), "skills", []string{"add", agent}); err != nil {
			t.Fatal(agent, err)
		}
	}
	for _, p := range []string{".claude/skills/yawl/SKILL.md", ".cursor/rules/yawl.mdc", "AGENTS.md"} {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil || !strings.Contains(string(b), "Yawl") {
			t.Errorf("%s: %v", p, err)
		}
	}
	if !strings.Contains(out.String(), "Wrote AGENTS.md") {
		t.Errorf("%q", out)
	}
}

func TestSkillsAddRefusals(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := Run(context.Background(), "skills", []string{"add", "claude"}); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Errorf("outside a repo: %v", err)
	}
	if err := Run(context.Background(), "skills", []string{"add", "vim"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("unknown agent: %v", err)
	}
}
