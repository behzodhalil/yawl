package agekey

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"testing"

	"filippo.io/age"
)

func TestFromRecoveryMatchesSealVectors(t *testing.T) {
	b, err := os.ReadFile("../seal/testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ Recovery, Pub string }
	json.Unmarshal(b, &v)
	id, pub, err := FromRecovery(v.Recovery)
	if err != nil || base64.StdEncoding.EncodeToString(pub[:]) != v.Pub {
		t.Fatalf("%v %x", err, pub)
	}
	r, err := Recipient(pub)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, _ := age.Encrypt(&buf, r)
	io.WriteString(w, "panel")
	w.Close()
	out, err := age.Decrypt(&buf, id)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(out); string(got) != "panel" {
		t.Fatalf("%q", got)
	}
	if _, _, err := FromRecovery("yawl-recovery-NOPE"); err == nil || err.Error() != "That is not a Yawl recovery key." {
		t.Fatalf("%v", err)
	}
}

func TestBackupKeyVector(t *testing.T) {
	k, err := BackupSigningKey("yawl-recovery-XPLB4K453FMRWQIXU7RVNS3MWIQJMP3R2TM2UWDJ6BUWFSRJQOFA")
	if err != nil || base64.StdEncoding.EncodeToString(k) != "K1YH1GIfpg6mhGI3zpgJaYfyZf/LpaQuv+YzO8zvCQc=" {
		t.Fatalf("%v %x", err, k)
	}
	if _, err := BackupSigningKey("yawl-recovery-NOPE"); err == nil {
		t.Fatal("a bad recovery key derived a key")
	}
}
