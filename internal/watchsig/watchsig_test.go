package watchsig

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

type vector struct {
	Key      string `json:"key"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Time     int64  `json:"time"`
	Body     string `json:"body"`
	Mac      string `json:"mac"`
	Server   string `json:"server"`
	AgentKey string `json:"agent_key"`
}

func load(t *testing.T) (vector, []byte) {
	t.Helper()
	b, err := os.ReadFile("../../testdata/watchsig.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vector
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = 7
	}
	return v, key
}

func TestSignVector(t *testing.T) {
	v, key := load(t)
	if got := Sign(key, v.Method, v.Path, v.Time, []byte(v.Body)); got != v.Mac {
		t.Fatalf("Sign = %s, want %s", got, v.Mac)
	}
	if got := hex.EncodeToString(AgentKey(key, v.Server)); got != v.AgentKey {
		t.Fatalf("AgentKey = %s, want %s", got, v.AgentKey)
	}
}

func TestHeadersSet(t *testing.T) {
	v, key := load(t)
	h := http.Header{}
	Headers(h, "panel", key, v.Method, v.Path, time.Unix(v.Time, 0), []byte(v.Body))
	if h.Get("X-Yawl-Id") != "panel" || h.Get("X-Yawl-Time") != "1798000000" || h.Get("X-Yawl-Mac") != v.Mac {
		t.Fatalf("headers %v", h)
	}
}
