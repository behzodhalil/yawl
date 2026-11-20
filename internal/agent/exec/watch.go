package exec

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/behzodhalil/yawl/internal/proto"
)

func (d *Dispatcher) watch(a proto.WatchArgs) proto.Result {
	if k, err := hex.DecodeString(a.Key); err != nil || len(k) != 32 {
		return proto.Result{Cause: "the watcher key is not 32 bytes of hex"}
	}
	if !strings.HasPrefix(a.URL, "https://") && !strings.HasPrefix(a.URL, "http://") {
		return proto.Result{Cause: "the watcher address is not a URL"}
	}
	b, _ := json.Marshal(a)
	tmp := d.WatchFile + ".tmp"
	if err := os.MkdirAll(filepath.Dir(d.WatchFile), 0o700); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	if err := os.Rename(tmp, d.WatchFile); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	return proto.Result{OK: true}
}
