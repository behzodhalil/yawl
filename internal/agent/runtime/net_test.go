package runtime

import (
	"net/netip"
	"os"
	"slices"
	"testing"
)

func TestParseProcNetTCP(t *testing.T) {
	f, _ := os.Open("testdata/tcp")
	defer f.Close()
	got, err := ParseProcNetTCP(f)
	if err != nil {
		t.Fatal(err)
	}
	want := []Listen{{netip.MustParseAddr("127.0.0.1"), 3000}, {netip.MustParseAddr("0.0.0.0"), 10000}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v (the ESTABLISHED row must be ignored)", got, want)
	}
}

func TestParseProcNetTCP6(t *testing.T) {
	f, _ := os.Open("testdata/tcp6")
	defer f.Close()
	got, err := ParseProcNetTCP(f)
	if err != nil {
		t.Fatal(err)
	}
	want := []Listen{{netip.MustParseAddr("::"), 8080}, {netip.MustParseAddr("::1"), 80}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLoopbackOnly(t *testing.T) {
	if !(Listen{netip.MustParseAddr("127.0.0.1"), 1}).Loopback() || (Listen{netip.MustParseAddr("0.0.0.0"), 1}).Loopback() {
		t.Fatal("Loopback wrong")
	}
}
