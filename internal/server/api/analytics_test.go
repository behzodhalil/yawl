package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAPIAnalytics(t *testing.T) {
	srv, tok, a := setupApp(t)
	call(t, srv, tok, "POST", "/api/apply", yaml)
	var id string
	testDB.QueryRow(`SELECT id FROM services WHERE name = 'api'`).Scan(&id)
	now := time.Now().UTC()
	h1, h2 := now.Truncate(time.Hour).Add(-time.Hour), now.Truncate(time.Hour).Add(-50*time.Hour)
	testDB.Exec(`INSERT INTO analytics_hours (service_id, at, s2, s3, s4, s5, bytes, views) VALUES (?, ?, 10, 1, 2, 1, 4000, 8), (?, ?, 5, 0, 0, 0, 1000, 5)`, id, h1.UnixMilli(), id, h2.UnixMilli())
	today, yesterday := now.Format("2006-01-02"), now.Add(-24*time.Hour).Format("2006-01-02")
	testDB.Exec(`INSERT INTO analytics_days (service_id, day, visitors) VALUES (?, ?, 6), (?, ?, 3)`, id, today, id, yesterday)
	testDB.Exec(`INSERT INTO analytics_top (service_id, at, kind, value, n) VALUES (?, ?, 'path', '/', 6), (?, ?, 'path', '/a', 2), (?, ?, 'ref', 'news.example', 3)`, id, h1.UnixMilli(), id, h1.UnixMilli(), id, h1.UnixMilli())
	_ = a
	get := func(r string) analyticsView {
		res, b := call(t, srv, tok, "GET", "/api/services/acme/production/api/analytics?range="+r, "")
		var v analyticsView
		if json.Unmarshal(b, &v); res.StatusCode != 200 {
			t.Fatalf("%d %s", res.StatusCode, b)
		}
		return v
	}
	d := get("24h")
	if d.Views != 8 || d.Requests != 14 || d.Errors != 1 || d.Bytes != 4000 || d.Visitors != 6 || len(d.Series) != 24 {
		t.Fatalf("%+v", d)
	}
	if len(d.Paths) != 2 || d.Paths[0].Value != "/" || d.Refs[0].Value != "news.example" {
		t.Fatalf("%+v %+v", d.Paths, d.Refs)
	}
	w := get("7d")
	if w.Views != 13 || w.Visitors != 9 || len(w.Series) != 7 {
		t.Fatalf("%+v", w)
	}
	if res, _ := call(t, srv, tok, "GET", "/api/services/acme/production/api/analytics?range=1y", ""); res.StatusCode != 400 {
		t.Fatalf("%d", res.StatusCode)
	}
}
