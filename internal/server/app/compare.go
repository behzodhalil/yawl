package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type Sender interface {
	Send(ctx context.Context, serverID string, cmd proto.Command) (proto.Result, error)
}

type DiffState string

const (
	DiffSame      DiffState = "same"
	DiffDifferent DiffState = "different"
	DiffOnlyLeft  DiffState = "only_left"
	DiffOnlyRight DiffState = "only_right"
)

const DefaultCompareTimeout = 15 * time.Second

type DiffRow struct {
	Service string    `json:"service"`
	Key     string    `json:"key"`
	State   DiffState `json:"state"`
}

func (a *App) Compare(ctx context.Context, gw Sender, project, left, right string, keys map[string]string, actor string) ([]DiffRow, error) {
	l, err := a.envValues(ctx, gw, project, left, keys[left])
	if err != nil {
		return nil, err
	}
	r, err := a.envValues(ctx, gw, project, right, keys[right])
	if err != nil {
		return nil, err
	}
	rows := []DiffRow{}
	for k, lv := range l {
		rv, ok := r[k]
		state := DiffOnlyLeft
		if ok && lv == rv {
			state = DiffSame
		} else if ok {
			state = DiffDifferent
		}
		rows = append(rows, DiffRow{k[0], k[1], state})
	}
	for k := range r {
		if _, ok := l[k]; !ok {
			rows = append(rows, DiffRow{k[0], k[1], DiffOnlyRight})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Service != rows[j].Service {
			return rows[i].Service < rows[j].Service
		}
		return rows[i].Key < rows[j].Key
	})
	return rows, Event(ctx, a.St.Q, a.Now().UnixMilli(), "env.compared", project, actor, map[string]string{"left": left, "right": right})
}

func (a *App) envValues(ctx context.Context, gw Sender, project, env, key string) (map[[2]string]string, error) {
	if key == "" {
		return nil, fmt.Errorf("A comparison key is needed for %s.", env)
	}
	e, err := a.St.Q.GetEnvironmentByName(ctx, db.GetEnvironmentByNameParams{Project: project, Env: env})
	if err != nil || e.DeletedAt.Valid {
		return nil, fmt.Errorf("There is no environment %s in %s.", env, project)
	}
	svcs, err := a.St.Q.ListServicesInEnv(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	names := map[string]string{"": ""}
	for _, s := range svcs {
		names[s.ID] = s.Name
	}
	vars, err := a.St.Q.EnvVarsInEnv(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	out := map[[2]string]string{}
	var sealed []proto.SealedVar
	var at [][2]string
	shared, err := a.St.Q.SharedVarsFor(ctx, e.ServerID)
	if err != nil {
		return nil, err
	}
	own := map[string]bool{}
	for _, v := range vars {
		own[v.Key] = own[v.Key] || v.ServiceID == ""
	}
	for _, v := range shared {
		if !own[v.Key] {
			sealed, at = append(sealed, proto.SealedVar{Key: v.Key, Box: v.AgentBox}), append(at, [2]string{"", v.Key})
		}
	}
	for _, v := range vars {
		k := [2]string{names[v.ServiceID], v.Key}
		if v.Ref != "" {
			out[k] = "ref:" + v.Ref
			continue
		}
		sealed, at = append(sealed, proto.SealedVar{Key: v.Key, Box: v.AgentBox}), append(at, k)
	}
	if len(sealed) == 0 {
		return out, nil
	}
	timeout := a.CompareTimeout
	if timeout == 0 {
		timeout = DefaultCompareTimeout
	}
	cmd, err := proto.NewCommand("hash:"+store.NewID("hs_"), proto.CmdHash, proto.HashArgs{Key: key, Vars: sealed})
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := gw.Send(sctx, e.ServerID, cmd)
	if err != nil {
		return nil, fmt.Errorf("%s is offline; values cannot be compared now.", e.ServerName)
	}
	if !res.OK {
		return nil, fmt.Errorf("%s: %s", e.ServerName, res.Cause)
	}
	var d proto.HashData
	if err := json.Unmarshal(res.Data, &d); err != nil || len(d.Hashes) != len(sealed) {
		return nil, fmt.Errorf("%s answered the comparison with the wrong number of hashes", e.ServerName)
	}
	for i, k := range at {
		out[k] = "hash:" + d.Hashes[i]
	}
	return out, nil
}
