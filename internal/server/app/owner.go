package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

var (
	ErrWrongLogin = errors.New("Wrong email or password.")
	ErrClaimed    = errors.New("This panel already has an owner.")
	ErrClaimToken = errors.New("That claim link is not valid or has expired. Run yawl server claim-link for a new one.")
)

const (
	claimTTL   = 24 * time.Hour
	SessionTTL = 30 * 24 * time.Hour
)

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *App) HasOwner(ctx context.Context) (bool, error) {
	n, err := a.St.Q.CountOwners(ctx)
	return n > 0, err
}

func (a *App) ClaimFromHash(ctx context.Context, hash string) error {
	if b, err := hex.DecodeString(hash); err != nil || len(b) != sha256.Size {
		return errors.New("YAWL_CLAIM_HASH is not a sha256 in hex")
	}
	return a.St.Tx(ctx, func(q *db.Queries) error {
		if n, err := q.CountOwners(ctx); err != nil || n > 0 {
			return err
		}
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: "claim.hash", Value: hash}); err != nil {
			return err
		}
		return q.SetSetting(ctx, db.SetSettingParams{Key: "claim.expires", Value: strconv.FormatInt(a.Now().AddDate(100, 0, 0).UnixMilli(), 10)})
	})
}

func (a *App) ClaimLink(ctx context.Context) (string, error) {
	tok := randomToken()
	err := a.St.Tx(ctx, func(q *db.Queries) error {
		if n, err := q.CountOwners(ctx); err != nil || n > 0 {
			return cmpErr(err, ErrClaimed)
		}
		exp := a.Now().Add(claimTTL).UnixMilli()
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: "claim.hash", Value: HashToken(tok)}); err != nil {
			return err
		}
		return q.SetSetting(ctx, db.SetSettingParams{Key: "claim.expires", Value: strconv.FormatInt(exp, 10)})
	})
	if err != nil {
		return "", err
	}
	return tok, nil
}

func (a *App) ClaimPending(ctx context.Context) bool {
	exp, err := a.St.Q.GetSetting(ctx, "claim.expires")
	n, _ := strconv.ParseInt(exp, 10, 64)
	return err == nil && a.Now().UnixMilli() < n
}

func cmpErr(err, otherwise error) error {
	if err != nil {
		return err
	}
	return otherwise
}

func checkLogin(email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || len(email) > 254 {
		return "", errors.New("Enter an email address.")
	}
	if len([]rune(password)) < 12 {
		return "", errors.New("Use at least 12 characters.")
	}
	if len(password) > 256 {
		return "", errors.New("Use at most 256 bytes.")
	}
	return email, nil
}

func (a *App) claimable(ctx context.Context, q *db.Queries, token string) error {
	if n, err := q.CountOwners(ctx); err != nil || n > 0 {
		return cmpErr(err, ErrClaimed)
	}
	want, err1 := q.GetSetting(ctx, "claim.hash")
	exp, err2 := q.GetSetting(ctx, "claim.expires")
	n, _ := strconv.ParseInt(exp, 10, 64)
	if err1 != nil || err2 != nil || a.Now().UnixMilli() >= n ||
		subtle.ConstantTimeCompare([]byte(want), []byte(HashToken(token))) != 1 {
		return ErrClaimToken
	}
	return nil
}

func (a *App) Claim(ctx context.Context, token, email, password string) (string, error) {
	email, err := checkLogin(email, password)
	if err != nil {
		return "", err
	}
	if err := a.claimable(ctx, a.St.Q, token); err != nil {
		return "", err
	}
	hash := hashPassword(password)
	var session string
	err = a.St.Tx(ctx, func(q *db.Queries) error {
		if err := a.claimable(ctx, q, token); err != nil {
			return err
		}
		now, id := a.Now().UnixMilli(), store.NewID("own_")
		if err := q.InsertOwner(ctx, db.InsertOwnerParams{ID: id, Email: email, PasswordHash: hash, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := q.DeleteClaim(ctx); err != nil {
			return err
		}
		if err := q.AdoptUnownedTokens(ctx, id); err != nil {
			return err
		}
		session, err = a.newSession(ctx, q, id)
		if err != nil {
			return err
		}
		return Event(ctx, q, now, "owner.claimed", id, "owner", map[string]string{"email": email})
	})
	return session, err
}

func (a *App) newSession(ctx context.Context, q *db.Queries, ownerID string) (string, error) {
	tok, now := randomToken(), a.Now()
	return tok, q.InsertSession(ctx, db.InsertSessionParams{ID: store.NewID("ses_"), OwnerID: ownerID, Hash: HashToken(tok),
		ExpiresAt: now.Add(SessionTTL).UnixMilli(), CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()})
}

func (a *App) CheckPassword(ctx context.Context, email, password string) (db.Owner, error) {
	o, err := a.St.Q.GetOwnerByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, sql.ErrNoRows) {
		spendHashTimeSoUnknownEmailIsNotFaster(password)
		return db.Owner{}, ErrWrongLogin
	} else if err != nil {
		return db.Owner{}, err
	}
	if !checkPassword(o.PasswordHash, password) {
		return db.Owner{}, ErrWrongLogin
	}
	return o, nil
}

func (a *App) NewSession(ctx context.Context, ownerID string) (string, error) {
	now := a.Now().UnixMilli()
	if err := a.St.Q.TouchSignIn(ctx, db.TouchSignInParams{LastSignInAt: sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now, ID: ownerID}); err != nil {
		return "", err
	}
	return a.newSession(ctx, a.St.Q, ownerID)
}

func (a *App) SessionOwner(ctx context.Context, session string) (db.Owner, error) {
	s, err := a.St.Q.GetSessionByHash(ctx, HashToken(session))
	if err != nil {
		return db.Owner{}, err
	}
	if a.Now().UnixMilli() >= s.ExpiresAt {
		return db.Owner{}, sql.ErrNoRows
	}
	return a.St.Q.GetOwner(ctx, s.OwnerID)
}

func (a *App) SignOut(ctx context.Context, session string) error {
	return a.St.Q.DeleteSessionByHash(ctx, HashToken(session))
}

func (a *App) ChangePassword(ctx context.Context, ownerID, old, new, keepSession string) error {
	o, err := a.St.Q.GetOwner(ctx, ownerID)
	if err != nil {
		return err
	}
	if !checkPassword(o.PasswordHash, old) {
		return ErrWrongLogin
	}
	if _, err := checkLogin(o.Email, new); err != nil {
		return err
	}
	return a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.SetOwnerPassword(ctx, db.SetOwnerPasswordParams{PasswordHash: hashPassword(new), UpdatedAt: a.Now().UnixMilli(), ID: ownerID}); err != nil {
			return err
		}
		return q.DeleteOtherSessions(ctx, db.DeleteOtherSessionsParams{OwnerID: ownerID, Hash: HashToken(keepSession)})
	})
}

const argonT, argonM, argonP, argonLen = 2, 19456, 1, 32

var (
	idKey     = argon2.IDKey
	hashSlots = make(chan struct{}, 2)
)

func slowHash(pw, salt []byte, t, m uint32, p uint8, l uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return idKey(pw, salt, t, m, p, l)
}

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key := slowHash([]byte(pw), salt, argonT, argonM, argonP, argonLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonM, argonT, argonP, b64.EncodeToString(salt), b64.EncodeToString(key))
}

func spendHashTimeSoUnknownEmailIsNotFaster(pw string) { hashPassword(pw) }

func checkPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := slowHash([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (a *App) TokenMember(ctx context.Context, hash string) (db.Owner, error) {
	id, err := a.St.Q.GetAPITokenOwner(ctx, hash)
	if err != nil {
		return db.Owner{}, err
	}
	if id == "" {
		if n, err := a.St.Q.CountOwners(ctx); err != nil {
			return db.Owner{}, err
		} else if n > 0 {
			return db.Owner{}, sql.ErrNoRows
		}
		return db.Owner{Role: "admin"}, nil
	}
	return a.St.Q.GetOwner(ctx, id)
}
