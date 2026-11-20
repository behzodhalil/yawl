package backup

import (
	"bytes"
	"crypto/rand"
	"io"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/seal"
)

func TestAgeRoundTripWithRecoveryKey(t *testing.T) {
	pub, priv, err := seal.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Recipient(pub)
	if err != nil {
		t.Fatal(err)
	}
	id, err := Identity(priv)
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, 1<<20)
	rand.Read(plain)
	var enc bytes.Buffer
	w, err := age.Encrypt(&enc, r)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(plain)
	w.Close()
	rd, err := age.Decrypt(&enc, id)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rd)
	if !bytes.Equal(got, plain) {
		t.Fatal("decrypted bytes differ")
	}
}

func TestAgeMatchesAgeEncoding(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	raw, err := decodeIdentity(id.String())
	if err != nil {
		t.Fatal(err)
	}
	var priv seal.PrivateKey
	copy(priv[:], raw)
	mine, err := Identity(priv)
	if err != nil {
		t.Fatal(err)
	}
	if mine.String() != id.String() || mine.Recipient().String() != id.Recipient().String() {
		t.Fatalf("encodings differ:\n%s\n%s", mine, id)
	}
}

func decodeIdentity(s string) ([]byte, error) {
	s = strings.ToLower(s)
	data := s[strings.LastIndexByte(s, '1')+1 : len(s)-6]
	var out []byte
	acc, bits := 0, 0
	for _, c := range data {
		acc, bits = acc<<5|strings.IndexRune("qpzry9x8gf2tvdw0s3jn54khce6mua7l", c), bits+5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
		}
	}
	return out, nil
}
