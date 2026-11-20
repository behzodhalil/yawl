package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/seal"
)

func TestRestoreStreamCutShort(t *testing.T) {
	pub, priv, _ := seal.GenerateKey()
	r, _ := Recipient(pub)
	id, _ := Identity(priv)
	var enc bytes.Buffer
	w, _ := age.Encrypt(&enc, r)
	big := make([]byte, 300_000)
	rand.Read(big)
	w.Write(big)
	w.Close()
	cut := enc.Bytes()[:enc.Len()/2]
	rt := &runtime.Fake{}
	err := restoreStream(context.Background(), rt, runtime.ContainerSpec{Cmd: []string{"mysql"}}, bytes.NewReader(cut), id, sumOf(cut))
	if err == nil {
		t.Fatal("a backup cut in half restored ok")
	}
}

func TestRestoreSpecAllOrNothing(t *testing.T) {
	c := strings.Join(restoreSpec(DB{Host: "shop-db"}).Cmd, " ")
	if c != "pg_restore --no-owner --no-privileges --exit-on-error --single-transaction -h shop-db -U app -d app" {
		t.Fatalf("cmd %q", c)
	}
}
