package watch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientErrorTrimsBody(t *testing.T) {
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("<!doctype html><html>" + strings.Repeat("x", 10000) + "</html>"))
	}))
	defer html.Close()
	_, err := Client{URL: html.URL, Key: make([]byte, 32)}.Signal(context.Background(), "panel", 0)
	if err == nil || err.Error() != "the watcher answered 404" {
		t.Fatalf("%v", err)
	}
	long := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(strings.Repeat("y", 1000)))
	}))
	defer long.Close()
	_, err = Client{URL: long.URL, Key: make([]byte, 32)}.Signal(context.Background(), "panel", 0)
	if err == nil || len(err.Error()) > 260 || !strings.HasPrefix(err.Error(), "the watcher answered 500: yyy") {
		t.Fatalf("%d %v", len(err.Error()), err)
	}
}
