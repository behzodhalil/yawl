package app

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

var rfcSecret = []byte("12345678901234567890")

func TestTOTPVectors(t *testing.T) {
	for at, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924"} {
		if got := totpCode(rfcSecret, at/30); got != want {
			t.Errorf("T=%d: %s, want %s", at, got, want)
		}
	}
}

func TestTOTPWindowAndReplay(t *testing.T) {
	now := time.Unix(1234567890, 0)
	step := now.Unix() / 30
	for _, d := range []int64{-1, 0, 1} {
		if got, ok := totpStep(rfcSecret, totpCode(rfcSecret, step+d), now, 0); !ok || got != step+d {
			t.Errorf("step %+d: %d %v", d, got, ok)
		}
	}
	for _, d := range []int64{-2, 2} {
		if _, ok := totpStep(rfcSecret, totpCode(rfcSecret, step+d), now, 0); ok {
			t.Errorf("step %+d accepted", d)
		}
	}
	if _, ok := totpStep(rfcSecret, totpCode(rfcSecret, step), now, step); ok {
		t.Error("a used step accepted again")
	}
	c := totpCode(rfcSecret, step)
	if _, ok := totpStep(rfcSecret, c[:3]+" "+c[3:], now, 0); !ok {
		t.Error("a code with a space refused")
	}
	if _, ok := totpStep(rfcSecret, "12345", now, 0); ok {
		t.Error("a short code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes := newRecoveryCodes()
	seen := map[string]bool{}
	for _, c := range codes {
		if !regexp.MustCompile(`^[A-Z2-7]{10}$`).MatchString(c) || seen[c] {
			t.Fatalf("bad or repeated code %q in %v", c, codes)
		}
		seen[c] = true
	}
	if len(codes) != 10 {
		t.Fatalf("%d codes", len(codes))
	}
}

func TestTOTPURI(t *testing.T) {
	got := totpURI(rfcSecret, "a@b.co", "panel.example.com")
	if got != "otpauth://totp/panel.example.com:a@b.co?issuer=panel.example.com&secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" {
		t.Fatal(got)
	}
	if len(newTOTPSecret()) != 20 {
		t.Fatal("secret length")
	}
}

func TestQRSVG(t *testing.T) {
	a, err := QRSVG("otpauth://totp/x:y?secret=ABC")
	if err != nil || !strings.HasPrefix(a, "<svg") || !strings.Contains(a, "viewBox") {
		t.Fatalf("%v %.80s", err, a)
	}
	if b, _ := QRSVG("otpauth://totp/x:y?secret=ABC"); a != b {
		t.Fatal("not stable")
	}
}
