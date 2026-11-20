package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeHcloud struct {
	mu         sync.Mutex
	created    map[string]any
	deletes    []string
	status     string
	failWait   bool
	failDelete bool
}

func (f *fakeHcloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer TOKEN" {
		w.WriteHeader(401)
		return
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/ssh_keys":
		w.WriteHeader(201)
		w.Write([]byte(`{"ssh_key":{"id":11}}`))
	case r.Method == "POST" && r.URL.Path == "/v1/servers":
		json.NewDecoder(r.Body).Decode(&f.created)
		w.WriteHeader(201)
		w.Write([]byte(`{"server":{"id":22,"status":"initializing","public_net":{"ipv4":{"ip":"5.6.7.8"}}}}`))
	case r.Method == "GET" && r.URL.Path == "/v1/servers/22":
		if f.failWait {
			w.WriteHeader(500)
			return
		}
		w.Write([]byte(`{"server":{"id":22,"status":"running","public_net":{"ipv4":{"ip":"5.6.7.8"}}}}`))
	case r.Method == "DELETE":
		f.deletes = append(f.deletes, r.URL.Path)
		if f.failDelete {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	default:
		w.WriteHeader(404)
	}
}

type cleanups struct {
	fns    []func()
	errors []string
}

func (c *cleanups) Cleanup(f func()) { c.fns = append(c.fns, f) }
func (c *cleanups) Errorf(format string, args ...any) {
	c.errors = append(c.errors, fmt.Sprintf(format, args...))
}
func (c *cleanups) run() {
	for i := len(c.fns) - 1; i >= 0; i-- {
		c.fns[i]()
	}
}

func TestHcloudCreateDelete(t *testing.T) {
	f := &fakeHcloud{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := Client{Base: srv.URL + "/v1", Token: "TOKEN"}
	cl := &cleanups{}
	s, err := Provision(context.Background(), c, cl, ServerOpts{Name: "yawl-accept", Type: "cx22", Image: "ubuntu-24.04", Location: "hel1", PublicKey: "ssh-ed25519 AAAA test"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != 22 || s.IP != "5.6.7.8" {
		t.Fatalf("%+v", s)
	}
	if f.created["server_type"] != "cx22" || f.created["image"] != "ubuntu-24.04" || f.created["location"] != "hel1" || !strings.Contains(toJSON(f.created["ssh_keys"]), "11") {
		t.Fatalf("%v", f.created)
	}
	cl.run()
	if len(f.deletes) != 2 || f.deletes[0] != "/v1/servers/22" || f.deletes[1] != "/v1/ssh_keys/11" {
		t.Fatalf("deletes %v", f.deletes)
	}
}

func TestHcloudDeletesOnFailure(t *testing.T) {
	f := &fakeHcloud{failWait: true}
	srv := httptest.NewServer(f)
	defer srv.Close()
	cl := &cleanups{}
	if _, err := Provision(context.Background(), Client{Base: srv.URL + "/v1", Token: "TOKEN"}, cl,
		ServerOpts{Name: "x", Type: "cx22", Image: "ubuntu-24.04", Location: "hel1", PublicKey: "k"}); err == nil {
		t.Fatal("a failed wait succeeded")
	}
	cl.run()
	if len(f.deletes) != 2 {
		t.Fatalf("deletes %v", f.deletes)
	}
}

func TestHcloudDeleteFailureIsSaid(t *testing.T) {
	f := &fakeHcloud{failDelete: true}
	srv := httptest.NewServer(f)
	defer srv.Close()
	var cl cleanups
	Provision(context.Background(), Client{Base: srv.URL + "/v1", Token: "TOKEN"}, &cl,
		ServerOpts{Name: "yawl-accept-1", Type: "cx22", Image: "ubuntu-24.04", Location: "hel1", PublicKey: "k"})
	cl.run()
	if len(cl.errors) != 2 || !strings.Contains(cl.errors[0], "22") || !strings.Contains(cl.errors[0], "yawl-accept-1") {
		t.Fatalf("%v", cl.errors)
	}
	if !strings.Contains(toJSON(f.created["labels"]), `"yawl-accept":"1"`) {
		t.Fatalf("labels %v", f.created["labels"])
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
