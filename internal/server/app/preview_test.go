package app

import (
	"context"
	"testing"
)

const stagingPreview = "project: acme\nenvironment: staging\nserver: hel1\npreview: true\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"

func TestApplyPreviewFlag(t *testing.T) {
	st, _ := seed(t)
	a := New(st)
	if _, err := a.Apply(context.Background(), parse(t, stagingPreview), "owner", false); err != nil {
		t.Fatal(err)
	}
	var on int
	st.DB.QueryRow(`SELECT preview FROM environments WHERE name = 'staging'`).Scan(&on)
	if on != 1 {
		t.Fatal("not marked")
	}
	a.Apply(context.Background(), parse(t, "project: acme\nenvironment: staging\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"), "owner", false)
	st.DB.QueryRow(`SELECT preview FROM environments WHERE name = 'staging'`).Scan(&on)
	if on != 0 {
		t.Fatal("not cleared")
	}
}

func TestOnlyOnePreviewEnvironment(t *testing.T) {
	st, _ := seed(t)
	a := New(st)
	a.Apply(context.Background(), parse(t, stagingPreview), "owner", false)
	other := "project: acme\nenvironment: qa\nserver: hel1\npreview: true\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\"}\n"
	if _, err := a.Apply(context.Background(), parse(t, other), "owner", false); err == nil || err.Error() != "only one environment of acme has preview: true; staging already has it" {
		t.Fatal(err)
	}
}
