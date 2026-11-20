package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFolder(t *testing.T) {
	dir, out := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(dir, "apps", "web"), 0o755)
	os.WriteFile(filepath.Join(dir, "README"), nil, 0o644)
	os.Symlink(out, filepath.Join(dir, "away"))
	os.Symlink(filepath.Join(dir, "apps"), filepath.Join(dir, "in"))
	base, _ := filepath.EvalSymlinks(dir)
	for root, want := range map[string]string{"": dir, "apps/web": filepath.Join(base, "apps", "web"), "in/web": filepath.Join(base, "apps", "web")} {
		if got, err := folder(dir, root); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", root, got, err, want)
		}
	}
	for root, cause := range map[string]string{"missing": "is not in the repository", "README": "is not a folder", "away": "leads out of", "../" + filepath.Base(out): "leads out of"} {
		if _, err := folder(dir, root); err == nil || !strings.Contains(err.Error(), cause) {
			t.Errorf("%q: %v, want %q", root, err, cause)
		}
	}
}
