package exec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"github.com/behzodhalil/yawl/internal/proto"
)

func (d *Dispatcher) hash(a proto.HashArgs) proto.Result {
	if d.Keys == nil {
		return proto.Result{Cause: "cannot compare: this agent has no secrets key; re-join it"}
	}
	key, err := d.Keys.OpenOnceUnmasked(a.Key)
	if err != nil {
		return proto.Result{Cause: "cannot compare: " + err.Error()}
	}
	if len(key) != 32 {
		return proto.Result{Cause: "the comparison key is not 32 bytes"}
	}
	out := proto.HashData{Hashes: make([]string, 0, len(a.Vars))}
	for _, v := range a.Vars {
		val, cause := d.open(v)
		if cause != "" {
			return proto.Result{Cause: cause}
		}
		m := hmac.New(sha256.New, key)
		m.Write(val)
		out.Hashes = append(out.Hashes, hex.EncodeToString(m.Sum(nil)))
	}
	return ok(out)
}
