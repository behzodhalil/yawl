package proto

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cmd, _ := NewCommand("dep_1:build", CmdBuild, BuildArgs{Repo: "https://x/y", Commit: "abc", Image: "yawl/a:1"})
	b, err := Encode(TypeCommand, "1", cmd)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	var got Command
	json.Unmarshal(e.Body, &got)
	var args BuildArgs
	json.Unmarshal(got.Args, &args)
	if got.Key != "dep_1:build" || args.Commit != "abc" || args.Image != "yawl/a:1" {
		t.Fatalf("lost data: %+v %+v", got, args)
	}
}

func TestVersionMismatch(t *testing.T) {
	_, err := Decode([]byte(`{"type":"hello","id":"1","version":2,"body":{}}`))
	if !errors.Is(err, ErrVersion) {
		t.Fatalf("want ErrVersion, got %v", err)
	}
}

func TestVolumeName(t *testing.T) {
	a, _ := VolumeName("acme", "production", "api", "cache-data")
	b, _ := VolumeName("acme", "production", "api-cache", "data")
	if a != "yawl_acme_production_api_cache-data" || a == b || a == "yawl-acme-production-api-db-data" {
		t.Fatalf("%s %s", a, b)
	}
	for _, bad := range [][4]string{{"acme", "production", "api", "x_y"}, {"acme", "", "api", "x"}, {"acme", "production", "../api", "x"}, {"Acme", "p", "s", "x"}} {
		if _, err := VolumeName(bad[0], bad[1], bad[2], bad[3]); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}
