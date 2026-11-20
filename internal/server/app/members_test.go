package app

import (
	"context"
	"testing"
	"time"
)

func claimed(t *testing.T) (*App, string) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	tok, _ := a.ClaimLink(ctx)
	s, err := a.Claim(ctx, tok, "me@acme.uz", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	return a, s
}

func TestClaimMakesAdmin(t *testing.T) {
	a, s := claimed(t)
	o, err := a.SessionOwner(context.Background(), s)
	if err != nil || o.Role != "admin" {
		t.Fatal(o.Role, err)
	}
}

func TestSignInRecordsLastSignIn(t *testing.T) {
	a, _ := claimed(t)
	at := time.UnixMilli(1_800_000_000_000)
	a.Now = func() time.Time { return at }
	o, err := a.CheckPassword(context.Background(), "me@acme.uz", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := a.St.Q.GetOwner(context.Background(), o.ID); o.LastSignInAt.Valid {
		t.Fatalf("stamped after the password alone: %+v", o.LastSignInAt)
	}
	if _, err := a.NewSession(context.Background(), o.ID); err != nil {
		t.Fatal(err)
	}
	if o, _ := a.St.Q.GetOwner(context.Background(), o.ID); !o.LastSignInAt.Valid || o.LastSignInAt.Int64 != at.UnixMilli() {
		t.Fatalf("%+v", o.LastSignInAt)
	}
}

func TestTokenMember(t *testing.T) {
	a, s := claimed(t)
	ctx := context.Background()
	old, _ := a.CreateToken(ctx, "ci")
	o, _ := a.SessionOwner(ctx, s)
	m, err := a.TokenMember(ctx, HashToken(old))
	if err != nil || m.ID != o.ID || m.Role != "admin" {
		t.Fatal(m, err)
	}
}

func TestInviteOnce(t *testing.T) {
	a, s := claimed(t)
	ctx := context.Background()
	adm, _ := a.SessionOwner(ctx, s)
	link, _, err := a.Invite(ctx, "developer", adm.ID)
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for _, e := range []string{"dev1@acme.uz", "dev2@acme.uz"} {
		go func(e string) { _, err := a.AcceptInvite(ctx, link, e, "correct horse battery"); errs <- err }(e)
	}
	ok := 0
	for i := 0; i < 2; i++ {
		if err := <-errs; err == nil {
			ok++
		} else if err != ErrInvite {
			t.Fatal(err)
		}
	}
	ms, _ := a.St.Q.ListMembers(ctx)
	if ok != 1 || len(ms) != 2 {
		t.Fatalf("%d sign-ups, %d members", ok, len(ms))
	}
	if ErrInvite.Error() != "That invite is not valid or has expired. Ask an Admin for a new one." {
		t.Fatal(ErrInvite)
	}
}

func TestInviteExpires(t *testing.T) {
	a, s := claimed(t)
	ctx := context.Background()
	adm, _ := a.SessionOwner(ctx, s)
	link, _, _ := a.Invite(ctx, "viewer", adm.ID)
	later := time.Now().Add(7*24*time.Hour + time.Minute)
	a.Now = func() time.Time { return later }
	if _, err := a.AcceptInvite(ctx, link, "v@acme.uz", "correct horse battery"); err != ErrInvite {
		t.Fatal(err)
	}
}

func member(t *testing.T, a *App, adminID, role, email string) (string, string) {
	t.Helper()
	ctx := context.Background()
	link, _, _ := a.Invite(ctx, role, adminID)
	id, err := a.AcceptInvite(ctx, link, email, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.NewSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return id, s
}

func TestRoleChangeEndsSessions(t *testing.T) {
	a, s := claimed(t)
	ctx := context.Background()
	adm, _ := a.SessionOwner(ctx, s)
	id, ses := member(t, a, adm.ID, "developer", "dev@acme.uz")
	tok, _ := a.CreateTokenFor(ctx, "ci", id)
	if err := a.SetRole(ctx, id, "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SessionOwner(ctx, ses); err == nil {
		t.Fatal("the session outlived the role change")
	}
	if _, err := a.TokenMember(ctx, HashToken(tok)); err == nil {
		t.Fatal("the token outlived the role change")
	}
}

func TestRemovedMemberTokenStops(t *testing.T) {
	a, s := claimed(t)
	ctx := context.Background()
	adm, _ := a.SessionOwner(ctx, s)
	id, _ := member(t, a, adm.ID, "developer", "dev@acme.uz")
	tok, _ := a.CreateTokenFor(ctx, "ci", id)
	if err := a.RemoveMember(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.TokenMember(ctx, HashToken(tok)); err == nil {
		t.Fatal("a removed member's token still works")
	}
}

func TestTokenCarriesRole(t *testing.T) {
	a, s := claimed(t)
	ctx := context.Background()
	adm, _ := a.SessionOwner(ctx, s)
	id, _ := member(t, a, adm.ID, "viewer", "v@acme.uz")
	tok, _ := a.CreateTokenFor(ctx, "ci", id)
	if m, err := a.TokenMember(ctx, HashToken(tok)); err != nil || m.Role != "viewer" {
		t.Fatal(m.Role, err)
	}
}

func TestLastAdmin(t *testing.T) {
	a, s := claimed(t)
	ctx := context.Background()
	adm, _ := a.SessionOwner(ctx, s)
	for _, err := range []error{a.SetRole(ctx, adm.ID, "developer"), a.RemoveMember(ctx, adm.ID)} {
		if err == nil || err.Error() != "There must be at least one Admin." {
			t.Fatal(err)
		}
	}
	id, _ := member(t, a, adm.ID, "admin", "two@acme.uz")
	if err := a.SetRole(ctx, adm.ID, "developer"); err != nil {
		t.Fatalf("with a second Admin: %v", err)
	}
	if err := a.RemoveMember(ctx, id); err == nil {
		t.Fatal("removed the last Admin")
	}
}

func TestUnownedTokensAfterClaim(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	early, _ := a.CreateToken(ctx, "before-claim")
	tok, _ := a.ClaimLink(ctx)
	s, _ := a.Claim(ctx, tok, "me@acme.uz", "correct horse battery")
	owner, _ := a.SessionOwner(ctx, s)
	if m, err := a.TokenMember(ctx, HashToken(early)); err != nil || m.ID != owner.ID {
		t.Fatalf("the early token is not the owner's: %+v %v", m, err)
	}
	two, _ := member(t, a, owner.ID, "admin", "two@acme.uz")
	_ = two
	if err := a.SetRole(ctx, owner.ID, "developer"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.TokenMember(ctx, HashToken(early)); err == nil {
		t.Fatal("the demoted owner's early token survived")
	}
	st.DB.Exec(`INSERT INTO api_tokens (id, name, hash, owner_id, created_at, updated_at) VALUES ('tok_x', 'stray', ?, '', 1, 1)`, HashToken("yawl_stray"))
	if _, err := a.TokenMember(ctx, HashToken("yawl_stray")); err == nil {
		t.Fatal("a token with no member is an Admin after the claim")
	}
}

func TestRemovedAdminsInvitesGo(t *testing.T) {
	a, s := claimed(t)
	ctx := context.Background()
	owner, _ := a.SessionOwner(ctx, s)
	id, _ := member(t, a, owner.ID, "admin", "two@acme.uz")
	link, _, _ := a.Invite(ctx, "admin", id)
	if err := a.RemoveMember(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AcceptInvite(ctx, link, "back@acme.uz", "correct horse battery"); err != ErrInvite {
		t.Fatalf("a removed Admin's invite still works: %v", err)
	}
}
