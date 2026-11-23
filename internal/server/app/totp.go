package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"rsc.io/qr"
)

// Two-step sign in: RFC 6238 codes, SHA-1, 6 digits, 30 s.

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() []byte {
	b := make([]byte, 20)
	rand.Read(b)
	return b
}

func totpCode(secret []byte, step int64) string {
	m := hmac.New(sha1.New, secret)
	binary.Write(m, binary.BigEndian, step)
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}

func totpStep(secret []byte, code string, now time.Time, last int64) (int64, bool) {
	code = strings.ReplaceAll(code, " ", "")
	if len(code) != 6 {
		return 0, false
	}
	step := now.Unix() / 30
	for _, s := range []int64{step - 1, step, step + 1} {
		if s > last && subtle.ConstantTimeCompare([]byte(totpCode(secret, s)), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

func totpURI(secret []byte, email, issuer string) string {
	q := url.Values{"secret": {b32.EncodeToString(secret)}, "issuer": {issuer}}
	return "otpauth://totp/" + url.PathEscape(issuer) + ":" + url.PathEscape(email) + "?" + q.Encode()
}

func newRecoveryCodes() []string {
	out := make([]string, 10)
	for i := range out {
		b := make([]byte, 7)
		rand.Read(b)
		out[i] = b32.EncodeToString(b)[:10]
	}
	return out
}

const qrQuietZoneModules = 4

func QRSVG(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	var d strings.Builder
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&d, "M%d %dh1v1h-1z", x+qrQuietZoneModules, y+qrQuietZoneModules)
			}
		}
	}
	n := c.Size + 2*qrQuietZoneModules
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`, n, n, n, n, d.String()), nil
}
