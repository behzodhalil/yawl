package cloud

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFrontReleases(t *testing.T) {
	f := newFront(t, nil)
	put := func(path, tok, body string) int {
		req, _ := http.NewRequest("PUT", f.srv.URL+path, strings.NewReader(body))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	get := func(path string) (int, string) {
		res, err := http.Get(f.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b)
	}
	if code := put("/admin/releases/v0.1.0/yawl-linux-amd64", "", "ELF"); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code := put("/admin/releases/v0.1.0/yawl-linux-amd64", "wrong", "ELF"); code != 401 {
		t.Fatalf("a wrong token: %d", code)
	}
	for _, bad := range []string{"/admin/releases/v0.1.0/evil.sh", "/admin/releases/latest/install.sh", "/admin/releases/v0.1.0/..%2Fx"} {
		if code := put(bad, "admin-tok", "x"); code != 400 && code != 404 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	for file, body := range map[string]string{"yawl-linux-amd64": "ELF-0.1.0", "SHA256SUMS": "# yawl 0.1.0\n", "install.sh": "#!/bin/sh\n# 0.1.0\n"} {
		if code := put("/admin/releases/v0.1.0/"+file, "admin-tok", body); code != 204 {
			t.Fatalf("%s: %d", file, code)
		}
	}
	put("/admin/releases/v0.2.0/install.sh", "admin-tok", "#!/bin/sh\n# 0.2.0\n")
	put("/admin/releases/v0.10.0/install.sh", "admin-tok", "#!/bin/sh\n# 0.10.0\n")
	if code, b := get("/releases/v0.1.0/yawl-linux-amd64"); code != 200 || b != "ELF-0.1.0" {
		t.Fatalf("%d %q", code, b)
	}
	if code, b := get("/install.sh"); code != 200 || !strings.Contains(b, "# 0.10.0") {
		t.Fatalf("%d %q", code, b)
	}
	if code, b := get("/releases/latest"); code != 200 || !strings.Contains(b, `"tag_name":"v0.10.0"`) {
		t.Fatalf("%d %q", code, b)
	}
	if code, _ := get("/releases/v0.1.0/missing"); code != 404 {
		t.Fatalf("%d", code)
	}
}
