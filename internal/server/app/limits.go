package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type Limits struct{ Servers, Members, Services int }

func ParseLimits(s string) (Limits, error) {
	var l Limits
	for _, kv := range strings.Split(s, ",") {
		if kv = strings.TrimSpace(kv); kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		n, err := strconv.Atoi(v)
		to := map[string]*int{"servers": &l.Servers, "members": &l.Members, "services": &l.Services}[k]
		if err != nil || n < 0 || to == nil {
			return Limits{}, fmt.Errorf("YAWL_LIMITS: %q is not one of servers, members, services = a number", kv)
		}
		*to = n
	}
	return l, nil
}

type LimitError struct {
	N    int
	What string
}

func (e LimitError) Error() string {
	return fmt.Sprintf("This panel allows %d %s in the beta.", e.N, e.What)
}

func OverLimit(n int, what string) error { return LimitError{n, what} }

func (a *App) CheckServices(ctx context.Context, q *db.Queries, before int64) error {
	return a.checkServices(ctx, q, before)
}

func (a *App) checkServices(ctx context.Context, q *db.Queries, before int64) error {
	if a.Limits.Services == 0 {
		return nil
	}
	after, err := q.CountLiveServices(ctx)
	if err != nil {
		return err
	}
	if after > int64(a.Limits.Services) && after > before {
		return OverLimit(a.Limits.Services, "services")
	}
	return nil
}

func (a *App) checkMembers(ctx context.Context, q *db.Queries) error {
	if a.Limits.Members == 0 {
		return nil
	}
	n, err := q.CountOwners(ctx)
	if err != nil {
		return err
	}
	if n >= int64(a.Limits.Members) {
		return OverLimit(a.Limits.Members, "members")
	}
	return nil
}
