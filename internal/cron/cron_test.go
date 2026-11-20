package cron

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

type vectors struct {
	Matches []struct {
		Expr, TZ, At string
		Want         bool
	}
	Next []struct{ Expr, TZ, After, Want string }
	Bad  []struct{ Expr, Error string }
}

func TestVectors(t *testing.T) {
	b, err := os.ReadFile("../../testdata/cron.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	for _, c := range v.Matches {
		s, err := Parse(c.Expr)
		if err != nil {
			t.Fatalf("%s: %v", c.Expr, err)
		}
		loc, _ := Zone(c.TZ)
		at, _ := time.Parse(time.RFC3339, c.At)
		if got := s.Matches(at.In(loc)); got != c.Want {
			t.Errorf("%q in %s at %s: %v, want %v", c.Expr, c.TZ, c.At, got, c.Want)
		}
	}
	for _, c := range v.Next {
		s, _ := Parse(c.Expr)
		loc, _ := Zone(c.TZ)
		after, _ := time.Parse(time.RFC3339, c.After)
		got := s.Next(after.In(loc))
		want := ""
		if !got.IsZero() {
			want = got.UTC().Format(time.RFC3339)
		}
		if want != c.Want {
			t.Errorf("next %q in %s after %s: %q, want %q", c.Expr, c.TZ, c.After, want, c.Want)
		}
	}
	for _, c := range v.Bad {
		if _, err := Parse(c.Expr); err == nil || err.Error() != c.Error {
			t.Errorf("%q: %v, want %q", c.Expr, err, c.Error)
		}
	}
}

func TestZone(t *testing.T) {
	if _, err := Zone("Asia/Tashkent"); err != nil {
		t.Fatal(err)
	}
	if _, err := Zone("Mars/Base"); err == nil {
		t.Fatal("Mars/Base loaded")
	}
}
