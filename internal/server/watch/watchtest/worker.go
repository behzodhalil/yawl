package watchtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/watchsig"
)

type Worker struct {
	Key        []byte
	Status     int
	Watched    map[string]bool
	LastRunAge time.Duration
	NeverRan   bool
	BacklogAge time.Duration

	mu        sync.Mutex
	Signals   []map[string]any
	Expects   []map[string]any
	Incidents []map[string]any
	URL       string
}

func NewWorker(t *testing.T, key []byte) *Worker {
	w := &Worker{Key: key, Watched: map[string]bool{}}
	s := httptest.NewServer(w)
	t.Cleanup(s.Close)
	w.URL = s.URL
	return w
}

func (w *Worker) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	id := r.Header.Get("X-Yawl-Id")
	t, _ := strconv.ParseInt(r.Header.Get("X-Yawl-Time"), 10, 64)
	key := w.Key
	if id != "panel" {
		key = watchsig.AgentKey(w.Key, id)
	}
	if d := time.Now().Unix() - t; d > 60 || d < -60 || watchsig.Sign(key, r.Method, r.URL.RequestURI(), t, body) != r.Header.Get("X-Yawl-Mac") {
		http.Error(rw, "unauthorized", 401)
		return
	}
	if w.Status != 0 {
		http.Error(rw, "unavailable", w.Status)
		return
	}
	rw.Header().Set("content-type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "POST /signal":
		var m map[string]any
		json.Unmarshal(body, &m)
		m["id"] = id
		w.Signals = append(w.Signals, m)
		now := time.Now().UnixMilli()
		out := map[string]int64{"ack": now, "last_run": now - w.LastRunAge.Milliseconds()}
		if w.NeverRan {
			out["last_run"] = 0
		}
		if w.BacklogAge > 0 {
			out["backlog_at"] = now - w.BacklogAge.Milliseconds()
		}
		json.NewEncoder(rw).Encode(out)
	case "POST /expectations":
		var m map[string]any
		json.Unmarshal(body, &m)
		w.Expects = append(w.Expects, m)
		rw.Write([]byte(`{"ok":true}`))
	case "POST /incident":
		var m map[string]any
		json.Unmarshal(body, &m)
		w.Incidents = append(w.Incidents, m)
		if s, _ := m["subject"].(string); w.Watched[s] {
			rw.Write([]byte(`{"ignored":true}`))
			return
		}
		rw.Write([]byte(`{"ok":true}`))
	case "GET /incidents":
		rw.Write([]byte(`[{"id":"inc_1","subject":"service:svc_1","closed_at":null}]`))
	case "GET /checks":
		rw.Write([]byte(`{"at":1,"results":{"service:svc_1":{"ok":true,"ms":120,"detail":"","since":1}}}`))
	default:
		http.NotFound(rw, r)
	}
}

func (w *Worker) Subjects() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, s := range w.Signals {
		out = append(out, s["subject"].(string))
	}
	return out
}

func (w *Worker) Count() (signals, expects, incidents int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.Signals), len(w.Expects), len(w.Incidents)
}
