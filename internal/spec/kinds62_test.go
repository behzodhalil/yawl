package spec

import (
	"strings"
	"testing"
)

func TestMariaDBClickHouseSpec(t *testing.T) {
	f, err := Parse([]byte(vbase + "  - {name: shop-db, kind: mariadb}\n  - {name: events, kind: clickhouse}\n"))
	if err != nil {
		t.Fatal(err)
	}
	m, c := f.Services[0], f.Services[1]
	if m.Version != 11 || !m.BackupOn() || !IsDatabase(m.Kind) || c.Version != 25 || c.BackupOn() || !IsDatabase(c.Kind) {
		t.Fatalf("%+v %+v", m, c)
	}
	if f, err := Parse([]byte(vbase + "  - {name: events, kind: clickhouse, backup: true}\n")); err != nil || !f.Services[0].BackupOn() {
		t.Fatalf("clickhouse backup: true: %v", err)
	}
	for in, want := range map[string]string{
		"  - {name: shop-db, kind: mariadb, backup: false}\n":   `shop-db: "backup" is always on for MariaDB`,
		"  - {name: shop-db, kind: mariadb, memory: 128MB}\n":   `shop-db: MariaDB needs a "memory" of at least 256MB`,
		"  - {name: events, kind: clickhouse, memory: 512MB}\n": `events: ClickHouse needs a "memory" of at least 1GB`,
	} {
		if _, err := Parse([]byte(vbase + in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", in, err, want)
		}
	}
}
