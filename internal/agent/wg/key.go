package wg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func LoadOrCreateKey(dir string) (wgtypes.Key, error) {
	p := filepath.Join(dir, "wg.key")
	if b, err := os.ReadFile(p); err == nil {
		return wgtypes.ParseKey(strings.TrimSpace(string(b)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return wgtypes.Key{}, err
	}
	k, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return wgtypes.Key{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return wgtypes.Key{}, err
	}
	return k, os.WriteFile(p, []byte(k.String()+"\n"), 0o600)
}

// AllowNonlocalBind sets net.ipv4.ip_nonlocal_bind (path is its /proc file):
// a database bound to this server's mesh address must start at boot, before
// the panel's first Network command has made yawl0 (review C2). install.sh
// makes it permanent; the agent sets it for servers installed before that.
func AllowNonlocalBind(path string) error {
	return os.WriteFile(path, []byte("1\n"), 0o644)
}
