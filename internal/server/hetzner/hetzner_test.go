package hetzner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHetzner(t *testing.T) {
	var made map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"message":"unable to authenticate"}}`))
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/locations":
			w.Write([]byte(`{"locations":[{"name":"hel1","city":"Helsinki"},{"name":"ash","city":"Ashburn, VA"}]}`))
		case "GET /v1/server_types":
			w.Write([]byte(`{"server_types":[{"name":"cx22","cores":2,"memory":4,"disk":40,"architecture":"x86","deprecated":false,
				"prices":[{"location":"hel1","price_monthly":{"gross":"4.5100000000"}}]},
				{"name":"cx11","cores":1,"memory":2,"disk":20,"architecture":"x86","deprecated":true,"prices":[]}]}`))
		case "GET /v1/ssh_keys":
			w.Write([]byte(`{"ssh_keys":[{"id":7},{"id":9}]}`))
		case "POST /v1/servers":
			json.NewDecoder(r.Body).Decode(&made)
			w.Write([]byte(`{"server":{"id":42,"public_net":{"ipv4":{"ip":"203.0.113.5"}}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	APIBase = api.URL + "/v1"
	ctx := context.Background()
	if err := Check(ctx, "bad"); err == nil || !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("a bad token: %v", err)
	}
	o, err := Choices(ctx, "good")
	if err != nil || len(o.Locations) != 2 || len(o.Types) != 1 || o.Types[0].Name != "cx22" || o.Types[0].PricesByLocation["hel1"] != "4.51" {
		t.Fatalf("%+v %v", o, err)
	}
	s, err := Create(ctx, "good", "fsn2", "cx22", "hel1", "#!/bin/sh\necho hi\n")
	if err != nil || s.ID != 42 || s.IP != "203.0.113.5" {
		t.Fatalf("%+v %v", s, err)
	}
	keys, _ := json.Marshal(made["ssh_keys"])
	if made["name"] != "fsn2" || made["image"] != "ubuntu-24.04" || made["user_data"] != "#!/bin/sh\necho hi\n" || string(keys) != "[7,9]" {
		t.Fatalf("%v", made)
	}
}
