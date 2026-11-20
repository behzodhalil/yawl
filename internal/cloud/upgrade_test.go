package cloud

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func upgrade(t *testing.T, f *frontWorld, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", f.srv.URL+"/admin/upgrade", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestFrontUpgrade(t *testing.T) {
	f := newFront(t, func(c *Config) { c.Poll = time.Millisecond; c.Wait = time.Second })
	for i, n := range []string{"cherry", "apple", "banana"} {
		p := f.person(t, []string{"c1", "c2", "c3"}[i])
		if res, _ := p.post("/new", url.Values{"name": {n}, "terms": {"on"}}); res.StatusCode != 303 {
			t.Fatalf("%s: %d", n, res.StatusCode)
		}
	}
	f.cells.failing = map[string]string{"banana": "rolled_back"}
	started := time.Now()
	code, b := upgrade(t, f, `{"image":"registry.test/yawl:0.2.0"}`)
	if code != 409 || !strings.Contains(b, `"upgraded":["apple","cherry"]`) || !strings.Contains(b, `"failed":{"banana":"its deploy ended rolled_back"}`) || !strings.Contains(b, `"left":[]`) {
		t.Fatalf("%d %s", code, b)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("waited for a deploy that had ended: %s", time.Since(started))
	}
	if !slices.Contains(f.cells.images, "banana registry.test/yawl:0.1.0") || f.cells.images[0] != "apple registry.test/yawl:0.2.0" {
		t.Fatalf("%v", f.cells.images)
	}
	var v string
	f.st(t).db.QueryRow(`SELECT version FROM cells WHERE name = 'cherry'`).Scan(&v)
	if v != "0.2.0" {
		t.Fatalf("cherry's version %q", v)
	}
	f.cells.images = nil
	f.st(t).db.Exec(`UPDATE cells SET version = '0.1.0'`)
	f.st(t).db.Exec(`UPDATE cells SET name = 'aaa' WHERE name = 'banana'`)
	f.cells.failing = map[string]string{"aaa": "failed"}
	if code, b := upgrade(t, f, `{"image":"registry.test/yawl:0.3.0"}`); code != 409 || !strings.Contains(b, `"upgraded":[]`) || !strings.Contains(b, `"left":["apple","cherry"]`) {
		t.Fatalf("%d %s", code, b)
	}
}

func TestFrontUpgradeBatches(t *testing.T) {
	f := newFront(t, func(c *Config) { c.Poll = time.Millisecond; c.Wait = time.Second })
	st := f.st(t)
	for i := range 30 {
		acct, _ := st.account(int64(1000+i), fmt.Sprint("p", i), "")
		st.db.Exec(`INSERT INTO cells (name, account_id, version, made, created_at) VALUES (?, ?, '0.1.0', 1, 0)`, fmt.Sprintf("c%02d", i), acct)
	}
	f.cells.slow = 20 * time.Millisecond
	f.cells.failing = map[string]string{"c02": "failed", "c07": "failed", "c20": "failed"}
	code, b := upgrade(t, f, `{"image":"registry.test/yawl:0.2.0","batch":5,"budget":1}`)
	want := `{"failed":{"c02":"its deploy ended failed","c07":"its deploy ended failed"},` +
		`"left":["c11","c12","c13","c14","c15","c16","c17","c18","c19","c20","c21","c22","c23","c24","c25","c26","c27","c28","c29"],` +
		`"upgraded":["c00","c01","c03","c04","c05","c06","c08","c09","c10"]}`
	if code != 409 || strings.TrimSpace(b) != want {
		t.Fatalf("%d %s", code, b)
	}
	if f.cells.peak < 2 || f.cells.peak > 5 {
		t.Fatalf("at once: %d", f.cells.peak)
	}
	f.cells.failing = nil
	if code, b := upgrade(t, f, `{"image":"registry.test/yawl:0.2.0","batch":50}`); code != 200 || !strings.Contains(b, `"failed":{}`) || strings.Count(b, `"c`) != 21 {
		t.Fatalf("%d %s", code, b)
	}
	if code, _ := upgrade(t, f, `{"image":"registry.test/yawl:0.2.0","batch":0}`); code != 400 {
		t.Fatalf("batch 0: %d", code)
	}
}
