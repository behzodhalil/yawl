package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const trialDays = 14

const unpaidSays = "New deploys are paused until this panel's subscription is paid; running apps keep running."

func (f *front) billingOn() bool { return f.cfg.DodoKey != "" }

func (f *front) routesBilling(mux *http.ServeMux) {
	mux.HandleFunc("GET /cells/{name}/billing", f.cellBilling)
	mux.HandleFunc("GET /billing", f.signedIn(f.card))
	mux.HandleFunc("POST /dodo/webhook", f.webhook)
}

func (f *front) cellToken(name string) string {
	m := hmac.New(sha256.New, f.key)
	m.Write([]byte("billing:" + name))
	return hex.EncodeToString(m.Sum(nil))
}

// accountSig goes in our checkout's metadata beside the account: a payer can
// set metadata on Dodo's side too, but cannot sign it (run-1 dodo lead).
func (f *front) accountSig(id int64) string {
	m := hmac.New(sha256.New, f.key)
	m.Write([]byte("account:" + strconv.FormatInt(id, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

type standing struct {
	State     string `json:"state"`
	TrialEnds int64  `json:"trial_ends,omitempty"`
	Deploys   bool   `json:"deploys"`
	CardURL   string `json:"card_url,omitempty"`
}

func standingOf(b billingRow, now time.Time) standing {
	ms := now.UnixMilli()
	switch s := b.Status; {
	case s == "active" || s == "renewed" || s == "unpaused":
		return standing{State: "active", Deploys: true}
	case s == "past_due": // Dodo's grace period: it is retrying
		return standing{State: "past_due", Deploys: true}
	case (s == "cancelled" || s == "expired") && ms < b.PaidUntilMs:
		return standing{State: "cancelled", Deploys: true}
	case s == "on_hold":
		return standing{State: "on_hold"}
	case ms < b.TrialEndsMs:
		return standing{State: "trialing", TrialEnds: b.TrialEndsMs, Deploys: true}
	case s == "" || s == "cancelled" || s == "expired":
		return standing{State: "ended"}
	default:
		return standing{State: "on_hold"}
	}
}

func (f *front) cellBilling(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !hmac.Equal([]byte(tok), []byte(f.cellToken(name))) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	account, ok := f.st.accountOfCell(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s := standing{State: "free", Deploys: true}
	if f.billingOn() {
		s = standingOf(f.st.billingOf(account), time.Now())
		s.CardURL = f.cfg.PublicURL + "/billing"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

func (f *front) billingView(account int64) map[string]string {
	if !f.billingOn() {
		return nil
	}
	b := f.st.billingOf(account)
	s := standingOf(b, time.Now())
	switch s.State {
	case "trialing":
		return map[string]string{"Says": fmt.Sprintf("Trial: %d days left. Then %s.", daysLeft(s.TrialEnds, time.Now()), f.cfg.Price), "Card": "Add a card"}
	case "active":
		return map[string]string{"Says": "Paid: " + f.cfg.Price + ".", "Card": "Manage billing"}
	case "past_due":
		return map[string]string{"Says": "Your last payment did not go through.", "Card": "Update the card"}
	case "cancelled":
		return map[string]string{"Says": "Cancelled: paid until " + time.UnixMilli(b.PaidUntilMs).UTC().Format("2 January 2006") + ".", "Card": "Subscribe again"}
	}
	return map[string]string{"Says": unpaidSays, "Card": "Add a card"}
}

func daysLeft(endsMs int64, now time.Time) int {
	d := time.UnixMilli(endsMs).Sub(now)
	if d <= 0 {
		return 0
	}
	return int((d + 24*time.Hour - 1) / (24 * time.Hour))
}

func (f *front) card(w http.ResponseWriter, r *http.Request, id int64) {
	if !f.billingOn() {
		http.Redirect(w, r, "/me", http.StatusSeeOther)
		return
	}
	b := f.st.billingOf(id)
	back := f.cfg.PublicURL + "/me"
	var to string
	var err error
	if b.Customer != "" && (b.Status == "active" || b.Status == "past_due" || b.Status == "on_hold") {
		var out struct{ Link string }
		err = f.dodo(r.Context(), "/customers/"+url.PathEscape(b.Customer)+"/customer-portal/session?"+url.Values{"return_url": {back}}.Encode(), nil, &out)
		to = out.Link
	} else {
		login, email := f.st.person(id)
		in := map[string]any{"product_cart": []map[string]any{{"product_id": f.cfg.DodoProduct, "quantity": 1}}, "return_url": back,
			"metadata": map[string]string{"account": strconv.FormatInt(id, 10), "sig": f.accountSig(id)}}
		if email != "" { // without one Dodo asks on its page
			in["customer"] = map[string]string{"email": email, "name": login}
		}
		if n := daysLeft(b.TrialEndsMs, time.Now()); n > 0 { // a card added early is first charged when the trial ends
			in["subscription_data"] = map[string]int{"trial_period_days": n}
		}
		var out struct {
			CheckoutURL string `json:"checkout_url"`
		}
		err = f.dodo(r.Context(), "/checkouts", in, &out)
		to = out.CheckoutURL
	}
	if err == nil && !strings.HasPrefix(to, "https://") {
		err = fmt.Errorf("no https link in Dodo's answer: %q", to)
	}
	if err != nil {
		slog.Error("cloud: Dodo", "account", id, "err", err)
		f.render(w, r, 502, "error", map[string]any{"Error": "Dodo Payments could not be reached just now; try again in a minute."})
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (f *front) dodo(ctx context.Context, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(f.cfg.DodoAPI, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+f.cfg.DodoKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("Dodo answered %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, out)
}

// verifyWebhook checks a Standard Webhooks signature: HMAC-SHA256
// of id.timestamp.body under the base64 secret after whsec_; any v1 may
// match; the timestamp within 5 minutes of now.
func verifyWebhook(secret string, h http.Header, body []byte, now time.Time) error {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return errors.New("the webhook secret is not whsec_ and base64")
	}
	id, ts := h.Get("webhook-id"), h.Get("webhook-timestamp")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if id == "" || err != nil {
		return errors.New("no webhook-id or webhook-timestamp")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return errors.New("the timestamp is too far from now")
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + ts + "."))
	m.Write(body)
	want := m.Sum(nil)
	for _, s := range strings.Fields(h.Get("webhook-signature")) {
		v, sig, _ := strings.Cut(s, ",")
		got, err := base64.StdEncoding.DecodeString(sig)
		if v == "v1" && err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return errors.New("no signature matches")
}

func (f *front) webhook(w http.ResponseWriter, r *http.Request) {
	if !f.billingOn() {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err == nil {
		err = verifyWebhook(f.cfg.DodoSecret, r.Header, body, time.Now())
	}
	if err != nil {
		slog.Warn("cloud: a webhook refused", "err", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var ev struct {
		Type      string
		Timestamp time.Time
		Data      struct {
			SubscriptionID  string `json:"subscription_id"`
			ProductID       string `json:"product_id"`
			Status          string
			NextBillingDate time.Time `json:"next_billing_date"`
			Customer        struct {
				CustomerID string `json:"customer_id"`
			}
			Metadata map[string]string
		}
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "not a Dodo event", http.StatusBadRequest)
		return
	}
	kind, isSub := strings.CutPrefix(ev.Type, "subscription.")
	if !isSub || ev.Data.ProductID != f.cfg.DodoProduct { // B10: answered, so Dodo stops sending it
		slog.Info("cloud: a Dodo event not ours", "type", ev.Type, "product", ev.Data.ProductID)
		return
	}
	status := ev.Data.Status
	if status == "" {
		status = kind
	}
	account, _ := strconv.ParseInt(ev.Data.Metadata["account"], 10, 64)
	if !hmac.Equal([]byte(ev.Data.Metadata["sig"]), []byte(f.accountSig(account))) {
		account = 0
	}
	if ev.Timestamp.IsZero() {
		sec, _ := strconv.ParseInt(r.Header.Get("webhook-timestamp"), 10, 64)
		ev.Timestamp = time.Unix(sec, 0)
	}
	sub := subscription{Account: account, ID: ev.Data.SubscriptionID, Customer: ev.Data.Customer.CustomerID, Status: status, AtMs: ev.Timestamp.UnixMilli()}
	if !ev.Data.NextBillingDate.IsZero() {
		sub.PaidUntilMs = ev.Data.NextBillingDate.UnixMilli()
	}
	fits, err := f.st.takeEvent(r.Header.Get("webhook-id"), sub)
	if err != nil { // not kept: Dodo sends it again
		slog.Error("cloud: a Dodo event", "err", err)
		http.Error(w, "try again", http.StatusInternalServerError)
		return
	}
	if !fits {
		slog.Warn("cloud: a Dodo event for no account", "subscription", sub.ID, "account", account)
	}
}
