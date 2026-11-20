package backup

import (
	"filippo.io/age"

	"github.com/behzodhalil/yawl/internal/agekey"
	"github.com/behzodhalil/yawl/internal/seal"
)

func Recipient(pub seal.PublicKey) (*age.X25519Recipient, error) { return agekey.Recipient(pub) }

func Identity(priv seal.PrivateKey) (*age.X25519Identity, error) { return agekey.Identity(priv) }
