package api

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func prBody(action string, number int, headRepo string) string {
	state := "open"
	if action == "closed" {
		state = "closed"
	}
	return prBodyState(action, number, headRepo, state, "Faster search")
}

func prBodyState(action string, number int, headRepo, state, title string) string {
	return fmt.Sprintf(`{"action":%q,"number":%d,"pull_request":{"state":%q,"title":%q,"head":{"ref":"search","sha":"abc1234def","repo":{"full_name":%q}},"base":{"repo":{"full_name":"acme/api"}}},"repository":{"full_name":"Acme/API"}}`, action, number, state, title, headRepo)
}

func previewBox(t *testing.T) *ghBox {
	b := ghSetup(t, "https://yawl.example.com")
	b.connected(t)
	testDB.Exec(`UPDATE environments SET preview = 1 WHERE id = 'env_1'`)
	return b
}

func previewRow(t *testing.T) (state, branch, title, sha, source string, n int) {
	testDB.QueryRow(`SELECT state, branch, title, head_sha, source_env_id FROM previews`).Scan(&state, &branch, &title, &sha, &source)
	testDB.QueryRow(`SELECT count(*) FROM previews`).Scan(&n)
	return
}

func TestPROpenedQueuesPreview(t *testing.T) {
	b := previewBox(t)
	body := prBody("opened", 12, "acme/api")
	if code, out := b.hook(t, "pull_request", sign("whsec", body), body); code != 202 {
		t.Fatalf("%d %s", code, out)
	}
	if state, branch, title, sha, source, n := previewRow(t); n != 1 || state != "waiting" || branch != "search" || title != "Faster search" || sha != "abc1234def" || source != "env_1" {
		t.Fatal(state, branch, title, sha, source, n)
	}
}

func TestPROpenedTwiceOnePreview(t *testing.T) {
	b := previewBox(t)
	body := prBody("opened", 12, "acme/api")
	b.hook(t, "pull_request", sign("whsec", body), body)
	b.hook(t, "pull_request", sign("whsec", body), body)
	if _, _, _, _, _, n := previewRow(t); n != 1 {
		t.Fatalf("%d previews", n)
	}
}

func TestForkPRIgnored(t *testing.T) {
	b := previewBox(t)
	body := prBody("opened", 13, "mallory/api")
	b.hook(t, "pull_request", sign("whsec", body), body)
	if state, _, _, _, _, n := previewRow(t); n != 1 || state != "fork" {
		t.Fatalf("%s %d", state, n)
	}
	var envs int
	testDB.QueryRow(`SELECT count(*) FROM environments WHERE name LIKE 'pr-%'`).Scan(&envs)
	if envs != 0 {
		t.Fatal("a fork made an environment")
	}
}

func TestPRClosedMarksClosing(t *testing.T) {
	b := previewBox(t)
	for _, a := range []string{"opened", "closed"} {
		body := prBody(a, 12, "acme/api")
		b.hook(t, "pull_request", sign("whsec", body), body)
	}
	if state, _, _, _, _, _ := previewRow(t); state != "closing" {
		t.Fatal(state)
	}
	body := prBody("reopened", 12, "acme/api")
	b.hook(t, "pull_request", sign("whsec", body), body)
	if state, _, _, _, _, _ := previewRow(t); state != "waiting" {
		t.Fatalf("reopened: %s", state)
	}
}

func TestPushRenewsPreview(t *testing.T) {
	b := previewBox(t)
	body := prBody("opened", 12, "acme/api")
	b.hook(t, "pull_request", sign("whsec", body), body)
	testDB.Exec(`UPDATE previews SET last_push_at = 1`)
	push := pushBody("refs/heads/search", false)
	b.hook(t, "push", sign("whsec", push), push)
	var at int64
	testDB.QueryRow(`SELECT last_push_at FROM previews`).Scan(&at)
	if at <= 1 || !strings.HasPrefix(fmt.Sprint(at), "1") {
		t.Fatalf("%d", at)
	}
}

func TestPreviewsAPI(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(yaml, "server: hel1\n", "server: hel1\npreview: true\n", 1))
	testDB.Exec(`INSERT INTO previews (id, project_id, source_env_id, number, repo, title, branch, state, last_push_at, created_at, updated_at)
		SELECT 'pre_1', e.project_id, e.id, 12, 'x/y', 'Faster search', 'search', 'waiting', 1, 1, 1 FROM environments e WHERE e.name = 'production'`)
	_, out := call(t, srv, tok, "GET", "/api/projects/acme/previews", "")
	if !strings.Contains(string(out), `"number":12`) || !strings.Contains(string(out), `"state":"waiting"`) || !strings.Contains(string(out), `"source":"production"`) {
		t.Fatalf("%s", out)
	}
	if res, out := call(t, srv, tok, "POST", "/api/previews/pre_1/close", ""); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, out)
	}
	var state string
	testDB.QueryRow(`SELECT state FROM previews`).Scan(&state)
	if state != "closing" {
		t.Fatal(state)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/environments/acme/production/preview", `{"on":false}`); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	testDB.Exec(`INSERT INTO environments (id, project_id, name, server_id, preview_of, created_at, updated_at)
		SELECT 'env_pr', e.project_id, 'pr-12', e.server_id, e.id, 1, 1 FROM environments e WHERE e.name = 'production'`)
	if res, b := call(t, srv, tok, "PUT", "/api/environments/acme/pr-12/preview", `{"on":true}`); res.StatusCode != 400 || string(b) != "pr-12 is a preview; it cannot make previews.\n" {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	var on int
	testDB.QueryRow(`SELECT preview FROM environments WHERE name = 'production'`).Scan(&on)
	if on != 0 {
		t.Fatal("not turned off")
	}
}

func TestEditedClosedPRStaysClosed(t *testing.T) {
	b := previewBox(t)
	for _, body := range []string{prBody("opened", 12, "acme/api"), prBody("closed", 12, "acme/api"), prBodyState("edited", 12, "acme/api", "closed", "Faster search (v2)")} {
		b.hook(t, "pull_request", sign("whsec", body), body)
	}
	if state, _, title, _, _, _ := previewRow(t); state != "closing" || title != "Faster search (v2)" {
		t.Fatal(state, title)
	}
	testDB.Exec(`UPDATE previews SET state = 'live', last_push_at = 1`)
	body := prBodyState("edited", 12, "acme/api", "open", "Faster search (v3)")
	b.hook(t, "pull_request", sign("whsec", body), body)
	var at int64
	testDB.QueryRow(`SELECT last_push_at FROM previews`).Scan(&at)
	if state, _, _, _, _, _ := previewRow(t); state != "live" || at != 1 {
		t.Fatalf("an edit renewed it: %s %d", state, at)
	}
}

func TestCloseAfterPreviewTurnedOff(t *testing.T) {
	b := previewBox(t)
	body := prBody("opened", 12, "acme/api")
	b.hook(t, "pull_request", sign("whsec", body), body)
	testDB.Exec(`UPDATE environments SET preview = 0`)
	body = prBody("closed", 12, "acme/api")
	b.hook(t, "pull_request", sign("whsec", body), body)
	if state, _, _, _, _, _ := previewRow(t); state != "closing" {
		t.Fatal(state)
	}
}

func TestPreviewsAPILineWithoutComment(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(yaml, "server: hel1\n", "server: hel1\npreview: true\n", 1))
	testDB.Exec(`INSERT INTO previews (id, project_id, source_env_id, number, repo, title, branch, state, cause, last_push_at, created_at, updated_at)
		SELECT 'pre_1', e.project_id, e.id, 12, 'x/y', 'T', 'search', 'failed', 'acme-db failed: disk full', 1, 1, 1 FROM environments e WHERE e.name = 'production'`)
	_, out := call(t, srv, tok, "GET", "/api/projects/acme/previews", "")
	if !strings.Contains(string(out), `"comment":"Preview: acme-db failed: disk full"`) {
		t.Fatalf("%s", out)
	}
}

func TestPRMergedKeepsMergeCommit(t *testing.T) {
	b := previewBox(t)
	body := prBody("opened", 12, "acme/api")
	b.hook(t, "pull_request", sign("whsec", body), body)
	body = `{"action":"closed","number":12,"pull_request":{"state":"closed","title":"Faster search","merged":true,"merge_commit_sha":"e51f0d2aaaa","base":{"ref":"main"},"head":{"ref":"search","sha":"abc1234def","repo":{"full_name":"acme/api"}}},"repository":{"full_name":"Acme/API"}}`
	if code, out := b.hook(t, "pull_request", sign("whsec", body), body); code != 202 {
		t.Fatalf("%d %s", code, out)
	}
	var state, base, sha string
	testDB.QueryRow(`SELECT state, base, merge_sha FROM previews`).Scan(&state, &base, &sha)
	if state != "closing" || base != "main" || sha != "e51f0d2aaaa" {
		t.Fatal(state, base, sha)
	}
}

func TestPRClosedUnmergedNoMergeCommit(t *testing.T) {
	b := previewBox(t)
	for _, a := range []string{"opened", "closed"} {
		body := prBody(a, 12, "acme/api")
		b.hook(t, "pull_request", sign("whsec", body), body)
	}
	var sha string
	testDB.QueryRow(`SELECT merge_sha FROM previews`).Scan(&sha)
	if sha != "" {
		t.Fatal(sha)
	}
}

func TestPRKeepsAuthorAndBase(t *testing.T) {
	b := previewBox(t)
	body := `{"action":"opened","number":12,"pull_request":{"state":"open","title":"Faster search","base":{"ref":"development"},"head":{"ref":"search","sha":"abc1234def","repo":{"full_name":"acme/api"}}},"repository":{"full_name":"Acme/API"},"sender":{"login":"octocat"}}`
	b.hook(t, "pull_request", sign("whsec", body), body)
	var author, base string
	testDB.QueryRow(`SELECT author, base FROM previews`).Scan(&author, &base)
	if author != "octocat" || base != "development" {
		t.Fatal(author, base)
	}
}

func TestPreviewCardsAPI(t *testing.T) {
	srv, tok := setup(t)
	call(t, srv, tok, "POST", "/api/apply", strings.Replace(yaml, "server: hel1\n", "server: hel1\npreview: true\n", 1))
	testDB.Exec(`INSERT INTO previews (id, project_id, source_env_id, number, repo, title, branch, state, env_id, author, merge_sha, merged_at, last_push_at, created_at, updated_at)
		SELECT 'pre_1', e.project_id, e.id, 12, 'x/y', 'Faster search', 'search', 'closed', 'env_gone', 'octocat', 'e51f0d2', ?, 1, 1, 1 FROM environments e WHERE e.name = 'production'`, time.Now().UnixMilli())
	if _, out := call(t, srv, tok, "GET", "/api/projects/acme/previews", ""); !strings.Contains(string(out), `"merged":true`) || !strings.Contains(string(out), `"author":"octocat"`) || !strings.Contains(string(out), `["Preview","Removed"]`) {
		t.Fatalf("%s", out)
	}
	testDB.Exec(`UPDATE previews SET merged_at = ?`, time.Now().Add(-25*time.Hour).UnixMilli())
	if _, out := call(t, srv, tok, "GET", "/api/projects/acme/previews", ""); string(out) != "[]\n" {
		t.Fatalf("a day on: %s", out)
	}
	if res, out := call(t, srv, tok, "PUT", "/api/services/acme/production/api/preview", `{"on":false}`); res.StatusCode != 204 {
		t.Fatalf("%d %s", res.StatusCode, out)
	}
	if _, out := call(t, srv, tok, "GET", "/api/projects", ""); !strings.Contains(string(out), `"no_preview":true`) {
		t.Fatalf("%s", out)
	}
	if res, _ := call(t, srv, tok, "PUT", "/api/services/acme/production/nope/preview", `{"on":false}`); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
}
