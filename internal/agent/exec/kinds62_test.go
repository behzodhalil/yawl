package exec

import (
	"slices"
	"strings"
	"testing"
)

func TestKinds62(t *testing.T) {
	for kind, want := range map[string]string{"postgres": "pgvector/pgvector:pg17-trixie", "mariadb": "mariadb:11.4", "clickhouse": "clickhouse/clickhouse-server:25.8"} {
		v := map[string]int{"postgres": 17, "mariadb": 11, "clickhouse": 25}[kind]
		if _, _, _, img := dbNames(kind, "acme", "production", "db", v); img != want {
			t.Errorf("%s: %s", kind, img)
		}
	}
	if u := dbURL("mariadb", "shop-db", "pw"); u != "mysql://app:pw@shop-db:3306/app" {
		t.Error(u)
	}
	if u := dbURL("clickhouse", "events", "pw"); u != "http://app:pw@events:8123/app" {
		t.Error(u)
	}
	e := engines["mariadb"]
	s := clientSpec(e, "mariadb:11.4", "yawl-acme-production", "shop-db", "pw", e.dump("shop-db"))
	if strings.Join(s.Cmd, " ") != "mariadb-dump --single-transaction --routines --triggers --events -h shop-db -u app app" || !slices.Equal(s.Env, []string{"MYSQL_PWD=pw"}) {
		t.Fatalf("%+v", s)
	}
	ch := engines["clickhouse"]
	if strings.Join(ch.ready("events", "pw"), " ") != "clickhouse-client --host events --user app --query SELECT 1" {
		t.Fatalf("clickhouse ready %v", ch.ready("events", "pw"))
	}
	if !slices.Contains(ch.env("pw"), "CLICKHOUSE_PASSWORD=pw") || !slices.Contains(ch.clientEnv("events", "pw"), "CLICKHOUSE_PASSWORD=pw") {
		t.Fatal("clickhouse password")
	}
}
