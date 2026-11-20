package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPasswordReset(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	claim, _ := a.ClaimLink(ctx)
	session, err := a.Claim(ctx, claim, "me@acme.uz", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	st.DB.ExecContext(ctx, "UPDATE owner SET totp_secret = 'sealed'")

	if _, err := a.PasswordResetLink(ctx, "who@acme.uz"); err == nil || err.Error() != "No member with the email who@acme.uz." {
		t.Fatalf("unknown email: %v", err)
	}
	old, _ := a.PasswordResetLink(ctx, " Me@acme.uz ")
	tok, err := a.PasswordResetLink(ctx, "me@acme.uz")
	if err != nil || tok == "" {
		t.Fatal(tok, err)
	}
	if _, err := a.ResetPassword(ctx, old, "a new long password"); !errors.Is(err, ErrResetLink) {
		t.Fatalf("a newer link ends the older: %v", err)
	}
	if _, err := a.ResetPassword(ctx, tok, "short"); err == nil || err.Error() != "Use at least 12 characters." {
		t.Fatalf("short: %v", err)
	}
	email, err := a.ResetPassword(ctx, tok, "a new long password")
	if err != nil || email != "me@acme.uz" {
		t.Fatal(email, err)
	}
	if _, err := a.ResetPassword(ctx, tok, "another long password"); !errors.Is(err, ErrResetLink) {
		t.Fatalf("second use: %v", err)
	}
	if _, err := a.SessionOwner(ctx, session); err == nil {
		t.Fatal("the session survived the reset")
	}
	o, err := a.CheckPassword(ctx, "me@acme.uz", "a new long password")
	if err != nil {
		t.Fatal(err)
	}
	if !o.TotpSecret.Valid {
		t.Fatal("two-step was turned off")
	}

	late, _ := a.PasswordResetLink(ctx, "me@acme.uz")
	a.Now = func() time.Time { return time.Now().Add(time.Hour + time.Second) }
	if _, err := a.ResetPassword(ctx, late, "a new long password"); !errors.Is(err, ErrResetLink) {
		t.Fatalf("expired: %v", err)
	}
}
