package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/behzodhalil/yawl/internal/server/vault"
)

func testVault(t *testing.T) *vault.Vault {
	v, err := vault.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func turnOn(t *testing.T, a *App, v *vault.Vault, ownerID string) ([]byte, []string) {
	t.Helper()
	ctx := context.Background()
	secret, uri, err := a.StartTwoStep(ctx, v, ownerID, "panel.test", "")
	if err != nil || uri == "" {
		t.Fatal(err)
	}
	raw, _ := b32.DecodeString(secret)
	codes, err := a.ConfirmTwoStep(ctx, v, ownerID, totpCode(raw, a.Now().Unix()/30))
	if err != nil {
		t.Fatal(err)
	}
	return raw, codes
}

func secretOf(t *testing.T, a *App, v *vault.Vault, id string) []byte {
	o, _ := a.St.Q.GetOwner(context.Background(), id)
	raw, err := v.Open(o.TotpSecret.String)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func adminOf(t *testing.T, a *App, session string) string {
	o, err := a.SessionOwner(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return o.ID
}

func TestTwoStepTurnOn(t *testing.T) {
	a, s := claimed(t)
	v, ctx, id := testVault(t), context.Background(), adminOf(t, a, s)
	if _, _, err := a.StartTwoStep(ctx, v, id, "panel.test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmTwoStep(ctx, v, id, "000000"); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("wrong code: %v", err)
	}
	_, codes := turnOn(t, a, v, id)
	if len(codes) != 10 {
		t.Fatalf("%d codes", len(codes))
	}
	on, left, required, err := a.TwoStepStatus(ctx, id)
	if err != nil || !on || left != 10 || required {
		t.Fatalf("status %v %d %v %v", on, left, required, err)
	}
	o, _ := a.St.Q.GetOwner(ctx, id)
	if need, _ := a.NeedsSecondStep(ctx, o); need != "code" {
		t.Fatalf("need %q", need)
	}
}

func TestCheckCodeOnce(t *testing.T) {
	a, s := claimed(t)
	v, ctx, id := testVault(t), context.Background(), adminOf(t, a, s)
	now := time.Unix(1_800_000_000, 0)
	a.Now = func() time.Time { return now }
	raw, codes := turnOn(t, a, v, id)
	now = now.Add(30 * time.Second)
	code := totpCode(raw, now.Unix()/30)
	if rec, err := a.CheckCode(ctx, v, id, code); err != nil || rec {
		t.Fatalf("first: %v %v", rec, err)
	}
	if _, err := a.CheckCode(ctx, v, id, code); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("replay: %v", err)
	}
	if rec, err := a.CheckCode(ctx, v, id, codes[0]); err != nil || !rec {
		t.Fatalf("recovery: %v %v", rec, err)
	}
	if _, err := a.CheckCode(ctx, v, id, codes[0]); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("recovery twice: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	passed := 0
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.CheckCode(ctx, v, id, codes[1]); err == nil {
				mu.Lock()
				passed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if passed != 1 {
		t.Fatalf("%d sign ins with one recovery code", passed)
	}
	if _, left, _, _ := a.TwoStepStatus(ctx, id); left != 8 {
		t.Fatalf("%d left", left)
	}
}

func TestRequired(t *testing.T) {
	a, s := claimed(t)
	v, ctx, admin := testVault(t), context.Background(), adminOf(t, a, s)
	dev, devSession := member(t, a, admin, "developer", "dev@acme.uz")
	if err := a.SetRequired(ctx, admin, true, s); !errors.Is(err, ErrSelfFirst) {
		t.Fatalf("without own: %v", err)
	}
	turnOn(t, a, v, admin)
	if err := a.SetRequired(ctx, admin, true, s); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SessionOwner(ctx, devSession); err == nil {
		t.Fatal("the member without two-step kept a session")
	}
	if _, err := a.SessionOwner(ctx, s); err != nil {
		t.Fatal("the caller's session ended")
	}
	d, _ := a.St.Q.GetOwner(ctx, dev)
	if need, _ := a.NeedsSecondStep(ctx, d); need != "setup" {
		t.Fatalf("need %q", need)
	}
	raw := secretOf(t, a, v, admin)
	if err := a.TurnOffTwoStep(ctx, v, admin, totpCode(raw, a.Now().Unix()/30+1)); !errors.Is(err, ErrRequired) {
		t.Fatalf("turn off while required: %v", err)
	}
}

func TestResetTwoStep(t *testing.T) {
	a, s := claimed(t)
	v, ctx, id := testVault(t), context.Background(), adminOf(t, a, s)
	turnOn(t, a, v, id)
	if err := a.ResetTwoStep(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SessionOwner(ctx, s); err == nil {
		t.Fatal("a session survived the reset")
	}
	on, left, _, _ := a.TwoStepStatus(ctx, id)
	o, _ := a.St.Q.GetOwner(ctx, id)
	if need, _ := a.NeedsSecondStep(ctx, o); on || left != 0 || need != "" {
		t.Fatalf("%v %d %q", on, left, need)
	}
}

func TestSecretSealed(t *testing.T) {
	a, s := claimed(t)
	v, ctx, id := testVault(t), context.Background(), adminOf(t, a, s)
	raw, _ := turnOn(t, a, v, id)
	o, _ := a.St.Q.GetOwner(ctx, id)
	if !o.TotpSecret.Valid || o.TotpSecret.String == b32.EncodeToString(raw) {
		t.Fatalf("stored %q", o.TotpSecret.String)
	}
	if got, err := v.Open(o.TotpSecret.String); err != nil || string(got) != string(raw) {
		t.Fatalf("open: %v", err)
	}
}

func TestRemoveMemberWithTwoStep(t *testing.T) {
	a, s := claimed(t)
	v, ctx, admin := testVault(t), context.Background(), adminOf(t, a, s)
	dev, _ := member(t, a, admin, "developer", "dev@acme.uz")
	turnOn(t, a, v, dev)
	if err := a.RemoveMember(ctx, dev); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordThenSession(t *testing.T) {
	a, _ := claimed(t)
	ctx := context.Background()
	if _, err := a.CheckPassword(ctx, "me@acme.uz", "wrong password here"); !errors.Is(err, ErrWrongLogin) {
		t.Fatalf("wrong: %v", err)
	}
	o, err := a.CheckPassword(ctx, " Me@Acme.uz ", "correct horse battery")
	if err != nil || o.Email != "me@acme.uz" || o.LastSignInAt.Valid {
		t.Fatalf("%+v %v", o, err)
	}
	link, _, _ := a.Invite(ctx, "viewer", o.ID)
	id, err := a.AcceptInvite(ctx, link, "v@acme.uz", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if a.St.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE owner_id = ?", id).Scan(&n); n != 0 {
		t.Fatalf("an invite made %d sessions", n)
	}
	s, err := a.NewSession(ctx, id)
	if m, _ := a.SessionOwner(ctx, s); err != nil || m.ID != id {
		t.Fatalf("session: %v", err)
	}
}

func TestConfirmTwoStepOnce(t *testing.T) {
	a, s := claimed(t)
	v, ctx, id := testVault(t), context.Background(), adminOf(t, a, s)
	secret, _, err := a.StartTwoStep(ctx, v, id, "panel.test", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := b32.DecodeString(secret)
	code := totpCode(raw, a.Now().Unix()/30)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.ConfirmTwoStep(ctx, v, id, code); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if n, _ := a.St.Q.CountRecoveryCodes(ctx, id); ok != 1 || n != 10 {
		t.Fatalf("%d confirms passed, %d codes", ok, n)
	}
}
