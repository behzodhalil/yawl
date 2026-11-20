package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"

	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
	"github.com/behzodhalil/yawl/internal/spec"
)

var importName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (a *App) Import(ctx context.Context, serviceID, source, ref, file, actor string) (string, error) {
	svc, err := a.St.Q.GetService(ctx, serviceID)
	if err != nil {
		return "", err
	}
	if !spec.IsDatabase(spec.Kind(svc.Kind)) {
		return "", fmt.Errorf("%s is not a database; a dump goes into a database", svc.Name)
	}
	if svc.Kind == "valkey" {
		return "", errors.New("Valkey is a cache; Yawl does not import into it")
	}
	if svc.Kind == "clickhouse" {
		return "", errors.New("Yawl does not import into ClickHouse yet; use clickhouse-client")
	}
	shown := ref
	switch source {
	case "upload":
	case "url":
		u, err := url.Parse(ref)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return "", errors.New("the link must be http or https")
		}
		shown = u.Host + u.Path
	case "path":
		if !importName.MatchString(ref) {
			return "", fmt.Errorf("%q: give a file name in /var/lib/yawl/agent/imports, without folders", ref)
		}
	default:
		return "", errors.New("the source is upload, url or path")
	}
	b := make([]byte, 24)
	rand.Read(b)
	impID, depID, now := store.NewID("imp_"), store.NewID("dep_"), a.Now().UnixMilli()
	err = a.St.Tx(ctx, func(q *db.Queries) error {
		if err := q.CreateImport(ctx, db.CreateImportParams{ID: impID, ServiceID: svc.ID, Source: source, Ref: ref, File: file, Token: hex.EncodeToString(b), CreatedAt: now}); err != nil {
			return err
		}
		if err := q.CreateImportDeploy(ctx, db.CreateImportDeployParams{ID: depID, ServiceID: svc.ID, ImportID: impID, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return Event(ctx, q, now, "import.queued", depID, actor, map[string]string{"service": svc.ID, "source": source, "from": shown})
	})
	return depID, err
}
