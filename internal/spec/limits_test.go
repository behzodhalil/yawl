package spec

import (
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	for in, want := range map[string]Size{"512MB": 512e6, "512 mb": 512e6, "1.5GB": 1.5e9, "1 GB": 1e9, "": 0} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"512", "12TB", "-1GB", "lots", "99999999999GB", "1" + strings.Repeat("0", 400) + "MB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestSizeString(t *testing.T) {
	for v, want := range map[Size]string{0: "none", 512e6: "512MB", 1.5e9: "1.5GB", 2e9: "2GB"} {
		if got := v.String(); got != want {
			t.Errorf("%d: %q", v, got)
		}
	}
	if Cores(0).String() != "none" || Cores(0.5).String() != "0.5" {
		t.Fatal(Cores(0).String(), Cores(0.5).String())
	}
}

func TestLimitsValidate(t *testing.T) {
	base := "project: acme\nenvironment: production\nserver: hel1\nservices:\n"
	if _, err := Parse([]byte(base + "  - {name: api, kind: web, repo: https://x/y, cpu: 0.05}\n")); err == nil || err.Error() != `api: "cpu" must be at least 0.1` {
		t.Fatal(err)
	}
	for _, c := range []string{".nan", ".inf", "-.inf"} {
		if _, err := Parse([]byte(base + "  - {name: api, kind: web, repo: https://x/y, cpu: " + c + "}\n")); err == nil || err.Error() != `api: "cpu" must be a number of cores like 0.5` {
			t.Errorf("%s: %v", c, err)
		}
	}
	if _, err := Parse([]byte(base + "  - {name: api, kind: web, repo: https://x/y, memory: 32MB}\n")); err == nil || err.Error() != `api: "memory" must be at least 64MB` {
		t.Fatal(err)
	}
	if _, err := Parse([]byte(base + "  - {name: api, kind: web, repo: https://x/y, memory: \"512\"}\n")); err == nil || !strings.Contains(err.Error(), `"memory"`) {
		t.Fatal(err)
	}
	for _, s := range []string{
		"  - {name: a, kind: web, repo: https://x/y, cpu: 0.5, memory: 512MB}\n",
		"  - {name: a, kind: worker, repo: https://x/y, cpu: 0.5, memory: 512MB}\n",
		"  - {name: a, kind: cron, repo: https://x/y, schedule: \"0 3 * * *\", command: [x], cpu: 0.5, memory: 512MB}\n",
		"  - {name: a, kind: postgres, cpu: 0.5, memory: 512MB}\n",
	} {
		f, err := Parse([]byte(base + s))
		if err != nil || f.Services[0].CPU != 0.5 || f.Services[0].Memory != 512e6 {
			t.Errorf("%s: %+v %v", s, f.Services, err)
		}
	}
}
