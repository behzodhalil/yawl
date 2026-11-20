package agent

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/watchsig"
)

type signals struct {
	mu     sync.Mutex
	status int
	got    []string
	bad    int
}

func (s *signals) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body bytes.Buffer
	body.ReadFrom(r.Body)
	key := watchsig.AgentKey(bytes.Repeat([]byte{7}, 32), r.Header.Get("X-Yawl-Id"))
	t := r.Header.Get("X-Yawl-Time")
	var ts int64
	json.Unmarshal([]byte(t), &ts)
	if r.Header.Get("X-Yawl-Id") != "srv_1" || watchsig.Sign(key, r.Method, r.URL.RequestURI(), ts, body.Bytes()) != r.Header.Get("X-Yawl-Mac") {
		s.bad++
		w.WriteHeader(401)
		return
	}
	var b struct{ Subject string }
	json.Unmarshal(body.Bytes(), &b)
	s.got = append(s.got, b.Subject)
	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	w.Write([]byte(`{"ack":1}`))
}

func (s *signals) count() (int, int) { s.mu.Lock(); defer s.mu.Unlock(); return len(s.got), s.bad }

func writeWatch(t *testing.T, dir, url string) {
	key := hex.EncodeToString(watchsig.AgentKey(bytes.Repeat([]byte{7}, 32), "srv_1"))
	b, _ := json.Marshal(proto.WatchArgs{URL: url, Key: key})
	if err := os.WriteFile(filepath.Join(dir, "watch.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out")
}

func TestSignalPosts(t *testing.T) {
	s := &signals{}
	srv := httptest.NewServer(s)
	defer srv.Close()
	dir := t.TempDir()
	writeWatch(t, dir, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Signal(ctx, dir, "srv_1", nil, 50*time.Millisecond)
	waitFor(t, func() bool { n, _ := s.count(); return n >= 2 })
	if _, bad := s.count(); bad != 0 || s.got[0] != "server:srv_1" {
		t.Fatalf("got %v bad %d", s.got, bad)
	}
}

func TestSignalNoFileIdle(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(old)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	Signal(ctx, t.TempDir(), "srv_1", nil, 20*time.Millisecond)
	if strings.TrimSpace(logs.String()) != "" {
		t.Fatalf("logged %q", logs.String())
	}
}

func TestSignalSurvivesErrors(t *testing.T) {
	s := &signals{status: 500}
	srv := httptest.NewServer(s)
	defer srv.Close()
	dir := t.TempDir()
	writeWatch(t, dir, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Signal(ctx, dir, "srv_1", nil, 30*time.Millisecond)
	waitFor(t, func() bool { n, _ := s.count(); return n >= 3 })
}

func TestCronReports(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body bytes.Buffer
		body.ReadFrom(r.Body)
		key := watchsig.AgentKey(bytes.Repeat([]byte{7}, 32), "srv_1")
		var ts int64
		json.Unmarshal([]byte(r.Header.Get("X-Yawl-Time")), &ts)
		if r.URL.Path != "/cron" || r.Header.Get("X-Yawl-Id") != "srv_1" || watchsig.Sign(key, "POST", "/cron", ts, body.Bytes()) != r.Header.Get("X-Yawl-Mac") {
			w.WriteHeader(401)
			return
		}
		var m map[string]any
		json.Unmarshal(body.Bytes(), &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}))
	defer srv.Close()
	dir := t.TempDir()
	report := ReportCron(dir, "srv_1", nil)
	j := proto.CronJob{ID: "svc_1"}
	report(context.Background(), "start", j, proto.CronRun{ScheduledAt: 1000})
	if len(got) != 0 {
		t.Fatal("sent without watch.json")
	}
	writeWatch(t, dir, srv.URL)
	report(context.Background(), "finish", j, proto.CronRun{ScheduledAt: 1000, StartedAt: 1500, FinishedAt: 42500, Exit: 3})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0]["subject"] != "cron:svc_1" || got[0]["event"] != "finish" || got[0]["exit"] != float64(3) ||
		got[0]["scheduled_at"] != float64(1000) || got[0]["ms"] != float64(41000) {
		t.Fatalf("%v", got)
	}
}
