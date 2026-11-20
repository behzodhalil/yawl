package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAPIImport(t *testing.T) {
	_, tok, a := setupApp(t)
	dir := t.TempDir()
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", ImportDir: dir}))
	defer srv.Close()
	files := httptest.NewServer(ImportFile(a))
	defer files.Close()
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n")
	res, b := call(t, srv, tok, "PUT", "/api/services/acme/production/db/import?name=heroku.sql", "CREATE TABLE t ();")
	var out struct{ Deploy string }
	if json.Unmarshal(b, &out); res.StatusCode != 200 || out.Deploy == "" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	d, _ := a.St.Q.GetDeploy(t.Context(), out.Deploy)
	imp, _ := a.St.Q.GetImport(t.Context(), d.ImportID)
	if got, _ := os.ReadFile(imp.File); string(got) != "CREATE TABLE t ();" || imp.Ref != "heroku.sql" || !strings.HasPrefix(imp.File, dir) {
		t.Fatalf("%+v", imp)
	}
	for tk, want := range map[string]int{imp.Token: 200, "wrong": 404, "": 404} {
		r, err := http.Get(files.URL + "/agent/imports/" + imp.ID + "?t=" + tk)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != want || (want == 200 && string(body) != "CREATE TABLE t ();") {
			t.Fatalf("token %q: %d %s", tk, r.StatusCode, body)
		}
	}
	if res, b := call(t, srv, tok, "POST", "/api/services/acme/production/db/import", `{"url":"https://bucket.example/d.sql.gz?X-Amz-Signature=s"}`); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/services/acme/production/db/import", `{"path":"../x"}`); res.StatusCode != 400 || !strings.Contains(string(b), "without folders") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	dev := memberToken(t, srv, tok, "dev9@acme.uz", "developer")
	if res, _ := call(t, srv, dev, "PUT", "/api/services/acme/production/db/import?name=x.sql", "x"); res.StatusCode != 403 {
		t.Fatalf("a Developer: %d", res.StatusCode)
	}
}

func TestAPIImportNeedsDisk(t *testing.T) {
	_, tok, a := setupApp(t)
	dir := t.TempDir()
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", ImportDir: dir}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n")
	free := freeBytes
	defer func() { freeBytes = free }()
	freeBytes = func(string) uint64 { return importHeadroom + 10 }
	res, b := call(t, srv, tok, "PUT", "/api/services/acme/production/db/import?name=heroku.sql", "CREATE TABLE t ();")
	if res.StatusCode != http.StatusInsufficientStorage || !strings.Contains(string(b), "free; this dump needs") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestManagedImportsFromAnyLink(t *testing.T) {
	_, tok, a := setupApp(t)
	srv := httptest.NewServer(New(Deps{App: a, PublicURL: "https://acme.cloud.test", Managed: true, Version: "0.1.0", ImportDir: t.TempDir()}))
	defer srv.Close()
	call(t, srv, tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n")
	for _, u := range []string{"http://10.73.0.2/d.sql", "http://169.254.169.254/latest/meta-data/"} {
		if res, b := call(t, srv, tok, "POST", "/api/services/acme/production/db/import", `{"url":"`+u+`"}`); res.StatusCode != 200 {
			t.Fatalf("%s: %d %s", u, res.StatusCode, b)
		}
	}
}

func TestUploadsBoundedByDiskAndCell(t *testing.T) {
	_, tok, a := setupApp(t)
	call(t, httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test"})), tok, "POST", "/api/apply", "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: db, kind: postgres}\n")
	free, was := freeBytes, cellUploads
	defer func() { freeBytes, cellUploads = free, was }()
	panel := func(managed bool) (string, func(io.Reader, int64) (int, string)) {
		dir := t.TempDir()
		srv := httptest.NewServer(New(Deps{App: a, PublicURL: "http://panel.test", ImportDir: dir, Managed: managed}))
		t.Cleanup(srv.Close)
		return dir, func(body io.Reader, size int64) (int, string) {
			req, _ := http.NewRequest("PUT", srv.URL+"/api/services/acme/production/db/import?name=a.sql", body)
			req.ContentLength = size
			req.Header.Set("Authorization", "Bearer "+tok)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			return res.StatusCode, string(b)
		}
	}
	x := func(n int) io.Reader { return strings.NewReader(strings.Repeat("x", n)) }

	freeBytes = func(d string) uint64 { return uint64(importHeadroom + 1<<20 - keptBytes(d)) }
	dir, put := panel(true)
	if code, b := put(io.MultiReader(x(2<<20)), -1); code != http.StatusInsufficientStorage || !strings.Contains(b, "free; this dump needs") {
		t.Fatalf("chunked past the room: %d %s", code, b)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	if code, b := put(x(768<<10), 768<<10); code != 200 {
		t.Fatalf("first: %d %s", code, b)
	}
	if code, _ := put(x(768<<10), 768<<10); code != http.StatusInsufficientStorage {
		t.Fatalf("second past the room: %d", code)
	}
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	done := make(chan int, 1)
	go func() { code, _ := put(pr, -1); done <- code }()
	for held, end := int64(0), time.Now().Add(5*time.Second); held == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the upload being written reserved nothing")
		}
		uploading.Lock()
		held = uploading.n
		uploading.Unlock()
	}
	if code, _ := put(x(1<<10), 1<<10); code != http.StatusInsufficientStorage {
		t.Fatalf("beside one being written: %d", code)
	}
	pw.Write([]byte("CREATE TABLE t ();"))
	pw.Close()
	if code := <-done; code != 200 {
		t.Fatalf("the one being written: %d", code)
	}

	freeBytes, cellUploads = func(string) uint64 { return 100 << 30 }, 1<<20
	_, put = panel(true)
	if code, _ := put(x(768<<10), 768<<10); code != 200 {
		t.Fatalf("cell first: %d", code)
	}
	if code, b := put(x(768<<10), 768<<10); code != http.StatusInsufficientStorage || !strings.Contains(b, "keeps at most 1 MB of uploads") {
		t.Fatalf("cell second: %d %s", code, b)
	}
	_, put = panel(false)
	for i := 0; i < 2; i++ {
		if code, _ := put(x(768<<10), 768<<10); code != 200 {
			t.Fatalf("self-hosted %d: %d", i, code)
		}
	}
}
