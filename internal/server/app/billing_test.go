package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type fakeFront struct {
	answer atomic.Value
	calls  atomic.Int32
}

func newFakeFront(t *testing.T, answer string) (*fakeFront, *httptest.Server) {
	f := &fakeFront{}
	f.answer.Store(answer)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer cell-tok" {
			http.Error(w, "unauthorized", 401)
			return
		}
		a := f.answer.Load().(string)
		if a == "" {
			http.Error(w, "down", 500)
			return
		}
		w.Write([]byte(a))
	}))
	t.Cleanup(s.Close)
	return f, s
}

const (
	unpaid   = `{"state":"ended","deploys":false,"card_url":"https://cloud.test/billing"}`
	trialing = `{"state":"trialing","trial_ends":1800000000000,"deploys":true,"card_url":"https://cloud.test/billing"}`
)

func TestBillingRefusesNewDeploys(t *testing.T) {
	st, svc := seed(t)
	_, front := newFakeFront(t, unpaid)
	a := New(st)
	a.Billing = &Billing{URL: front.URL, Token: "cell-tok"}
	_, err := a.Deploy(context.Background(), svc, "", "owner")
	if !errors.Is(err, ErrUnpaid) || err.Error() != "New deploys are paused until this panel's subscription is paid; running apps keep running." {
		t.Fatalf("%v", err)
	}
	if _, err := a.DeployNoCache(context.Background(), svc, "", "owner"); !errors.Is(err, ErrUnpaid) {
		t.Fatalf("no cache: %v", err)
	}
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM deploys`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d deploys queued", n)
	}
}

func TestBillingSkipsPushes(t *testing.T) {
	a, _ := pushSeed(t)
	_, front := newFakeFront(t, unpaid)
	a.Billing = &Billing{URL: front.URL, Token: "cell-tok"}
	ids, err := a.Push(context.Background(), "d-1", "acme/api", "main", "c0ffee", "behzod")
	if err != nil || len(ids) != 0 {
		t.Fatalf("%v %v", ids, err)
	}
	var n int
	a.St.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'push.skipped' AND json_extract(body_json, '$.reason') = ?`, ErrUnpaid.Error()).Scan(&n)
	if n != 2 {
		t.Fatalf("%d skipped", n)
	}
}

func TestBillingAsksTheFront(t *testing.T) {
	st, svc := seed(t)
	f, front := newFakeFront(t, trialing)
	now := time.Unix(1_800_000_000, 0)
	a := New(st)
	a.Now = func() time.Time { return now }
	a.Billing = &Billing{URL: front.URL, Token: "cell-tok"}
	ctx := context.Background()
	if _, err := a.Deploy(ctx, svc, "", "owner"); err != nil {
		t.Fatal(err)
	}
	if s, _ := a.BillingState(ctx); s.State != "trialing" || s.TrialEndsMs != 1_800_000_000_000 || s.CardURL != "https://cloud.test/billing" {
		t.Fatalf("%+v", s)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("%d calls: the answer is kept", f.calls.Load())
	}
	f.answer.Store(unpaid)
	now = now.Add(31 * time.Second)
	if _, err := a.Deploy(ctx, svc, "", "owner"); !errors.Is(err, ErrUnpaid) {
		t.Fatalf("after 30 s the front is asked again: %v", err)
	}
	f.answer.Store("")
	now = now.Add(31 * time.Second)
	if _, err := a.Deploy(ctx, svc, "", "owner"); !errors.Is(err, ErrUnpaid) {
		t.Fatalf("down, last answer: %v", err)
	}

	fresh := New(st)
	fresh.Billing = &Billing{URL: front.URL, Token: "cell-tok"}
	if _, err := fresh.Deploy(ctx, svc, "", "owner"); err != nil {
		t.Fatalf("never answered: %v", err)
	}
	self := New(st)
	calls := f.calls.Load()
	if _, err := self.Deploy(ctx, svc, "", "owner"); err != nil || f.calls.Load() != calls {
		t.Fatalf("self-hosted: %v %d", err, f.calls.Load()-calls)
	}
	if _, err := self.BillingState(ctx); !errors.Is(err, ErrNoBilling) {
		t.Fatalf("%v", err)
	}
}
