package agent

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/watchsig"
)

func Signal(ctx context.Context, dataDir, serverID string, hc *http.Client, every time.Duration) {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	path := filepath.Join(dataDir, "watch.json")
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		signalOnce(ctx, path, serverID, hc)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func signalOnce(ctx context.Context, path, serverID string, hc *http.Client) {
	body, _ := json.Marshal(map[string]string{"subject": "server:" + serverID})
	post(ctx, path, serverID, hc, "/signal", body)
}

func post(ctx context.Context, path, serverID string, hc *http.Client, route string, body []byte) {
	b, err := os.ReadFile(path)
	if err != nil {
		slog.Debug("watcher: not set up", "err", err)
		return
	}
	var w proto.WatchArgs
	if err := json.Unmarshal(b, &w); err != nil {
		slog.Warn("watcher: bad watch.json", "err", err)
		return
	}
	key, err := hex.DecodeString(w.Key)
	if err != nil {
		slog.Warn("watcher: bad key", "err", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, "POST", w.URL+route, bytes.NewReader(body))
	if err != nil {
		slog.Warn("watcher: "+route, "err", err)
		return
	}
	watchsig.Headers(req.Header, serverID, key, "POST", req.URL.RequestURI(), time.Now(), body)
	res, err := hc.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("watcher: "+route, "err", err)
		}
		return
	}
	res.Body.Close()
	if res.StatusCode/100 != 2 {
		slog.Warn("watcher: refused", "route", route, "status", res.StatusCode)
	}
}

func ReportCron(dataDir, serverID string, hc *http.Client) func(context.Context, string, proto.CronJob, proto.CronRun) {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	path := filepath.Join(dataDir, "watch.json")
	return func(ctx context.Context, event string, j proto.CronJob, r proto.CronRun) {
		ms := int64(0)
		if r.FinishedAt > 0 && r.StartedAt > 0 {
			ms = r.FinishedAt - r.StartedAt
		}
		body, _ := json.Marshal(map[string]any{"subject": "cron:" + j.ID, "event": event, "scheduled_at": r.ScheduledAt,
			"exit": r.Exit, "ms": ms, "cause": r.Cause, "timed_out": r.TimedOut})
		post(ctx, path, serverID, hc, "/cron", body)
	}
}
