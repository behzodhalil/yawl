package seal

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

type (
	PublicKey  [32]byte
	PrivateKey [32]byte
)

const MaxValue = 64 << 10

func GenerateKey() (PublicKey, PrivateKey, error) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return PublicKey{}, PrivateKey{}, err
	}
	return *pub, *priv, nil
}

func (k PublicKey) String() string { return base64.StdEncoding.EncodeToString(k[:]) }

func ParsePublicKey(s string) (PublicKey, error) {
	var k PublicKey
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return k, errors.New("not a public key")
	}
	copy(k[:], b)
	return k, nil
}

func Seal(to PublicKey, value []byte) (string, error) {
	if len(value) > MaxValue {
		return "", fmt.Errorf("value is %d bytes; the limit is %d", len(value), MaxValue)
	}
	k := [32]byte(to)
	out, err := box.SealAnonymous(nil, value, &k, rand.Reader)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

func Open(pub PublicKey, priv PrivateKey, sealed string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, errors.New("sealed value is not base64")
	}
	p, k := [32]byte(pub), [32]byte(priv)
	out, ok := box.OpenAnonymous(nil, b, &p, &k)
	if !ok {
		return nil, errors.New("sealed for another key")
	}
	return out, nil
}

const recoveryPrefix = "yawl-recovery-"

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func EncodeRecovery(priv PrivateKey) string { return recoveryPrefix + b32.EncodeToString(priv[:]) }

func DecodeRecovery(s string) (PublicKey, PrivateKey, error) {
	var priv PrivateKey
	raw, ok := strings.CutPrefix(strings.TrimSpace(s), recoveryPrefix)
	b, err := b32.DecodeString(raw)
	if !ok || err != nil || len(b) != 32 {
		return PublicKey{}, priv, errors.New("not a Yawl recovery key")
	}
	copy(priv[:], b)
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return PublicKey{}, priv, err
	}
	return PublicKey(pub), priv, nil
}
