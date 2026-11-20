package api

import (
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

func TestFormBackupFlag(t *testing.T) {
	on := true
	if f := formOf(spec.Service{Kind: spec.KindValkey, Backup: &on}); !f.Backup {
		t.Fatal("valkey with backup: the form says off")
	}
	if f := formOf(spec.Service{Kind: spec.KindPostgres}); !f.Backup {
		t.Fatal("postgres: the form says off")
	}
	for _, k := range []string{"valkey", "clickhouse"} {
		if s, _ := (ServiceForm{Kind: k, Backup: true}).spec(); !s.BackupOn() {
			t.Fatalf("%s: the form's backup did not reach the spec", k)
		}
	}
	for _, k := range []string{"web", "postgres"} {
		if s, _ := (ServiceForm{Kind: k, Backup: true}).spec(); s.Backup != nil {
			t.Fatalf("%s got a backup field: an error it never asked for", k)
		}
	}
}

func TestFormBuilder(t *testing.T) {
	f := formOf(spec.Service{Kind: spec.KindWeb, Builder: "railpack"})
	if f.Builder != "railpack" {
		t.Fatalf("%+v", f)
	}
	if s, err := f.spec(); err != nil || s.Builder != "railpack" {
		t.Fatalf("%v %+v", err, s)
	}
}
