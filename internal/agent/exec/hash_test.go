package exec

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"testing"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

func hashOf(key []byte, v string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))
}

func runHash(t *testing.T, key []byte, values ...string) (proto.Result, proto.HashData) {
	kr, sealTo := keyring(t)
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	var vars []proto.SealedVar
	for i, v := range values {
		vars = append(vars, proto.SealedVar{Key: string(rune('A' + i)), Box: sealTo(v)})
	}
	res := d.Handle(context.Background(), cmd(t, proto.CmdHash, proto.HashArgs{Key: sealTo(string(key)), Vars: vars}))
	var data proto.HashData
	json.Unmarshal(res.Data, &data)
	return res, data
}

func TestHashMatchesHMAC(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	res, data := runHash(t, key, "one", "two")
	if !res.OK || !slices.Equal(data.Hashes, []string{hashOf(key, "one"), hashOf(key, "two")}) {
		t.Fatalf("%+v %v", res, data.Hashes)
	}
}

func TestHashSameValueSameHash(t *testing.T) {
	_, data := runHash(t, bytes.Repeat([]byte{1}, 32), "same", "same", "other")
	if data.Hashes[0] != data.Hashes[1] || data.Hashes[0] == data.Hashes[2] {
		t.Fatal(data.Hashes)
	}
}

func TestHashRefusesShortKey(t *testing.T) {
	res, _ := runHash(t, bytes.Repeat([]byte{1}, 16), "x")
	if res.OK || res.Cause != "the comparison key is not 32 bytes" {
		t.Fatalf("%+v", res)
	}
}

func TestHashNamesUnopenableVar(t *testing.T) {
	kr, sealTo := keyring(t)
	_, otherSeal := keyring(t)
	d := NewDispatcher(&runtime.Fake{}, &fakeProxy{}, &fakeBuilder{})
	d.Keys = kr
	res := d.Handle(context.Background(), cmd(t, proto.CmdHash, proto.HashArgs{Key: sealTo(string(bytes.Repeat([]byte{1}, 32))),
		Vars: []proto.SealedVar{{Key: "TOKEN", Box: otherSeal("x")}}}))
	if res.OK || res.Cause != "Cannot open TOKEN: it was sealed for another server key; set it again" {
		t.Fatalf("%+v", res)
	}
}

func TestHashUnrecorded(t *testing.T) {
	if !proto.Unrecorded(proto.CmdHash) {
		t.Fatal("Hash must be unrecorded")
	}
}
