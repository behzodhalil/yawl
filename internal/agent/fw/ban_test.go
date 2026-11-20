package fw

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBanAfterFive(t *testing.T) {
	b := &Ban{Allowed: func(a netip.Addr) bool { return a == netip.MustParseAddr("192.0.2.50") }}
	at := time.Unix(1_800_000_000, 0)
	lines := []string{
		"Failed password for root from 192.0.2.9 port 4242 ssh2",
		"Invalid user x from 192.0.2.9 port 4243",
		"Connection closed by authenticating user root 192.0.2.9 port 1 [preauth]",
		"Failed password for invalid user admin from 192.0.2.9 port 4244 ssh2",
		"Accepted publickey for deploy from 192.0.2.77 port 22 ssh2",
		"Failed publickey for root from 192.0.2.9 port 4245 ssh2",
	}
	var banned []netip.Addr
	for i, l := range lines {
		if a, ban := b.Line(l, at.Add(time.Duration(i)*time.Minute)); ban {
			banned = append(banned, a)
		}
	}
	if len(banned) != 1 || banned[0] != netip.MustParseAddr("192.0.2.9") {
		t.Fatalf("banned %v", banned)
	}

	slow := &Ban{}
	for i := range 5 {
		if _, ban := slow.Line("Failed password for root from 192.0.2.8 port 1 ssh2", at.Add(time.Duration(i)*165*time.Second)); ban {
			t.Fatal("5 failures over 11 minutes banned")
		}
	}
	for i := range 6 {
		if _, ban := b.Line("Failed password for root from 192.0.2.50 port 1 ssh2", at.Add(time.Duration(i)*time.Second)); ban {
			t.Fatal("an allowed address was banned")
		}
	}
	v6 := &Ban{}
	var got bool
	for i := range 5 {
		_, got = v6.Line("Failed password for root from 2001:db8::9 port 1 ssh2", at.Add(time.Duration(i)*time.Second))
	}
	if !got {
		t.Fatal("IPv6 not banned")
	}
}

func TestTailAuthLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.log")
	os.WriteFile(path, []byte("Failed password for root from 192.0.2.66 port 1 ssh2\n"), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan netip.Addr, 1)
	go (&Ban{}).Tail(ctx, path, 10*time.Millisecond, func(a netip.Addr) { got <- a })
	time.Sleep(50 * time.Millisecond)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	for i := range 5 {
		fmt.Fprintf(f, "Oct 29 sshd[1]: Failed password for root from 192.0.2.9 port %d ssh2\n", i)
	}
	f.Close()
	select {
	case a := <-got:
		if a != netip.MustParseAddr("192.0.2.9") {
			t.Fatalf("banned %v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ban from the log")
	}
}

func TestBanReadsTheRealAddress(t *testing.T) {
	b := &Ban{}
	for _, l := range []string{
		"Invalid user x from 203.0.113.5 from 192.0.2.9 port 4243",
		"Failed password for invalid user y from 203.0.113.5 from 192.0.2.9 port 4244 ssh2",
		"Connection closed by authenticating user z 203.0.113.5 port 1 192.0.2.9 port 2 [preauth]",
	} {
		if a, _ := b.Line(l, time.Unix(0, 0)); a != netip.MustParseAddr("192.0.2.9") {
			t.Errorf("%q read as %v", l, a)
		}
	}
}

func TestBanForgetsOldAddresses(t *testing.T) {
	b := &Ban{}
	at := time.Unix(1_800_000_000, 0)
	for i := range 3000 {
		b.Line(fmt.Sprintf("Failed password for root from 10.0.%d.%d port 1 ssh2", i/256, i%256), at)
	}
	b.Line("Failed password for root from 192.0.2.9 port 1 ssh2", at.Add(banWindow))
	if len(b.seen) != 1 {
		t.Fatalf("%d addresses kept", len(b.seen))
	}
}
