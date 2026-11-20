package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

func TestLimits(t *testing.T) {
	if l, err := ParseLimits("servers=3,members=5,services=20"); err != nil || l != (Limits{Servers: 3, Members: 5, Services: 20}) {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := ParseLimits("servers=many"); err == nil {
		t.Fatal("a bad limit was taken")
	}
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	a.Limits = Limits{Services: 2, Members: 1}
	two, _ := spec.Parse([]byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: https://x/y, port: 8080}\n  - {name: web, kind: web, repo: https://x/y, port: 8080}\n"))
	if _, err := a.Apply(ctx, two, "owner", false); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	three, _ := spec.Parse([]byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: https://x/y, port: 8080}\n  - {name: web, kind: web, repo: https://x/y, port: 8080}\n  - {name: jobs, kind: worker, repo: https://x/y}\n"))
	if _, err := a.Apply(ctx, three, "owner", false); err == nil || err.Error() != "This panel allows 2 services in the beta." {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := a.Claim(ctx, mustClaim(t, a), "me@acme.uz", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Invite(ctx, "developer", "me@acme.uz"); err == nil || !strings.Contains(err.Error(), "This panel allows 1 members in the beta.") {
		t.Fatalf("invite over the limit: %v", err)
	}
}

func mustClaim(t *testing.T, a *App) string {
	tok, err := a.ClaimLink(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestLimitsOtherPaths(t *testing.T) {
	a, st := restoreSeed(t)
	ctx := context.Background()
	n, _ := st.Q.CountLiveServices(ctx)
	a.Limits = Limits{Services: int(n)}
	if _, err := a.Restore(ctx, "svc_db", "bak_ok", "acme-db-2", "sealed-key", "owner"); !errors.As(err, new(LimitError)) {
		t.Fatalf("restore into a new database: %v", err)
	}
	if err := a.Delete(ctx, "service", "svc_db", "owner"); err != nil {
		t.Fatal(err)
	}
	two, _ := spec.Parse([]byte("project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: web, kind: web, repo: https://x/y, port: 8080}\n"))
	if _, err := a.Apply(ctx, two, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := a.RestoreFromTrash(ctx, "service", "svc_db", "owner"); !errors.As(err, new(LimitError)) {
		t.Fatalf("restore from the trash: %v", err)
	}
}
