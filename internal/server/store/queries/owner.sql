-- name: CountOwners :one
SELECT COUNT(*) FROM owner;
-- name: InsertOwner :exec
INSERT INTO owner (id, email, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?);
-- name: GetOwnerByEmail :one
SELECT * FROM owner WHERE email = ?;
-- name: GetOwner :one
SELECT * FROM owner WHERE id = ?;
-- name: SetOwnerPassword :exec
UPDATE owner SET password_hash = ?, updated_at = ? WHERE id = ?;
-- name: InsertSession :exec
INSERT INTO sessions (id, owner_id, hash, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?);
-- name: GetSessionByHash :one
SELECT * FROM sessions WHERE hash = ?;
-- name: DeleteSessionByHash :exec
DELETE FROM sessions WHERE hash = ?;
-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= ?;
-- name: DeleteOtherSessions :exec
DELETE FROM sessions WHERE owner_id = ? AND hash <> ?;
-- name: DeleteClaim :exec
DELETE FROM settings WHERE key IN ('claim.hash', 'claim.expires');
-- name: InsertMember :exec
INSERT INTO owner (id, email, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?);
-- name: ListMembers :many
SELECT * FROM owner ORDER BY created_at, id;
-- name: CountAdmins :one
SELECT COUNT(*) FROM owner WHERE role = 'admin';
-- name: SetMemberRole :exec
UPDATE owner SET role = ?, updated_at = ? WHERE id = ?;
-- name: DeleteMember :exec
DELETE FROM owner WHERE id = ?;
-- name: DeleteMemberSessions :exec
DELETE FROM sessions WHERE owner_id = ?;
-- name: DeleteMemberTokens :exec
DELETE FROM api_tokens WHERE owner_id = ?;
-- name: CreateMemberToken :exec
INSERT INTO api_tokens (id, name, hash, owner_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?);
-- name: InsertInvite :exec
INSERT INTO invites (id, hash, role, created_by, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?);
-- name: UseInvite :one
UPDATE invites SET used_at = sqlc.arg(now), updated_at = sqlc.arg(now) WHERE hash = sqlc.arg(hash) AND used_at IS NULL AND expires_at > sqlc.arg(now) RETURNING role;
-- name: PendingInvites :many
SELECT * FROM invites WHERE used_at IS NULL AND expires_at > ? ORDER BY created_at;
-- name: DeleteInvite :execrows
DELETE FROM invites WHERE id = ? AND used_at IS NULL;
-- name: AdoptUnownedTokens :exec
UPDATE api_tokens SET owner_id = ? WHERE owner_id = '';
-- name: DeleteInvitesBy :exec
DELETE FROM invites WHERE created_by = ? AND used_at IS NULL;
-- name: InviteOpen :one
SELECT COUNT(*) FROM invites WHERE hash = ? AND used_at IS NULL AND expires_at > ?;
-- name: SetTOTPPending :exec
UPDATE owner SET totp_pending = ?, updated_at = ? WHERE id = ?;
-- name: SetTOTPSecret :exec
UPDATE owner SET totp_secret = ?, totp_pending = NULL, totp_last_step = ?, updated_at = ? WHERE id = ?;
-- name: UseTOTPStep :execrows
UPDATE owner SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?;
-- name: ClearTOTP :exec
UPDATE owner SET totp_secret = NULL, totp_pending = NULL, totp_last_step = 0, updated_at = ? WHERE id = ?;
-- name: InsertRecoveryCode :exec
INSERT INTO recovery_codes (id, owner_id, hash) VALUES (?, ?, ?);
-- name: UseRecoveryCode :execrows
UPDATE recovery_codes SET used_at = ? WHERE owner_id = ? AND hash = ? AND used_at IS NULL;
-- name: CountRecoveryCodes :one
SELECT COUNT(*) FROM recovery_codes WHERE owner_id = ? AND used_at IS NULL;
-- name: DeleteRecoveryCodes :exec
DELETE FROM recovery_codes WHERE owner_id = ?;
-- name: DeleteSessionsWithoutTwoStep :exec
DELETE FROM sessions WHERE hash <> ? AND owner_id IN (SELECT id FROM owner WHERE totp_secret IS NULL);
-- name: FirstOwner :one
SELECT * FROM owner WHERE role = 'admin' ORDER BY created_at, id LIMIT 1;
-- name: SetOwnerSSOSubject :exec
UPDATE owner SET sso_subject = ? WHERE id = ?;
-- name: InsertPasswordReset :exec
INSERT INTO password_resets (id, hash, owner_id, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?);
-- name: DeletePasswordResets :exec
DELETE FROM password_resets WHERE owner_id = ? AND used_at IS NULL;
-- name: PasswordResetOwner :one
SELECT owner_id FROM password_resets WHERE hash = ? AND used_at IS NULL AND expires_at > ?;
-- name: UsePasswordReset :one
UPDATE password_resets SET used_at = sqlc.arg(now), updated_at = sqlc.arg(now) WHERE hash = sqlc.arg(hash) AND used_at IS NULL AND expires_at > sqlc.arg(now) RETURNING owner_id;
