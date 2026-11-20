package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

var ErrResetLink = errors.New("This reset link has expired or was already used.")

const resetTTL = time.Hour

func (a *App) PasswordResetLink(ctx context.Context, email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	o, err := a.St.Q.GetOwnerByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("No member with the email %s.", email)
	} else if err != nil {
		return "", err
	}
	tok, now := randomToken(), a.Now()
	return tok, a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeletePasswordResets(ctx, o.ID); err != nil {
			return err
		}
		return q.InsertPasswordReset(ctx, db.InsertPasswordResetParams{ID: store.NewID("pwr_"), Hash: HashToken(tok), OwnerID: o.ID,
			ExpiresAt: now.Add(resetTTL).UnixMilli(), CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()})
	})
}

func (a *App) ResetPassword(ctx context.Context, token, password string) (string, error) {
	id, err := a.St.Q.PasswordResetOwner(ctx, db.PasswordResetOwnerParams{Hash: HashToken(token), ExpiresAt: a.Now().UnixMilli()})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrResetLink
	} else if err != nil {
		return "", err
	}
	o, err := a.St.Q.GetOwner(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrResetLink
	} else if err != nil {
		return "", err
	}
	if _, err := checkLogin(o.Email, password); err != nil {
		return "", err
	}
	hash := hashPassword(password)
	return o.Email, a.St.Tx(ctx, func(q *db.Queries) error {
		now := a.Now().UnixMilli()
		if _, err := q.UsePasswordReset(ctx, db.UsePasswordResetParams{Now: sql.NullInt64{Int64: now, Valid: true}, Hash: HashToken(token)}); err != nil {
			return ErrResetLink
		}
		if err := q.SetOwnerPassword(ctx, db.SetOwnerPasswordParams{PasswordHash: hash, UpdatedAt: now, ID: o.ID}); err != nil {
			return err
		}
		if err := q.DeleteMemberSessions(ctx, o.ID); err != nil {
			return err
		}
		return Event(ctx, q, now, "audit", "reset-password", o.Email,
			map[string]any{"text": "reset their password with a link", "path": "/reset", "status": 200})
	})
}
