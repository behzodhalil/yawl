package spec

import (
	"strings"
	"testing"
)

func TestDomainPaths(t *testing.T) {
	web := func(name, extra string) string {
		return "  - {name: " + name + ", kind: web, repo: https://x/y, port: 80" + extra + "}\n"
	}
	f, err := Parse([]byte(vbase + web("site", ", domains: [acme.uz, www.acme.uz], www: bare") + web("api", ", domains: [acme.uz/api, acme.uz/Docs_v1.2/x-y]") + web("v1", ", domains: [api.acme.uz/v1]")))
	if err != nil {
		t.Fatal(err)
	}
	if h, p := SplitDomain(f.Services[1].Domains[0]); h != "acme.uz" || p != "/api" {
		t.Fatalf("%s %s", h, p)
	}
	for in, want := range map[string]string{
		web("a", ", domains: [acme.uz/api/]"):                                                               `a: "domains" entry "acme.uz/api/": a path starts with / and has letters, digits, -, _ and . between its slashes, like acme.uz/api`,
		web("a", ", domains: [acme.uz/]"):                                                                   `"acme.uz/": a path starts with /`,
		web("a", ", domains: [acme.uz//api]"):                                                               `"acme.uz//api": a path starts with /`,
		web("a", ", domains: [acme.uz/a%20b]"):                                                              `"acme.uz/a%20b": a path starts with /`,
		web("a", ", domains: [acme.uz/a/../b]"):                                                             `"acme.uz/a/../b": a part of a path cannot be . or ..`,
		web("a", ", domains: [Acme.uz/api]"):                                                                `"Acme.uz/api" must be a lowercase host name`,
		web("a", ", domains: [x.sslip.io/api]"):                                                             `sslip.io hosts are given by Yawl itself`,
		web("a", ", domains: [acme.uz/api]") + web("b", ", domains: [acme.uz/api]"):                         `b: "domains" entry "acme.uz/api" appears twice`,
		web("a", ", domains: [acme.uz/api/v2]") + web("b", ", domains: [acme.uz/api]"):                      `b: "domains" entry "acme.uz/api" overlaps "acme.uz/api/v2"; one host cannot have a path inside another`,
		web("a", ", domains: [acme.uz/api, acme.uz/api/v2]"):                                                `a: "domains" entry "acme.uz/api/v2" overlaps "acme.uz/api"`,
		web("a", ", domains: [acme.uz, www.acme.uz], www: bare") + web("b", ", domains: [www.acme.uz/api]"): `a: "www" redirects all of www.acme.uz, so it cannot have the path "www.acme.uz/api"`,
		web("a", ", domains: [acme.uz, www.acme.uz], www: www") + web("b", ", domains: [acme.uz/api]"):      `a: "www" redirects all of acme.uz, so it cannot have the path "acme.uz/api"`,
	} {
		if _, err := Parse([]byte(vbase + in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", in, err, want)
		}
	}
	if _, err := Parse([]byte(vbase + web("a", ", domains: [shop.uz/api, www.shop.uz/api], www: bare"))); err != nil {
		t.Fatalf("www pairs whole hosts only: %v", err)
	}
	if !PathsNest("/api", "/api/v2") || PathsNest("/api", "/apix") || PathsNest("", "/api") {
		t.Fatal("PathsNest")
	}
}
