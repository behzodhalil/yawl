package compose

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// magicRe finds a name as $NAME, ${NAME} or ${NAME:-default} (the default is
// dropped: Yawl gives the value); $$ is Compose's escaped dollar, left alone.
var magicRe = regexp.MustCompile(`\$\$|\$\{(SERVICE_[A-Z0-9_]+)(?::?[-?][^}]*)?\}|\$(SERVICE_[A-Z0-9_]+)`)

func nameOf(m []string) string { return m[1] + m[2] }

func namesIn(v string) []string {
	var out []string
	for _, m := range magicRe.FindAllStringSubmatch(v, -1) {
		if n := nameOf(m); n != "" {
			out = append(out, n)
		}
	}
	return out
}

const (
	alnumSet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	lowerSet = "abcdefghijklmnopqrstuvwxyz0123456789"
	symSet   = "!#%&*+-=?@^_~" // none that a shell, a URL or YAML quoting reads
)

func pick(set string, n int) string {
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		b[i] = set[k.Int64()]
	}
	return string(b)
}

func Secret() string { return pick(alnumSet, 32) }

func withSymbols(n int) func() string {
	return func() string {
		for {
			if s := pick(alnumSet+symSet, n); strings.ContainsAny(s, symSet) {
				return s
			}
		}
	}
}

func chars(set string, n int) func() string { return func() string { return pick(set, n) } }

func realBase64(n int) func() string {
	return func() string {
		b := make([]byte, n)
		rand.Read(b)
		return base64.StdEncoding.EncodeToString(b)
	}
}

func hexOf(n int) func() string {
	return func() string {
		b := make([]byte, n/2)
		rand.Read(b)
		return hex.EncodeToString(b)
	}
}

var generators = []struct {
	prefix string
	make   func() string
}{
	{"SERVICE_PASSWORDWITHSYMBOLS_64_", withSymbols(64)}, {"SERVICE_PASSWORDWITHSYMBOLS_", withSymbols(32)},
	{"SERVICE_PASSWORD_64_", chars(alnumSet, 64)}, {"SERVICE_PASSWORD_", chars(alnumSet, 32)},
	{"SERVICE_LOWERCASEUSER_", chars(lowerSet, 16)}, {"SERVICE_USER_", chars(alnumSet, 16)},
	{"SERVICE_REALBASE64_128_", realBase64(128)}, {"SERVICE_REALBASE64_64_", realBase64(64)},
	{"SERVICE_REALBASE64_32_", realBase64(32)}, {"SERVICE_REALBASE64_", realBase64(32)},
	{"SERVICE_BASE64_128_", chars(alnumSet, 128)}, {"SERVICE_BASE64_64_", chars(alnumSet, 64)},
	{"SERVICE_BASE64_32_", chars(alnumSet, 32)}, {"SERVICE_BASE64_", chars(alnumSet, 32)}, // not base64, as in Coolify
	{"SERVICE_HEX_128_", hexOf(128)}, {"SERVICE_HEX_64_", hexOf(64)}, {"SERVICE_HEX_32_", hexOf(32)},
}

func generatorFor(name string) func() string {
	var longest func() string
	longestLen := 0
	for _, g := range generators {
		if len(g.prefix) > longestLen && strings.HasPrefix(name, g.prefix) && len(name) > len(g.prefix) {
			longest, longestLen = g.make, len(g.prefix)
		}
	}
	return longest
}

type namer struct {
	host   func(service string) string
	ids    map[string]string
	values map[string]string
	filled bool
	dbs    map[string]bool
}

func newNamer(services []string, host func(string) string) *namer {
	n := &namer{host: host, ids: map[string]string{}, values: map[string]string{}}
	for _, s := range services {
		n.ids[strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(s))] = s
	}
	return n
}

// address reads SERVICE_URL_<ID>[_<PORT>] or SERVICE_FQDN_<ID>[_<PORT>]: the
// service, its port (80 without one), and whether it wants the scheme.
func (n *namer) address(name string) (service string, port int, url, ok bool) {
	rest, url := strings.CutPrefix(name, "SERVICE_URL_")
	if !url {
		var fqdn bool
		if rest, fqdn = strings.CutPrefix(name, "SERVICE_FQDN_"); !fqdn {
			return "", 0, false, false
		}
	}
	if s := n.ids[rest]; s != "" {
		return s, 80, url, true
	}
	if i := strings.LastIndexByte(rest, '_'); i > 0 {
		if p, err := strconv.Atoi(rest[i+1:]); err == nil && n.ids[rest[:i]] != "" && p > 0 && p < 65536 {
			return n.ids[rest[:i]], p, url, true
		}
	}
	return "", 0, false, false
}

func (n *namer) declared(env map[string]*string, into map[string]int) {
	see := func(name string) {
		if s, p, _, ok := n.address(name); ok {
			into[s] = p
		}
	}
	for k, v := range env {
		see(k)
		if v != nil {
			for _, name := range namesIn(*v) {
				see(name)
			}
		}
	}
}

func (n *namer) fill(v string) (out string, generated bool, left []string) {
	out = magicRe.ReplaceAllStringFunc(v, func(m string) string {
		name := nameOf(magicRe.FindStringSubmatch(m))
		if name == "" {
			return m
		}
		if gen := generatorFor(name); gen != nil {
			if n.values[name] == "" {
				n.values[name] = gen()
			}
			generated = true
			return n.values[name]
		}
		if s, _, url, ok := n.address(name); ok && !n.dbs[s] {
			if n.host == nil || n.host(s) == "" {
				left = append(left, name)
				return m
			}
			n.filled = true
			if url {
				return "https://" + n.host(s)
			}
			return n.host(s)
		}
		return m
	})
	return out, generated, left
}

func (n *namer) known(k string) bool {
	if generatorFor(k) != nil {
		return true
	}
	s, _, _, ok := n.address(k)
	return ok && !n.dbs[s]
}

func (n *namer) dbAddress(v string) (name, db string) {
	for _, name := range namesIn(v) {
		if s, _, _, ok := n.address(name); ok && n.dbs[s] {
			return name, s
		}
	}
	return "", ""
}
