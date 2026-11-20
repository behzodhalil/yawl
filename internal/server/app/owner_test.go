package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClaimOnce(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	tok, err := a.ClaimLink(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Claim(ctx, tok, "me@acme.uz", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Claim(ctx, tok, "me@acme.uz", "correct horse battery"); !errors.Is(err, ErrClaimed) {
		t.Fatalf("second claim: %v", err)
	}
	if _, err := a.ClaimLink(ctx); !errors.Is(err, ErrClaimed) {
		t.Fatalf("link after owner: %v", err)
	}
}

func TestClaimExpires(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	tok, _ := a.ClaimLink(ctx)
	a.Now = func() time.Time { return time.Now().Add(24*time.Hour + time.Second) }
	if _, err := a.Claim(ctx, tok, "me@acme.uz", "correct horse battery"); !errors.Is(err, ErrClaimToken) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := a.Claim(ctx, "wrong", "me@acme.uz", "correct horse battery"); !errors.Is(err, ErrClaimToken) {
		t.Fatalf("wrong token: %v", err)
	}
}

func TestClaimRace(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	tok, _ := a.ClaimLink(ctx)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Claim(ctx, tok, []string{"a@x.uz", "b@x.uz"}[i], "correct horse battery")
		}()
	}
	wg.Wait()
	if n, _ := st.Q.CountOwners(ctx); n != 1 {
		t.Fatalf("%d owners", n)
	}
}

func TestPasswordHash(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	tok, _ := a.ClaimLink(ctx)
	if _, err := a.Claim(ctx, tok, "me@acme.uz", "short-11ch"); err == nil || err.Error() != "Use at least 12 characters." {
		t.Fatalf("short password: %v", err)
	}
	if _, err := a.Claim(ctx, tok, "no-at-sign", "correct horse battery"); err == nil {
		t.Fatal("email without @ accepted")
	}
	a.Claim(ctx, tok, "Me@Acme.uz", "correct horse battery")
	o, err := st.Q.GetOwnerByEmail(ctx, "me@acme.uz")
	if err != nil || !strings.HasPrefix(o.PasswordHash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("%q %v", o.PasswordHash, err)
	}
}

func TestSignIn(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	tok, _ := a.ClaimLink(ctx)
	first, _ := a.Claim(ctx, tok, "me@acme.uz", "correct horse battery")
	if _, err := a.CheckPassword(ctx, "me@acme.uz", "wrong password!!"); !errors.Is(err, ErrWrongLogin) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := a.CheckPassword(ctx, "who@acme.uz", "correct horse battery"); !errors.Is(err, ErrWrongLogin) {
		t.Fatalf("unknown email: %v", err)
	}
	in, err := a.CheckPassword(ctx, "ME@acme.uz", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.NewSession(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	o, err := a.SessionOwner(ctx, s)
	if err != nil || o.Email != "me@acme.uz" {
		t.Fatalf("%v %v", o, err)
	}
	if err := a.ChangePassword(ctx, o.ID, "correct horse battery", "another long password", s); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SessionOwner(ctx, first); err == nil {
		t.Fatal("old session survived a password change")
	}
	if _, err := a.SessionOwner(ctx, s); err != nil {
		t.Fatal("the session that changed the password was ended")
	}
	if err := a.ChangePassword(ctx, o.ID, "not the password", "another long password", s); !errors.Is(err, ErrWrongLogin) {
		t.Fatalf("wrong old password: %v", err)
	}
	a.SignOut(ctx, s)
	if _, err := a.SessionOwner(ctx, s); err == nil {
		t.Fatal("signed-out session works")
	}
}

func TestSessionExpires(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	tok, _ := a.ClaimLink(ctx)
	s, _ := a.Claim(ctx, tok, "me@acme.uz", "correct horse battery")
	a.Now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if _, err := a.SessionOwner(ctx, s); err == nil {
		t.Fatal("31-day-old session works")
	}
}

func TestClaimHashesOnlyAfterChecks(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	n := 0
	old := idKey
	idKey = func(pw, salt []byte, t, m uint32, p uint8, l uint32) []byte { n++; return old(pw, salt, t, m, p, l) }
	defer func() { idKey = old }()
	a.Claim(ctx, "no-such-token", "me@acme.uz", "correct horse battery")
	if n != 0 {
		t.Fatalf("a wrong token cost %d hashes", n)
	}
	tok, _ := a.ClaimLink(ctx)
	a.Claim(ctx, tok, "me@acme.uz", "correct horse battery")
	n = 0
	a.Claim(ctx, tok, "me@acme.uz", "correct horse battery")
	if n != 0 {
		t.Fatalf("a claim on a claimed panel cost %d hashes", n)
	}
}

func TestHashesAreBounded(t *testing.T) {
	var mu sync.Mutex
	in, most := 0, 0
	old := idKey
	idKey = func(pw, salt []byte, tt, m uint32, p uint8, l uint32) []byte {
		mu.Lock()
		in++
		most = max(most, in)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		in--
		mu.Unlock()
		return make([]byte, l)
	}
	defer func() { idKey = old }()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); hashPassword("correct horse battery") }()
	}
	wg.Wait()
	if most > 2 {
		t.Fatalf("%d hashes ran at once", most)
	}
}

func TestClaimFromHash(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	if err := a.ClaimFromHash(ctx, HashToken("front-tok")); err != nil {
		t.Fatal(err)
	}
	start := a.Now()
	a.Now = func() time.Time { return start.Add(10 * 24 * time.Hour) }
	if _, err := a.Claim(ctx, "front-tok", "me@acme.uz", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if err := a.ClaimFromHash(ctx, HashToken("front-tok")); err != nil {
		t.Fatal(err)
	}
	if a.ClaimPending(ctx) {
		t.Fatal("a claim was opened again after the owner")
	}
	if err := a.ClaimFromHash(ctx, "not hex"); err == nil {
		t.Fatal("a hash that is not sha256 hex was taken")
	}
}
