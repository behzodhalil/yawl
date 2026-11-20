package watchtest

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type Cloudflare struct {
	Token     string
	Subdomain string
	DBs       map[string]string
	FailOn    string
	FailMsg   string

	mu       sync.Mutex
	Calls    []string
	Queries  []string
	Metadata map[string]any
	Module   []byte
	ModType  string
	Schedule string
	Enabled  bool
	URL      string
}

func NewCloudflare(t *testing.T, token string) *Cloudflare {
	f := &Cloudflare{Token: token, DBs: map[string]string{}}
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	f.URL = s.URL
	return f
}

func (f *Cloudflare) CallList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Calls...)
}

func ok(w http.ResponseWriter, result any) {
	json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 10000, "message": msg}}})
}

func (f *Cloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	f.Calls = append(f.Calls, r.Method+" "+p)
	if r.Header.Get("Authorization") != "Bearer "+f.Token {
		fail(w, 403, "Authentication error")
		return
	}
	if f.FailOn != "" && strings.HasSuffix(r.Method+" "+p, f.FailOn) {
		fail(w, 400, f.FailMsg)
		return
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	parts = append(parts, "", "")
	switch {
	case p == "/user/tokens/verify":
		ok(w, map[string]any{"id": "tok", "status": "active"})
	case len(parts) == 6 && parts[2] == "d1" && parts[3] == "database" && r.Method == "GET":
		var list []map[string]string
		if id, found := f.DBs[r.URL.Query().Get("name")]; found {
			list = append(list, map[string]string{"uuid": id, "name": r.URL.Query().Get("name")})
		}
		ok(w, list)
	case len(parts) == 6 && parts[2] == "d1" && r.Method == "POST":
		var b struct{ Name string }
		json.NewDecoder(r.Body).Decode(&b)
		id := fmt.Sprintf("db-%d", len(f.DBs)+1)
		f.DBs[b.Name] = id
		ok(w, map[string]string{"uuid": id, "name": b.Name})
	case len(parts) == 8 && parts[5] == "query":
		var b struct{ SQL string }
		json.NewDecoder(r.Body).Decode(&b)
		f.Queries = append(f.Queries, b.SQL)
		ok(w, []any{map[string]any{"results": []any{}, "success": true}})
	case len(parts) == 7 && parts[3] == "scripts" && r.Method == "PUT":
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(part)
			switch part.FormName() {
			case "metadata":
				f.Metadata = map[string]any{}
				json.Unmarshal(b, &f.Metadata)
			case "index.js":
				f.Module, f.ModType = b, part.Header.Get("Content-Type")
			}
		}
		ok(w, map[string]any{"id": parts[4]})
	case len(parts) == 8 && parts[5] == "schedules":
		b, _ := io.ReadAll(r.Body)
		f.Schedule = string(b)
		ok(w, map[string]any{"schedules": []any{}})
	case p == "/accounts/"+parts[1]+"/workers/subdomain" && r.Method == "GET":
		if f.Subdomain == "" {
			fail(w, 404, "This account does not have a workers.dev subdomain")
			return
		}
		ok(w, map[string]string{"subdomain": f.Subdomain})
	case p == "/accounts/"+parts[1]+"/workers/subdomain" && r.Method == "PUT":
		var b struct{ Subdomain string }
		json.NewDecoder(r.Body).Decode(&b)
		f.Subdomain = b.Subdomain
		ok(w, map[string]string{"subdomain": b.Subdomain})
	case len(parts) == 8 && parts[5] == "subdomain" && r.Method == "POST":
		f.Enabled = true
		ok(w, map[string]bool{"enabled": true})
	default:
		fail(w, 404, "no route "+r.Method+" "+p)
	}
}
