package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/server/vault"
)

var (
	ErrWrongCode = errors.New("Wrong code.")
	ErrRequired  = errors.New("Two-step sign in is required for everyone here.")
	ErrSelfFirst = errors.New("Turn on two-step sign in for yourself first.")
	errNoPending = errors.New("Start turning on two-step sign in again.")
)

const RequiredKey = "auth.require_2fa"

func (a *App) required(ctx context.Context, q *db.Queries) bool {
	v, err := q.GetSetting(ctx, RequiredKey)
	return err == nil && v == "on"
}

func (a *App) NeedsSecondStep(ctx context.Context, o db.Owner) (string, error) {
	switch {
	case o.TotpSecret.Valid:
		return "code", nil
	case a.required(ctx, a.St.Q):
		return "setup", nil
	}
	return "", nil
}

func (a *App) StartTwoStep(ctx context.Context, v *vault.Vault, ownerID, issuer, code string) (secret, uri string, err error) {
	o, err := a.St.Q.GetOwner(ctx, ownerID)
	if err != nil {
		return "", "", err
	}
	if o.TotpSecret.Valid {
		if _, err := a.CheckCode(ctx, v, ownerID, code); err != nil {
			return "", "", err
		}
	}
	raw := newTOTPSecret()
	err = a.St.Q.SetTOTPPending(ctx, db.SetTOTPPendingParams{TotpPending: sql.NullString{String: v.Seal(raw), Valid: true}, UpdatedAt: a.Now().UnixMilli(), ID: ownerID})
	return b32.EncodeToString(raw), totpURI(raw, o.Email, issuer), err
}

func (a *App) ConfirmTwoStep(ctx context.Context, v *vault.Vault, ownerID, code string) ([]string, error) {
	o, err := a.St.Q.GetOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	if !o.TotpPending.Valid {
		return nil, errNoPending
	}
	raw, err := v.Open(o.TotpPending.String)
	if err != nil {
		return nil, err
	}
	step, ok := totpStep(raw, code, a.Now(), 0)
	if !ok {
		return nil, ErrWrongCode
	}
	codes := newRecoveryCodes()
	return codes, a.St.Tx(ctx, func(q *db.Queries) error {
		if cur, err := q.GetOwner(ctx, ownerID); err != nil {
			return err
		} else if cur.TotpPending != o.TotpPending {
			return errNoPending
		}
		if err := q.SetTOTPSecret(ctx, db.SetTOTPSecretParams{TotpSecret: sql.NullString{String: v.Seal(raw), Valid: true}, TotpLastStep: step, UpdatedAt: a.Now().UnixMilli(), ID: ownerID}); err != nil {
			return err
		}
		return putRecoveryCodes(ctx, q, ownerID, codes)
	})
}

func putRecoveryCodes(ctx context.Context, q *db.Queries, ownerID string, codes []string) error {
	if err := q.DeleteRecoveryCodes(ctx, ownerID); err != nil {
		return err
	}
	for _, c := range codes {
		if err := q.InsertRecoveryCode(ctx, db.InsertRecoveryCodeParams{ID: store.NewID("rec_"), OwnerID: ownerID, Hash: HashToken(c)}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) CheckCode(ctx context.Context, v *vault.Vault, ownerID, code string) (recovery bool, err error) {
	o, err := a.St.Q.GetOwner(ctx, ownerID)
	if err != nil {
		return false, err
	}
	if !o.TotpSecret.Valid {
		return false, ErrWrongCode
	}
	raw, err := v.Open(o.TotpSecret.String)
	if err != nil {
		return false, err
	}
	if step, ok := totpStep(raw, code, a.Now(), o.TotpLastStep); ok {
		n, err := a.St.Q.UseTOTPStep(ctx, db.UseTOTPStepParams{TotpLastStep: step, ID: ownerID, TotpLastStep_2: step})
		if err != nil {
			return false, err
		}
		if n == 1 {
			return false, nil
		}
		return false, ErrWrongCode
	}
	rc := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	n, err := a.St.Q.UseRecoveryCode(ctx, db.UseRecoveryCodeParams{UsedAt: sql.NullInt64{Int64: a.Now().UnixMilli(), Valid: true}, OwnerID: ownerID, Hash: HashToken(rc)})
	if err != nil {
		return false, err
	}
	if n == 1 {
		return true, nil
	}
	return false, ErrWrongCode
}

func (a *App) TurnOffTwoStep(ctx context.Context, v *vault.Vault, ownerID, code string) error {
	if a.required(ctx, a.St.Q) {
		return ErrRequired
	}
	if _, err := a.CheckCode(ctx, v, ownerID, code); err != nil {
		return err
	}
	return a.St.Tx(ctx, func(q *db.Queries) error { return clearTwoStep(ctx, q, ownerID, a.Now().UnixMilli()) })
}

func clearTwoStep(ctx context.Context, q *db.Queries, ownerID string, now int64) error {
	if err := q.ClearTOTP(ctx, db.ClearTOTPParams{UpdatedAt: now, ID: ownerID}); err != nil {
		return err
	}
	return q.DeleteRecoveryCodes(ctx, ownerID)
}

func (a *App) ResetTwoStep(ctx context.Context, ownerID string) error {
	return a.St.Tx(ctx, func(q *db.Queries) error {
		if _, err := q.GetOwner(ctx, ownerID); err != nil {
			return err
		}
		if err := clearTwoStep(ctx, q, ownerID, a.Now().UnixMilli()); err != nil {
			return err
		}
		return q.DeleteMemberSessions(ctx, ownerID)
	})
}

func (a *App) NewRecoveryCodes(ctx context.Context, v *vault.Vault, ownerID, code string) ([]string, error) {
	if _, err := a.CheckCode(ctx, v, ownerID, code); err != nil {
		return nil, err
	}
	codes := newRecoveryCodes()
	return codes, a.St.Tx(ctx, func(q *db.Queries) error { return putRecoveryCodes(ctx, q, ownerID, codes) })
}

func (a *App) SetRequired(ctx context.Context, callerID string, on bool, keepSession string) error {
	o, err := a.St.Q.GetOwner(ctx, callerID)
	if err != nil {
		return err
	}
	if on && !o.TotpSecret.Valid {
		return ErrSelfFirst
	}
	val := "off"
	if on {
		val = "on"
	}
	return a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: RequiredKey, Value: val}); err != nil {
			return err
		}
		if !on {
			return nil
		}
		return q.DeleteSessionsWithoutTwoStep(ctx, HashToken(keepSession))
	})
}

func (a *App) TwoStepStatus(ctx context.Context, ownerID string) (on bool, codesLeft int, required bool, err error) {
	o, err := a.St.Q.GetOwner(ctx, ownerID)
	if err != nil {
		return false, 0, false, err
	}
	n, err := a.St.Q.CountRecoveryCodes(ctx, ownerID)
	return o.TotpSecret.Valid, int(n), a.required(ctx, a.St.Q), err
}
