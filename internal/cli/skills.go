package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/behzodhalil/yawl/internal/agentskill"
)

func skillsCmd(args []string) error {
	if len(args) != 2 || args[0] != "add" || agentskill.Paths[args[1]] == "" {
		return errors.New("usage: yawl skills add claude|cursor|codex")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	p := filepath.Join(root, agentskill.Paths[args[1]])
	existing, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, agentskill.Render(args[1], existing), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Wrote", agentskill.Paths[args[1]])
	return nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", errors.New("not in a git repository; run yawl skills add from your app's repository")
		}
	}
}
