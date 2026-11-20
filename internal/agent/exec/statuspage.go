package exec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/behzodhalil/yawl/internal/proto"
)

const maxStatusHTML = 1 << 20

func (d *Dispatcher) statusFile(id string) string {
	if id == "" || d.StatusDir == "" || strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
		return ""
	}
	return filepath.Join(d.StatusDir, id+".html")
}

func (d *Dispatcher) statusPage(a proto.StatusPageArgs) proto.Result {
	file := d.statusFile(a.ID)
	switch {
	case file == "":
		return bad(errors.New("a status page id is lowercase letters, digits and _"))
	case len(a.HTML) > maxStatusHTML:
		return bad(errors.New("a status page is at most 1 MB"))
	case a.HTML == "":
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return proto.Result{Cause: err.Error()}
		}
		return ok(nil)
	}
	if err := os.MkdirAll(d.StatusDir, 0o755); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(a.HTML), 0o644); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	if err := os.Rename(tmp, file); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	return ok(nil)
}
