package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

var (
	ErrInvite    = errors.New("That invite is not valid or has expired. Ask an Admin for a new one.")
	ErrLastAdmin = errors.New("There must be at least one Admin.")
)

const inviteTTL = 7 * 24 * time.Hour

func validRole(r string) error {
	if r != "admin" && r != "developer" && r != "viewer" {
		return fmt.Errorf("role must be admin, developer or viewer, got %q", r)
	}
	return nil
}

func (a *App) Invite(ctx context.Context, role, by string) (string, string, error) {
	if err := validRole(role); err != nil {
		return "", "", err
	}
	if err := a.checkMembers(ctx, a.St.Q); err != nil {
		return "", "", err
	}
	tok, now, id := randomToken(), a.Now(), store.NewID("inv_")
	err := a.St.Q.InsertInvite(ctx, db.InsertInviteParams{ID: id, Hash: HashToken(tok), Role: role, CreatedBy: by,
		ExpiresAt: now.Add(inviteTTL).UnixMilli(), CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()})
	return tok, id, err
}

func (a *App) AcceptInvite(ctx context.Context, token, email, password string) (string, error) {
	email, err := checkLogin(email, password)
	if err != nil {
		return "", err
	}
	if n, err := a.St.Q.InviteOpen(ctx, db.InviteOpenParams{Hash: HashToken(token), ExpiresAt: a.Now().UnixMilli()}); err != nil {
		return "", err
	} else if n == 0 {
		return "", ErrInvite
	}
	hash := hashPassword(password)
	var id string
	err = a.St.Tx(ctx, func(q *db.Queries) error {
		if err := a.checkMembers(ctx, q); err != nil {
			return err
		}
		now := a.Now().UnixMilli()
		role, err := q.UseInvite(ctx, db.UseInviteParams{Now: sql.NullInt64{Int64: now, Valid: true}, Hash: HashToken(token)})
		if err != nil {
			return ErrInvite
		}
		if _, err := q.GetOwnerByEmail(ctx, email); err == nil {
			return fmt.Errorf("%s is already a member.", email)
		}
		id = store.NewID("own_")
		if err := q.InsertMember(ctx, db.InsertMemberParams{ID: id, Email: email, PasswordHash: hash, Role: role, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return Event(ctx, q, now, "member.joined", id, email, map[string]string{"role": role})
	})
	return id, err
}

func (a *App) SetRole(ctx context.Context, id, role string) error {
	if err := validRole(role); err != nil {
		return err
	}
	return a.St.Tx(ctx, func(q *db.Queries) error {
		m, err := q.GetOwner(ctx, id)
		if err != nil {
			return err
		}
		if m.Role == "admin" && role != "admin" {
			if err := lastAdmin(ctx, q); err != nil {
				return err
			}
		}
		if err := q.SetMemberRole(ctx, db.SetMemberRoleParams{Role: role, UpdatedAt: a.Now().UnixMilli(), ID: id}); err != nil {
			return err
		}
		return endAccess(ctx, q, id)
	})
}

func (a *App) RemoveMember(ctx context.Context, id string) error {
	return a.St.Tx(ctx, func(q *db.Queries) error {
		m, err := q.GetOwner(ctx, id)
		if err != nil {
			return err
		}
		if m.Role == "admin" {
			if err := lastAdmin(ctx, q); err != nil {
				return err
			}
		}
		if err := endAccess(ctx, q, id); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, id); err != nil {
			return err
		}
		return q.DeleteMember(ctx, id)
	})
}

func lastAdmin(ctx context.Context, q *db.Queries) error {
	n, err := q.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

func endAccess(ctx context.Context, q *db.Queries, id string) error {
	if err := q.DeleteMemberSessions(ctx, id); err != nil {
		return err
	}
	if err := q.DeleteInvitesBy(ctx, id); err != nil {
		return err
	}
	return q.DeleteMemberTokens(ctx, id)
}

func (a *App) CreateTokenFor(ctx context.Context, name, ownerID string) (string, error) {
	b := make([]byte, 24)
	rand.Read(b)
	plain := "yawl_" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	now := a.Now().UnixMilli()
	err := a.St.Q.CreateMemberToken(ctx, db.CreateMemberTokenParams{ID: store.NewID("tok_"), Name: name, Hash: HashToken(plain), OwnerID: ownerID, CreatedAt: now, UpdatedAt: now})
	return plain, err
}
