package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const reservedFile = "project: acme\nenvironment: production\nserver: hel1\nservices:\n  - {name: api, kind: web, repo: \"https://x/y\", domains: [%s]}\n"

func TestApplyRefusesPanelHost(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	st.Q.SetSetting(ctx, db.SetSettingParams{Key: "panel.domain", Value: "panel.acme.uz"})
	for _, d := range []string{"panel.acme.uz", "panel.acme.uz/api"} {
		if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(reservedFile, d)), "owner", true); err == nil || err.Error() != "panel.acme.uz is the panel's domain." {
			t.Fatalf("%s: %v", d, err)
		}
	}
	if ds, _ := st.Q.ListDomains(ctx, "svc_1"); len(ds) != 0 {
		t.Fatalf("domains = %+v", ds)
	}
	if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(reservedFile, "api.acme.uz")), "owner", false); err != nil {
		t.Fatal(err)
	}
}

func TestApplyRefusesStatusPageHost(t *testing.T) {
	st, _ := seed(t)
	a, ctx := New(st), context.Background()
	if _, err := st.DB.Exec(`INSERT INTO status_pages (id, slug, title, server_id, domain, created_at, updated_at) VALUES ('sp_1','acme','Acme','srv_1','status.acme.uz',1,1)`); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"status.acme.uz", "status.acme.uz/x"} {
		if _, err := a.Apply(ctx, parse(t, fmt.Sprintf(reservedFile, d)), "owner", false); err == nil || !strings.Contains(err.Error(), "is a status page's domain") {
			t.Fatalf("%s: %v", d, err)
		}
	}
}
