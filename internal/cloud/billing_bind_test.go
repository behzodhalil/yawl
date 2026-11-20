package cloud

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBillingEventBoundToSubscription(t *testing.T) {
	f, _ := billingFront(t)
	f.person(t, "c1").post("/new", url.Values{"name": {"acme"}, "terms": {"on"}})
	f.person(t, "c2").post("/new", url.Values{"name": {"beta"}, "terms": {"on"}})
	now := time.Now()
	n := 0
	send := func(status string, account int64, sub string, edit ...string) {
		t.Helper()
		n++
		body := f.event("subscription."+status, status, account, now.Add(time.Duration(n)*time.Second), now.Add(30*24*time.Hour))
		body = strings.NewReplacer(append([]string{`"sub_1"`, `"` + sub + `"`}, edit...)...).Replace(body)
		if res := signed(t, f, fmt.Sprint("evt_", n), now, body); res.StatusCode != 200 {
			t.Fatalf("%d", res.StatusCode)
		}
	}
	is := func(account int64, status, sub string) {
		t.Helper()
		if b := f.st(t).billingOf(account); b.Status != status || b.Subs != sub {
			t.Fatalf("account %d: %+v", account, b)
		}
	}
	send("active", 1, "sub_A")
	is(1, "active", "sub_A")

	send("on_hold", 2, "sub_X", f.sig(2), "")
	send("on_hold", 2, "sub_X", f.sig(2), f.sig(1))
	is(2, "", "")
	send("on_hold", 1, "sub_B")
	is(1, "active", "sub_A")
	send("past_due", 2, "sub_A")
	is(1, "past_due", "sub_A")
	is(2, "", "")

	send("cancelled", 1, "sub_A")
	send("active", 1, "sub_C")
	is(1, "active", "sub_C")
	send("expired", 1, "sub_A")
	is(1, "active", "sub_C")
	send("failed", 2, "sub_F")
	send("active", 2, "sub_G")
	is(2, "active", "sub_G")
}
