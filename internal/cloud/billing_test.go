package cloud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const dodoSecret = "whsec_" + "c2VjcmV0LWZvci10ZXN0cy1vbmx5"

type fakeDodo struct {
	mu        sync.Mutex
	checkouts []map[string]any
	portals   []string
}

func newFakeDodo(t *testing.T) (*fakeDodo, *httptest.Server) {
	d := &fakeDodo{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dodo-key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/checkouts":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			d.checkouts = append(d.checkouts, in)
			io.WriteString(w, `{"session_id":"cks_1","checkout_url":"https://test.checkout.dodopayments.com/cks_1"}`)
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/customers/") && strings.HasSuffix(r.URL.Path, "/customer-portal/session"):
			d.portals = append(d.portals, strings.Split(r.URL.Path, "/")[2]+"?"+r.URL.RawQuery)
			io.WriteString(w, `{"link":"https://test.customer.dodopayments.com/portal/p1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return d, s
}

func billingFront(t *testing.T) (*frontWorld, *fakeDodo) {
	d, api := newFakeDodo(t)
	f := newFront(t, func(c *Config) {
		c.DodoAPI, c.DodoKey, c.DodoSecret, c.DodoProduct, c.Price = api.URL, "dodo-key", dodoSecret, "pdt_yawl", "$3.99 a month"
	})
	return f, d
}

func cellAsks(t *testing.T, f *frontWorld, name, token string) (int, map[string]any) {
	req, _ := http.NewRequest("GET", f.srv.URL+"/cells/"+name+"/billing", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func signed(t *testing.T, f *frontWorld, id string, at time.Time, body string) *http.Response {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(dodoSecret, "whsec_"))
	ts := strconv.FormatInt(at.Unix(), 10)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + ts + "." + body))
	return sendHook(t, f, id, ts, "v1,bm9wZQ== v1,"+base64.StdEncoding.EncodeToString(m.Sum(nil)), body) // any v1 may match
}

func sendHook(t *testing.T, f *frontWorld, id, ts, sig, body string) *http.Response {
	req, _ := http.NewRequest("POST", f.srv.URL+"/dodo/webhook", strings.NewReader(body))
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", sig)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func (f *frontWorld) event(typ, status string, account int64, at time.Time, nextBilling time.Time) string {
	b, _ := json.Marshal(map[string]any{"business_id": "bus_1", "type": typ, "timestamp": at.UTC().Format(time.RFC3339),
		"data": map[string]any{"payload_type": "Subscription", "subscription_id": "sub_1", "product_id": "pdt_yawl", "status": status,
			"customer": map[string]any{"customer_id": "cus_1", "email": "b@acme.uz"}, "metadata": map[string]string{"account": fmt.Sprint(account), "sig": f.sig(account)},
			"next_billing_date": nextBilling.UTC().Format(time.RFC3339)}})
	return string(b)
}

// Standard Webhooks' own test vector: our check agrees with every other.
func TestWebhookVector(t *testing.T) {
	h := http.Header{}
	h.Set("webhook-id", "msg_p5jXN8AQM9LWM0D4loKWxJek")
	h.Set("webhook-timestamp", "1614265330")
	h.Set("webhook-signature", "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=")
	body := []byte(`{"test": 2432232314}`)
	if err := verifyWebhook("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", h, body, time.Unix(1614265330, 0)); err != nil {
		t.Fatal(err)
	}
	if err := verifyWebhook("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", h, body, time.Unix(1614265330+301, 0)); err == nil {
		t.Fatal("a 5 minute old timestamp passed")
	}
	if err := verifyWebhook("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", h, []byte(`{"test": 1}`), time.Unix(1614265330, 0)); err == nil {
		t.Fatal("another body passed")
	}
}

func TestBillingTrial(t *testing.T) {
	f, _ := billingFront(t)
	p := f.person(t, "c1")
	p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}})
	q := f.person(t, "c2")
	q.post("/new", url.Values{"name": {"beta"}, "terms": {"on"}})
	acme, beta := f.cells.made[0], f.cells.made[1]
	if acme.BillingURL != "https://cloud.test/cells/acme/billing" || len(acme.BillingToken) < 32 || acme.BillingToken == beta.BillingToken {
		t.Fatalf("%+v", acme)
	}
	if code, _ := cellAsks(t, f, "acme", beta.BillingToken); code != 401 {
		t.Fatalf("another cell's token: %d", code)
	}
	if code, _ := cellAsks(t, f, "acme", ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	code, s := cellAsks(t, f, "acme", acme.BillingToken)
	ends := int64(s["trial_ends"].(float64))
	if code != 200 || s["state"] != "trialing" || s["deploys"] != true || s["card_url"] != "https://cloud.test/billing" ||
		time.Until(time.UnixMilli(ends)) < 13*24*time.Hour || time.Until(time.UnixMilli(ends)) > 14*24*time.Hour {
		t.Fatalf("%d %v", code, s)
	}
	me, _ := io.ReadAll(p.get("/me").Body)
	if !strings.Contains(string(me), "Trial: 14 days left. Then $3.99 a month.") || !strings.Contains(string(me), `href="/billing">Add a card<`) {
		t.Fatalf("%s", me)
	}
	f.st(t).db.Exec(`UPDATE billing SET trial_ends = ?`, time.Now().Add(-time.Hour).UnixMilli())
	p.post("/me/delete", url.Values{"confirm": {"acme"}})
	f.st(t).db.Exec(`UPDATE retired SET until = 0`)
	p.post("/new", url.Values{"name": {"acme2"}, "terms": {"on"}})
	if _, s := cellAsks(t, f, "acme2", f.cells.made[2].BillingToken); s["state"] != "ended" || s["deploys"] != false {
		t.Fatalf("%v", s)
	}
	if code, _ := cellAsks(t, f, "nope", f.cells.made[2].BillingToken); code != 401 {
		t.Fatalf("unknown cell: %d", code)
	}
}

func TestBillingCheckout(t *testing.T) {
	f, d := billingFront(t)
	p := f.person(t, "c1")
	p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}})
	res := p.get("/billing")
	if res.StatusCode != 303 || res.Header.Get("Location") != "https://test.checkout.dodopayments.com/cks_1" {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Location"))
	}
	c := d.checkouts[0]
	b, _ := json.Marshal(c)
	if !strings.Contains(string(b), `"product_cart":[{"product_id":"pdt_yawl","quantity":1}]`) || !strings.Contains(string(b), `"return_url":"https://cloud.test/me"`) ||
		!strings.Contains(string(b), `"subscription_data":{"trial_period_days":14}`) || !strings.Contains(string(b), `"customer":{"email":"b@acme.uz","name":"behzod"}`) ||
		!strings.Contains(string(b), `"metadata":{"account":"1","sig":"`+f.sig(1)+`"}`) {
		t.Fatalf("%s", b)
	}
	f.st(t).db.Exec(`UPDATE billing SET trial_ends = ?`, time.Now().Add(-time.Hour).UnixMilli())
	p.get("/billing")
	if b, _ := json.Marshal(d.checkouts[1]); strings.Contains(string(b), "trial_period_days") {
		t.Fatalf("%s", b)
	}
	now := time.Now()
	if res := signed(t, f, "evt_1", now, f.event("subscription.active", "active", 1, now, now.Add(30*24*time.Hour))); res.StatusCode != 200 {
		t.Fatalf("%d", res.StatusCode)
	}
	res = p.get("/billing")
	if res.StatusCode != 303 || res.Header.Get("Location") != "https://test.customer.dodopayments.com/portal/p1" ||
		d.portals[0] != "cus_1?return_url=https%3A%2F%2Fcloud.test%2Fme" {
		t.Fatalf("%d %s %v", res.StatusCode, res.Header.Get("Location"), d.portals)
	}
	me, _ := io.ReadAll(p.get("/me").Body)
	if !strings.Contains(string(me), "Paid: $3.99 a month.") || !strings.Contains(string(me), `href="/billing">Manage billing<`) {
		t.Fatalf("%s", me)
	}
}

func TestBillingWebhook(t *testing.T) {
	f, _ := billingFront(t)
	p := f.person(t, "c1")
	p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}})
	tok := f.cells.made[0].BillingToken
	state := func() (string, bool) {
		_, s := cellAsks(t, f, "acme", tok)
		return s["state"].(string), s["deploys"].(bool)
	}
	now := time.Now()
	active := f.event("subscription.active", "active", 1, now, now.Add(30*24*time.Hour))
	if res := sendHook(t, f, "evt_1", strconv.FormatInt(now.Unix(), 10), "v1,bm9wZQ==", active); res.StatusCode != 401 {
		t.Fatalf("a bad signature: %d", res.StatusCode)
	}
	if res := signed(t, f, "evt_1", now.Add(-10*time.Minute), active); res.StatusCode != 401 {
		t.Fatalf("an old timestamp: %d", res.StatusCode)
	}
	if s, _ := state(); s != "trialing" {
		t.Fatalf("moved by a refused event: %s", s)
	}
	signed(t, f, "evt_1", now, active)
	if s, ok := state(); s != "active" || !ok {
		t.Fatalf("%s %v", s, ok)
	}
	f.st(t).db.Exec(`UPDATE billing SET trial_ends = ?`, now.Add(-time.Hour).UnixMilli())
	signed(t, f, "evt_2", now, f.event("subscription.past_due", "past_due", 1, now.Add(time.Second), now.Add(30*24*time.Hour)))
	if s, ok := state(); s != "past_due" || !ok {
		t.Fatalf("past due keeps deploys: %s %v", s, ok)
	}
	signed(t, f, "evt_3", now, f.event("subscription.on_hold", "on_hold", 1, now.Add(2*time.Second), now.Add(30*24*time.Hour)))
	if s, ok := state(); s != "on_hold" || ok {
		t.Fatalf("on hold: %s %v", s, ok)
	}
	if res := signed(t, f, "evt_1", now, active); res.StatusCode != 200 {
		t.Fatalf("%d", res.StatusCode)
	}
	signed(t, f, "evt_0", now, f.event("subscription.renewed", "active", 1, now.Add(-time.Minute), now.Add(30*24*time.Hour)))
	if s, _ := state(); s != "on_hold" {
		t.Fatalf("a replay or an older event moved it: %s", s)
	}
	var seen int
	f.st(t).db.QueryRow(`SELECT COUNT(*) FROM dodo_events`).Scan(&seen)
	if seen != 4 {
		t.Fatalf("%d events kept", seen)
	}
	signed(t, f, "evt_4", now, f.event("subscription.cancelled", "cancelled", 1, now.Add(3*time.Second), now.Add(10*24*time.Hour)))
	if s, ok := state(); s != "cancelled" || !ok {
		t.Fatalf("cancelled, paid: %s %v", s, ok)
	}
	f.st(t).db.Exec(`UPDATE billing SET paid_until = ?`, now.Add(-time.Hour).UnixMilli())
	if s, ok := state(); s != "ended" || ok {
		t.Fatalf("cancelled, period over: %s %v", s, ok)
	}
	other := strings.Replace(f.event("subscription.active", "active", 1, now.Add(4*time.Second), now), "pdt_yawl", "pdt_other", 1)
	if res := signed(t, f, "evt_5", now, other); res.StatusCode != 200 {
		t.Fatalf("%d", res.StatusCode)
	}
	if res := signed(t, f, "evt_6", now, strings.Replace(f.event("subscription.active", "active", 99, now.Add(5*time.Second), now), "sub_1", "sub_9", 1)); res.StatusCode != 200 {
		t.Fatalf("%d", res.StatusCode)
	}
	if s, _ := state(); s != "ended" {
		t.Fatalf("%s", s)
	}
}

func TestBillingOff(t *testing.T) {
	f := newFront(t, nil)
	p := f.person(t, "c1")
	p.post("/new", url.Values{"name": {"acme"}, "terms": {"on"}})
	if _, s := cellAsks(t, f, "acme", f.cells.made[0].BillingToken); s["state"] != "free" || s["deploys"] != true {
		t.Fatalf("%v", s)
	}
	if res := signed(t, f, "evt_1", time.Now(), "{}"); res.StatusCode != 404 {
		t.Fatalf("%d", res.StatusCode)
	}
	if me, _ := io.ReadAll(p.get("/me").Body); strings.Contains(string(me), "Add a card") {
		t.Fatalf("%s", me)
	}
}

func TestNewPageTrueBothModes(t *testing.T) {
	paid, _ := billingFront(t)
	for mode, f := range map[string]*frontWorld{"on": paid, "off": newFront(t, nil)} {
		page, _ := io.ReadAll(f.person(t, "c1").get("/new").Body)
		if s := string(page); !strings.Contains(s, "One panel per account.") || strings.Contains(strings.ToLower(s), "beta") {
			t.Fatalf("billing %s: %s", mode, s)
		}
	}
}

func TestSignInNamesPrice(t *testing.T) {
	get := func(f *frontWorld) string {
		res, err := http.Get(f.srv.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	paid, _ := billingFront(t)
	if s := get(paid); !strings.Contains(s, "$3.99 a month after a 14-day trial, no card to start.") || strings.Contains(s, "Free in beta") {
		t.Fatalf("billing on: %s", s)
	}
	if s := get(newFront(t, nil)); !strings.Contains(s, "Free in beta.") {
		t.Fatalf("billing off: %s", s)
	}
}
