package agekey

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"

	"filippo.io/age"
	"golang.org/x/crypto/curve25519"
)

func Recipient(pub [32]byte) (*age.X25519Recipient, error) {
	return age.ParseX25519Recipient(bech32("age", pub[:]))
}

func Identity(priv [32]byte) (*age.X25519Identity, error) {
	return age.ParseX25519Identity(strings.ToUpper(bech32("age-secret-key-", priv[:])))
}

const recoveryPrefix = "yawl-recovery-"

func FromRecovery(text string) (*age.X25519Identity, [32]byte, error) {
	var pub [32]byte
	priv, err := decode(text)
	if err != nil {
		return nil, pub, err
	}
	p, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return nil, pub, err
	}
	copy(pub[:], p)
	id, err := Identity(priv)
	return id, pub, err
}

func decode(text string) (priv [32]byte, err error) {
	raw, ok := strings.CutPrefix(strings.TrimSpace(text), recoveryPrefix)
	b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(raw)
	if !ok || err != nil || len(b) != 32 {
		return priv, errors.New("That is not a Yawl recovery key.")
	}
	copy(priv[:], b)
	return priv, nil
}

func BackupSigningKey(text string) ([]byte, error) {
	priv, err := decode(text)
	if err != nil {
		return nil, err
	}
	return hkdf.Key(sha256.New, priv[:], nil, "yawl panel backup signing", 32)
}

const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// bech32 encodes data under hrp (BIP 173), the encoding age uses for keys;
// age's own encoder is internal to it.
func bech32(hrp string, data []byte) string {
	var five []byte
	acc, bits := 0, 0
	for _, b := range data {
		acc, bits = acc<<8|int(b), bits+8
		for bits >= 5 {
			bits -= 5
			five = append(five, byte(acc>>bits&31))
		}
	}
	if bits > 0 {
		five = append(five, byte(acc<<(5-bits)&31))
	}
	var values []byte
	for _, c := range hrp {
		values = append(values, byte(c>>5))
	}
	values = append(values, 0)
	for _, c := range hrp {
		values = append(values, byte(c&31))
	}
	values = append(append(values, five...), 0, 0, 0, 0, 0, 0)
	mod := polymod(values) ^ 1
	var sb strings.Builder
	sb.WriteString(hrp + "1")
	for _, v := range five {
		sb.WriteByte(charset[v])
	}
	for i := range 6 {
		sb.WriteByte(charset[mod>>(5*(5-i))&31])
	}
	return sb.String()
}

func polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := range 5 {
			if top>>i&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}
