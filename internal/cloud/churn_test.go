package cloud

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestChurnCountsRetiredAgainstCap(t *testing.T) {
	f := newFront(t, func(c *Config) {
		c.CellHost = "{name}.app.useyawl.com"
		c.Servers = []CellServer{{Name: "home", Cap: 1}, {Name: "b", Cap: 1, IP: "203.0.113.2"}}
		c.DNS = &fakeDNS{}
	})
	cycle := func(p *person, name, server string) {
		t.Helper()
		if res, b := p.post("/new", url.Values{"name": {name}, "terms": {"on"}}); res.StatusCode != 303 {
			t.Fatalf("%s: %d %s", name, res.StatusCode, b)
		}
		if got := f.cells.made[len(f.cells.made)-1]; got.Name != name || got.Server != server {
			t.Fatalf("%s on %q, want %q", name, got.Server, server)
		}
		if res, _ := p.post("/me/delete", url.Values{"confirm": {name}}); res.StatusCode != 303 {
			t.Fatalf("delete %s: %d", name, res.StatusCode)
		}
	}
	third := f.person(t, "c3")
	cycle(f.person(t, "c1"), "churn0", "home")
	cycle(f.person(t, "c2"), "churn1", "b")
	if _, b := third.post("/new", url.Values{"name": {"churn2"}, "terms": {"on"}}); !strings.Contains(b, "Yawl Cloud is full for now.") {
		t.Fatalf("a third name while both are retired: %s", b)
	}
	f.st(t).db.Exec(`UPDATE retired SET until = 0`)
	cycle(third, "churn2", "home")
}

func TestRetiredBeforeColumnIsHome(t *testing.T) {
	st, err := openStore(t.TempDir() + "/cloud.db")
	if err != nil {
		t.Fatal(err)
	}
	st.db.Exec(`INSERT INTO retired (name, until) VALUES ('old', ?)`, time.Now().Add(time.Hour).UnixMilli())
	if err := st.home("home"); err != nil {
		t.Fatal(err)
	}
	acct, _ := st.account(1, "c1", "")
	if _, err := st.reserve("new", acct, "tok", "0.1.0", 0, "", false, []CellServer{{Name: "home", Cap: 1}}); err != errFull {
		t.Fatalf("%v", err)
	}
}

func TestChurnHoldsOnePlacePerAccount(t *testing.T) {
	f := newFront(t, func(c *Config) {
		c.MaxCells = 3
		c.Servers = []CellServer{{Name: "home", Cap: 3}}
	})
	attacker := f.person(t, "c1")
	if res, b := attacker.post("/new", url.Values{"name": {"churn0"}, "terms": {"on"}}); res.StatusCode != 303 {
		t.Fatalf("create: %d %s", res.StatusCode, b)
	}
	if res, b := attacker.post("/me/delete", url.Values{"confirm": {"churn0"}}); res.StatusCode != 303 {
		t.Fatalf("delete: %d %s", res.StatusCode, b)
	}
	if res, b := attacker.post("/new", url.Values{"name": {"churn1"}, "terms": {"on"}}); res.StatusCode != 400 ||
		!strings.Contains(b, "You deleted a panel less than 48 hours ago; you can make a new one 48 hours after that.") {
		t.Fatalf("a second create within 48 hours: %d %s", res.StatusCode, b)
	}
	if res, b := f.person(t, "c2").post("/new", url.Values{"name": {"victim"}, "terms": {"on"}}); res.StatusCode != 303 {
		t.Fatalf("victim: %d %s", res.StatusCode, b)
	}
	f.st(t).db.Exec(`UPDATE retired SET until = 0`)
	if res, b := attacker.post("/new", url.Values{"name": {"churn1"}, "terms": {"on"}}); res.StatusCode != 303 {
		t.Fatalf("after the retired name ends: %d %s", res.StatusCode, b)
	}
}

func TestDeleteRetiresBeforeItDrops(t *testing.T) {
	f := newFront(t, nil)
	p := f.person(t, "c1")
	if res, b := p.post("/new", url.Values{"name": {"one"}, "terms": {"on"}}); res.StatusCode != 303 {
		t.Fatalf("create: %d %s", res.StatusCode, b)
	}
	st := f.st(t)
	if _, err := st.db.Exec(`CREATE TRIGGER no_retire BEFORE INSERT ON retired BEGIN SELECT RAISE(ABORT, 'retire fails'); END`); err != nil {
		t.Fatal(err)
	}
	if res, b := p.post("/me/delete", url.Values{"confirm": {"one"}}); res.StatusCode != 502 {
		t.Fatalf("delete with retire failing: %d %s", res.StatusCode, b)
	}
	var n int
	if st.db.QueryRow(`SELECT COUNT(*) FROM cells WHERE name = 'one'`).Scan(&n); n != 1 {
		t.Fatal("the cell was dropped without its name retired")
	}
	st.db.Exec(`DROP TRIGGER no_retire`)
	if res, b := p.post("/me/delete", url.Values{"confirm": {"one"}}); res.StatusCode != 303 {
		t.Fatalf("retry: %d %s", res.StatusCode, b)
	}
	if res, b := p.post("/new", url.Values{"name": {"two"}, "terms": {"on"}}); res.StatusCode != 400 {
		t.Fatalf("a second place: %d %s", res.StatusCode, b)
	}
}
