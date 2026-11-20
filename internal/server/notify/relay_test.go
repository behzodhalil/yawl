package notify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestRelayErrors(t *testing.T) {
	answer := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(409)
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	answer = "No chat is linked.\n"
	err := Relay{URL: srv.URL + "/", Key: "k"}.Send(context.Background(), Message{Text: "x"})
	var re *RelayError
	if !errors.As(err, &re) || re.Status != 409 || re.Msg != "No chat is linked." {
		t.Fatalf("got %v", err)
	}
	answer = "<html>bad gateway</html>"
	if err := (Relay{URL: srv.URL, Key: "k"}).Unlink(context.Background(), "7"); err == nil || err.Error() != "the Telegram relay answered 409" {
		t.Fatalf("got %v", err)
	}
	if _, err := (Relay{URL: "http://127.0.0.1:1"}).Health(context.Background()); err == nil {
		t.Fatal("no error from a relay that is not there")
	}
}

func TestRouteTo(t *testing.T) {
	chats := []RelayChat{{ID: "7"}, {ID: "-100"}, {ID: "-200"}, {ID: "-300", GoneAt: 5}}
	routes := map[string]Route{
		"-100": {Servers: []string{"srv_1"}},
		"-200": {Servers: []string{"srv_2"}, Projects: []string{"acme"}},
	}
	for _, c := range []struct {
		name, server, project string
		to                    []string
		all                   bool
	}{
		{"by server", "srv_1", "", []string{"7", "-100"}, false},
		{"by project", "", "acme", []string{"7", "-200"}, false},
		{"either", "srv_1", "acme", []string{"7", "-100", "-200"}, true},
		{"neither matches", "srv_3", "blog", []string{"7"}, false},
		{"no metadata", "", "", []string{"7", "-100", "-200"}, true},
	} {
		to, all := RouteTo(chats, routes, c.server, c.project)
		if !slices.Equal(to, c.to) || all != c.all {
			t.Errorf("%s: %v %v", c.name, to, all)
		}
	}
	if to, all := RouteTo(chats, nil, "srv_1", "acme"); len(to) != 3 || !all {
		t.Errorf("no routes: %v %v", to, all)
	}
	known := map[string]Route{}
	for id, r := range routes {
		known[id] = r.Known(map[string]bool{"srv_1": false}, map[string]bool{"acme": true})
	}
	if k := known["-200"]; len(k.Servers) != 0 || !slices.Equal(k.Projects, []string{"acme"}) {
		t.Errorf("known: %+v", k)
	}
	if to, _ := RouteTo(chats, known, "srv_2", ""); !slices.Equal(to, []string{"7", "-100"}) {
		t.Errorf("deleted server: %v", to)
	}
}
