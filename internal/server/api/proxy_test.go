package api

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestTrustedProxy(t *testing.T) {
	req := func(from string) (string, bool) {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = from + ":5555"
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
		r.Header.Set("X-Forwarded-Proto", "https")
		return clientIP(r), secure(r)
	}
	setTrustedProxy(netip.Addr{})
	if ip, s := req("172.18.0.1"); ip != "172.18.0.1" || s {
		t.Fatalf("untrusted: %s %v", ip, s)
	}
	setTrustedProxy(netip.MustParseAddr("172.18.0.1"))
	defer setTrustedProxy(netip.Addr{})
	if ip, s := req("172.18.0.1"); ip != "203.0.113.7" || !s {
		t.Fatalf("the gateway: %s %v", ip, s)
	}
	if ip, s := req("172.18.0.5"); ip != "172.18.0.5" || s {
		t.Fatalf("a neighbour: %s %v", ip, s)
	}
	if ip, _ := req("127.0.0.1"); ip != "203.0.113.7" {
		t.Fatalf("loopback: %s", ip)
	}
}
