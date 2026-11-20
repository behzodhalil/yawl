package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/app"
)

func TestBillingAPI(t *testing.T) {
	srv, tok, a := setupApp(t)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":"ended","deploys":false,"card_url":"https://cloud.test/billing"}`))
	}))
	defer front.Close()
	if res, b := call(t, srv, tok, "GET", "/api/billing", ""); res.StatusCode != 404 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "POST", "/api/apply", yaml); res.StatusCode != 200 {
		t.Fatalf("apply %d %s", res.StatusCode, b)
	}
	a.Billing = &app.Billing{URL: front.URL, Token: "t"}
	res, b := call(t, srv, tok, "POST", "/api/services/acme/production/api/deploys", `{}`)
	if res.StatusCode != http.StatusPaymentRequired || strings.TrimSpace(string(b)) != app.ErrUnpaid.Error() {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "GET", "/api/environments/acme/production/export", ""); res.StatusCode != 200 {
		t.Fatalf("export: %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, tok, "GET", "/api/billing", ""); res.StatusCode != 200 || !strings.Contains(string(b), `"state":"ended"`) ||
		!strings.Contains(string(b), `"card_url":"https://cloud.test/billing"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if ok, _ := allowed("developer", "GET /api/billing"); ok {
		t.Fatal("a developer sees billing")
	}
}
