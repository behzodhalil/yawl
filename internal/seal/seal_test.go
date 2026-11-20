package seal

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"testing"
)

func TestSealRoundTripAwkward(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 64<<10)
	rand.Read(big)
	for _, v := range [][]byte{[]byte("a\nb"), []byte("x=y"), []byte(`"'`), big} {
		s, err := Seal(pub, v)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Open(pub, priv, s)
		if err != nil || !bytes.Equal(got, v) {
			t.Fatalf("round trip of %d bytes: %v", len(v), err)
		}
	}
	if _, err := Seal(pub, append(big, 'x')); err == nil {
		t.Fatal("64 KB + 1 was sealed")
	}
}

func TestOpenWrongKey(t *testing.T) {
	pub, _, _ := GenerateKey()
	pub2, priv2, _ := GenerateKey()
	s, _ := Seal(pub, []byte("secret"))
	if _, err := Open(pub2, priv2, s); err == nil {
		t.Fatal("opened with the wrong key")
	}
}

func TestRecoveryRoundTrip(t *testing.T) {
	pub, priv, _ := GenerateKey()
	enc := EncodeRecovery(priv)
	p2, k2, err := DecodeRecovery(enc)
	if err != nil || p2 != pub || k2 != priv || enc[:14] != "yawl-recovery-" {
		t.Fatalf("%q %v", enc, err)
	}
	if pk, err := ParsePublicKey(pub.String()); err != nil || pk != pub {
		t.Fatal("public key string round trip")
	}
}

// The browser seals with libsodium's crypto_box_seal. These vectors hold the
// two implementations to one format, in both directions.
type vectors struct {
	Recovery string `json:"recovery"`
	Pub      string `json:"pub"`
	Box      string `json:"box"`
}

func TestWriteVectors(t *testing.T) {
	if os.Getenv("YAWL_WRITE_VECTORS") != "1" {
		t.Skip("set YAWL_WRITE_VECTORS=1 to rewrite testdata/vectors.json")
	}
	pub, priv, _ := GenerateKey()
	box, _ := Seal(pub, []byte("hello from go"))
	b, _ := json.MarshalIndent(vectors{EncodeRecovery(priv), pub.String(), box}, "", "  ")
	os.MkdirAll("testdata", 0o755)
	if err := os.WriteFile("testdata/vectors.json", append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenJSVectors(t *testing.T) {
	b, err := os.ReadFile("../../web/src/testdata/js-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	json.Unmarshal(b, &v)
	pub, priv, err := DecodeRecovery(v.Recovery)
	if err != nil || pub.String() != v.Pub {
		t.Fatalf("the JS recovery key decodes to %s, JS said %s (%v)", pub, v.Pub, err)
	}
	got, err := Open(pub, priv, v.Box)
	if err != nil || string(got) != "hello from js" {
		t.Fatalf("%q %v", got, err)
	}
}
