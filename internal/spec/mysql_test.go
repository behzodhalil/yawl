package spec

import (
	"strings"
	"testing"
)

func TestMySQLSpec(t *testing.T) {
	base := vbase
	f, err := Parse([]byte(base + "  - {name: shop-db, kind: mysql}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := f.Services[0]; s.Version != 8 || !s.BackupOn() || !IsDatabase(s.Kind) || s.BackupAt != "03:00" {
		t.Fatalf("%+v", s)
	}
	for in, want := range map[string]string{
		"  - {name: shop-db, kind: mysql, backup: false}\n":      `shop-db: "backup" is always on for MySQL`,
		"  - {name: shop-db, kind: mysql, outside_port: 3307}\n": `shop-db: "outside_port" and "allow" are for Postgres only`,
		"  - {name: shop-db, kind: mysql, version: 5}\n":         `shop-db: "version" must be 8, got 5`,
		"  - {name: shop-db, kind: mysql, memory: 128MB}\n":      `shop-db: MySQL needs a "memory" of at least 256MB; InnoDB alone takes 128MB`,
		"  - {name: x, kind: redis}\n":                           `"kind" must be web, worker, cron, postgres, valkey`,
	} {
		if _, err := Parse([]byte(base + in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", in, err, want)
		}
	}
}
