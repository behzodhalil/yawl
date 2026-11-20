package watchsig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

func Sign(key []byte, method, path string, t int64, body []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(method + "\n" + path + "\n" + strconv.FormatInt(t, 10) + "\n"))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func Headers(h http.Header, id string, key []byte, method, path string, now time.Time, body []byte) {
	t := now.Unix()
	h.Set("X-Yawl-Id", id)
	h.Set("X-Yawl-Time", strconv.FormatInt(t, 10))
	h.Set("X-Yawl-Mac", Sign(key, method, path, t, body))
}

func AgentKey(panelKey []byte, serverID string) []byte {
	m := hmac.New(sha256.New, panelKey)
	m.Write([]byte("agent:" + serverID))
	return m.Sum(nil)
}
