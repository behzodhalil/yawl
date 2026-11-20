package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/behzodhalil/yawl/internal/update"
)

type Follow struct {
	Updater   func() (update.Updater, error)
	Current   string
	Exe       string
	Exit      func()
	Wait      func(context.Context) error
	Every     time.Duration
	Now       func() time.Time
	Log       *slog.Logger
	StatePath string

	mu   sync.Mutex
	last time.Time
}

const exitWait = 30 * time.Second

func (f *Follow) Check(ctx context.Context, serverVersion string) {
	if serverVersion == "" || serverVersion == "dev" || f.Current == "dev" || serverVersion == f.Current {
		return
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	f.mu.Lock()
	if f.last.IsZero() && f.StatePath != "" {
		if b, err := os.ReadFile(f.StatePath); err == nil {
			var v string
			var ms int64
			if _, err := fmt.Sscanf(string(b), "%s %d", &v, &ms); err == nil && v == serverVersion {
				f.last = time.UnixMilli(ms)
			}
		}
	}
	if !f.last.IsZero() && now().Sub(f.last) < f.Every {
		f.mu.Unlock()
		return
	}
	f.last = now()
	if f.StatePath != "" {
		os.WriteFile(f.StatePath, []byte(fmt.Sprintf("%s %d", serverVersion, f.last.UnixMilli())), 0o600)
	}
	f.mu.Unlock()
	log := f.Log
	if log == nil {
		log = slog.Default()
	}
	u, err := f.Updater()
	if err != nil {
		log.Warn("the panel runs another version; this agent cannot update", "panel", serverVersion, "agent", f.Current, "err", err)
		return
	}
	path, err := u.Fetch(ctx, serverVersion, goruntime.GOOS, goruntime.GOARCH, filepath.Dir(f.Exe))
	if err != nil {
		log.Warn("agent update failed; trying again in an hour", "to", serverVersion, "err", err)
		return
	}
	if err := update.Swap(path, f.Exe); err != nil {
		log.Warn("agent update failed; trying again in an hour", "to", serverVersion, "err", err)
		return
	}
	log.Info("agent updated; restarting", "from", f.Current, "to", serverVersion)
	if f.Wait != nil {
		wctx, cancel := context.WithTimeout(ctx, exitWait)
		if err := f.Wait(wctx); err != nil {
			log.Warn("commands still running; restarting anyway", "err", err)
		}
		cancel()
	}
	f.Exit()
}
