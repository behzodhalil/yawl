package join

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

var (
	ErrTokenUnknown = errors.New("join token unknown")
	ErrTokenUsed    = errors.New("join token already used")
	ErrTokenExpired = errors.New("join token expired")
)

const tokenTTL = time.Hour

func hashToken(plain string) string {
	s := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(s[:])
}

func NewToken(ctx context.Context, q *db.Queries, now time.Time) (string, error) {
	b := make([]byte, 32)
	rand.Read(b)
	plain := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	err := q.CreateJoinToken(ctx, db.CreateJoinTokenParams{
		ID: store.NewID("jtk_"), Hash: hashToken(plain), ExpiresAt: now.Add(tokenTTL).UnixMilli(),
		CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	})
	return plain, err
}

func NewPanelToken(ctx context.Context, q *db.Queries, now time.Time) (string, error) {
	b := make([]byte, 32)
	rand.Read(b)
	plain := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	err := q.CreatePanelJoinToken(ctx, db.CreatePanelJoinTokenParams{
		ID: store.NewID("jtk_"), Hash: hashToken(plain), ExpiresAt: now.Add(tokenTTL).UnixMilli(),
		CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	})
	return plain, err
}

func IsPanel(ctx context.Context, q *db.Queries, plain string) bool {
	t, err := q.GetJoinTokenByHash(ctx, hashToken(plain))
	return err == nil && t.Panel == 1
}

func Redeem(ctx context.Context, q *db.Queries, plain string, now time.Time) error {
	t, err := q.GetJoinTokenByHash(ctx, hashToken(plain))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTokenUnknown
	}
	if err != nil {
		return err
	}
	if t.UsedAt.Valid {
		return ErrTokenUsed
	}
	if now.UnixMilli() > t.ExpiresAt {
		return ErrTokenExpired
	}
	n, err := q.UseJoinToken(ctx, db.UseJoinTokenParams{UsedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, UpdatedAt: now.UnixMilli(), ID: t.ID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTokenUsed
	}
	return nil
}

type Request struct {
	Token       string `json:"token"`
	CSRPEM      string `json:"csr_pem"`
	AgentPubkey string `json:"agent_pubkey"`
	WGPubkey    string `json:"wg_pubkey"`
	Name        string `json:"name"`
	PublicIP    string `json:"public_ip"`
}

type Response struct {
	ServerID string `json:"server_id"`
	CertPEM  string `json:"cert_pem"`
	CAPEM    string `json:"ca_pem"`
}

type ErrServerLimit struct{ Max int }

func (e ErrServerLimit) Error() string {
	return fmt.Sprintf("This panel allows %d servers in the beta.", e.Max)
}

func ServerName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out != "" && (out[0] < 'a' || out[0] > 'z') {
		out = "s-" + out
	}
	if len(out) > 31 {
		out = strings.TrimRight(out[:31], "-")
	}
	return out
}

func HandlerLimited(st *store.Store, ca *CA, max int, drop func(serverID string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		req.Name = ServerName(req.Name)
		ip, err := netip.ParseAddr(req.PublicIP)
		if err != nil || req.Name == "" {
			http.Error(w, "name and public_ip are required", http.StatusBadRequest)
			return
		}
		blk, _ := pem.Decode([]byte(req.CSRPEM))
		if blk == nil {
			http.Error(w, "csr_pem is not PEM", http.StatusBadRequest)
			return
		}
		csr, err := x509.ParseCertificateRequest(blk.Bytes)
		if err != nil {
			http.Error(w, "bad CSR", http.StatusBadRequest)
			return
		}
		if k, err := base64.StdEncoding.DecodeString(req.WGPubkey); req.WGPubkey != "" && (err != nil || len(k) != 32) {
			http.Error(w, "wg_pubkey is not a WireGuard public key", http.StatusBadRequest)
			return
		}
		if k, err := base64.StdEncoding.DecodeString(req.AgentPubkey); req.AgentPubkey != "" && (err != nil || len(k) != 32) {
			http.Error(w, "agent_pubkey is not a 32-byte public key", http.StatusBadRequest)
			return
		}
		ctx, now := r.Context(), time.Now()
		var resp Response
		err = st.Tx(ctx, func(q *db.Queries) error {
			panel := IsPanel(ctx, q, req.Token)
			if _, err := q.GetServerByName(ctx, req.Name); max > 0 && errors.Is(err, sql.ErrNoRows) {
				if n, err := q.CountServers(ctx); err != nil {
					return err
				} else if n >= int64(max) {
					return ErrServerLimit{max}
				}
			}
			if err := Redeem(ctx, q, req.Token, now); err != nil {
				return err
			}
			if old, err := q.GetServerByName(ctx, req.Name); err == nil {
				if err := q.DeleteServerShares(ctx, old.ID); err != nil {
					return err
				}
			}
			id, err := q.UpsertServer(ctx, db.UpsertServerParams{
				ID: store.NewID("srv_"), Name: req.Name, PublicIp: ip.String(), AgentPubkey: req.AgentPubkey,
				CertSerial: "", CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
			})
			if err != nil {
				return err
			}
			if err := assignPrivateIP(ctx, q, id, req.WGPubkey, now); err != nil {
				return err
			}
			cert, err := ca.Sign(csr, id)
			if err != nil {
				return err
			}
			if err := RecordSerial(ctx, q, id, cert, now); err != nil {
				return err
			}
			if panel {
				if err := q.SetSetting(ctx, db.SetSettingParams{Key: "panel.server_id", Value: id}); err != nil {
					return err
				}
			}
			resp = Response{ServerID: id, CertPEM: string(cert), CAPEM: string(ca.PEM)}
			return nil
		})
		switch {
		case errors.Is(err, ErrTokenUnknown), errors.Is(err, ErrTokenUsed), errors.Is(err, ErrTokenExpired):
			http.Error(w, err.Error(), http.StatusUnauthorized)
		case errors.As(err, new(ErrServerLimit)):
			http.Error(w, err.Error(), http.StatusForbidden)
		case err != nil:
			http.Error(w, fmt.Sprintf("join failed: %v", err), http.StatusInternalServerError)
		default:
			drop(resp.ServerID)
			json.NewEncoder(w).Encode(resp)
		}
	})
}

func RecordSerial(ctx context.Context, q *db.Queries, serverID string, certPEM []byte, now time.Time) error {
	c, err := ParseCertPEM(certPEM)
	if err != nil {
		return err
	}
	return q.SetServerCert(ctx, db.SetServerCertParams{CertSerial: c.SerialNumber.Text(16), UpdatedAt: now.UnixMilli(), ID: serverID})
}

func assignPrivateIP(ctx context.Context, q *db.Queries, id, wgPubkey string, now time.Time) error {
	srv, err := q.GetServer(ctx, id)
	if err != nil {
		return err
	}
	ip := srv.PrivateIp
	if ip == "" {
		used, err := q.UsedPrivateIPs(ctx)
		if err != nil {
			return err
		}
		a, err := NextPrivateIP(used)
		if err != nil {
			return err
		}
		ip = a.String()
	}
	return q.SetServerWG(ctx, db.SetServerWGParams{WgPubkey: wgPubkey, PrivateIp: ip, UpdatedAt: now.UnixMilli(), ID: id})
}

func NextPrivateIP(used []string) (netip.Addr, error) {
	taken := map[string]bool{}
	for _, u := range used {
		taken[u] = true
	}
	for a := netip.MustParseAddr("10.73.0.1"); a.Compare(netip.MustParseAddr("10.73.255.254")) <= 0; a = a.Next() {
		if !taken[a.String()] {
			return a, nil
		}
	}
	return netip.Addr{}, errors.New("the private network 10.73.0.0/16 is full")
}

func Revoke(ctx context.Context, q *db.Queries, plain string, now time.Time) error {
	t, err := q.GetJoinTokenByHash(ctx, hashToken(plain))
	if err != nil {
		return err
	}
	_, err = q.UseJoinToken(ctx, db.UseJoinTokenParams{UsedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, UpdatedAt: now.UnixMilli(), ID: t.ID})
	return err
}
