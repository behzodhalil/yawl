package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type Network struct {
	St  *store.Store
	GW  Sender
	Now func() time.Time
}

const (
	wgPort      = 51820
	silentAfter = 5 * time.Minute
)

func (w *Network) Tick(ctx context.Context) error {
	if app.Adopting(ctx, w.St.Q) {
		return nil
	}
	all, err := w.St.Q.ListServers(ctx)
	if err != nil {
		return err
	}
	var mesh []db.Server
	for _, s := range all {
		if s.WgPubkey != "" && s.PrivateIp != "" {
			mesh = append(mesh, s)
		}
	}
	byKey := map[string]string{}
	for _, s := range mesh {
		byKey[s.WgPubkey] = s.ID
	}
	for _, s := range mesh {
		if s.Offline == 1 {
			continue
		}
		a := proto.NetworkArgs{Address: s.PrivateIp, ListenPort: wgPort, Peers: []proto.Peer{}}
		for _, p := range mesh {
			if p.ID != s.ID {
				a.Peers = append(a.Peers, proto.Peer{PublicKey: p.WgPubkey, Endpoint: net.JoinHostPort(p.PublicIp, strconv.Itoa(wgPort)), AllowedIP: p.PrivateIp + "/32"})
			}
		}
		if err := w.apply(ctx, s, a, byKey); err != nil {
			return err
		}
	}
	for _, s := range mesh {
		if s.Offline == 1 {
			if err := w.St.Q.DeletePeersOf(ctx, s.ID); err != nil {
				return err
			}
		}
	}
	if err := w.silence(ctx, mesh); err != nil {
		return err
	}
	return w.left(ctx, mesh, all)
}

func (w *Network) left(ctx context.Context, mesh, all []db.Server) error {
	flags, err := w.St.Q.SettingsWithPrefix(ctx, "network.open.")
	if err != nil {
		return err
	}
	on, names := map[string]bool{}, map[string]string{}
	for _, s := range mesh {
		on[s.ID] = true
	}
	for _, s := range all {
		names[s.ID] = s.Name
	}
	name := func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return id
	}
	for _, f := range flags {
		ids := strings.SplitN(strings.TrimPrefix(f.Key, "network.open."), ":", 2)
		if len(ids) != 2 || (on[ids[0]] && on[ids[1]]) {
			continue
		}
		pair := []string{name(ids[0]), name(ids[1])}
		sort.Strings(pair)
		incident, label := "network:"+ids[0]+":"+ids[1], pair[0]+" and "+pair[1]
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.DeleteSettingKey(ctx, f.Key); err != nil {
				return err
			}
			return announceIncident(ctx, q, w.Now().UnixMilli(), "network.recovered", incident, nil,
				app.NetworkOK(pair[0], pair[1], "One of them left the private network", label+": one of them left the private network."), incident, "close", label, false)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *Network) apply(ctx context.Context, s db.Server, a proto.NetworkArgs, byKey map[string]string) error {
	cmd, err := proto.NewCommand(fmt.Sprintf("network:%s:%d", s.ID, w.Now().UnixMilli()), proto.CmdNetwork, a)
	if err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := w.GW.Send(sctx, s.ID, cmd)
	key := "network.cause." + s.ID
	if err != nil || !res.OK {
		cause := res.Cause
		if err != nil {
			cause = err.Error()
		}
		if old, _ := w.St.Q.GetSetting(ctx, key); old != cause {
			slog.Warn("private network not applied", "server", s.Name, "cause", cause)
			return w.St.Q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: cause})
		}
		return nil
	}
	w.St.Q.DeleteSettingKey(ctx, key)
	var d proto.NetworkData
	if json.Unmarshal(res.Data, &d) != nil {
		return nil
	}
	now := w.Now().UnixMilli()
	return w.St.Tx(ctx, func(q *db.Queries) error {
		old, err := q.ServerPeers(ctx)
		if err != nil {
			return err
		}
		since := map[string]int64{}
		for _, r := range old {
			if r.ServerID == s.ID {
				since[r.PeerID] = r.Since
			}
		}
		if err := q.DeleteServerPeers(ctx, s.ID); err != nil {
			return err
		}
		for k, ms := range d.Handshakes {
			peer, ok := byKey[k]
			if !ok {
				continue
			}
			first, ok := since[peer]
			if !ok {
				first = now
			}
			if err := q.UpsertServerPeer(ctx, db.UpsertServerPeerParams{ServerID: s.ID, PeerID: peer, LastHandshake: ms, Since: first, UpdatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *Network) silence(ctx context.Context, mesh []db.Server) error {
	rows, err := w.St.Q.ServerPeers(ctx)
	if err != nil {
		return err
	}
	type seen struct{ last, since int64 }
	pair := map[[2]string]*seen{}
	for _, r := range rows {
		k := [2]string{r.ServerID, r.PeerID}
		if k[0] > k[1] {
			k[0], k[1] = k[1], k[0]
		}
		p := pair[k]
		if p == nil {
			p = &seen{}
			pair[k] = p
		}
		p.last, p.since = max(p.last, r.LastHandshake), max(p.since, r.Since)
	}
	byID := map[string]db.Server{}
	for _, s := range mesh {
		byID[s.ID] = s
	}
	keys := make([][2]string, 0, len(pair))
	for k := range pair {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	now := w.Now()
	for _, k := range keys {
		a, okA := byID[k[0]]
		b, okB := byID[k[1]]
		if !okA || !okB || a.Offline == 1 || b.Offline == 1 {
			continue
		}
		names := []string{a.Name, b.Name}
		sort.Strings(names)
		p := pair[k]
		quiet := now.Sub(time.UnixMilli(max(p.last, p.since)))
		incident, label, flag := "network:"+k[0]+":"+k[1], names[0]+" and "+names[1], "network.open."+k[0]+":"+k[1]
		open, _ := w.St.Q.GetSetting(ctx, flag)
		switch {
		case quiet >= silentAfter && open == "":
			text := fmt.Sprintf("%s cannot reach %s on the private network (no handshake for %d min). Open UDP %d between them.",
				names[0], names[1], int(quiet/time.Minute), wgPort)
			if err := w.St.Tx(ctx, func(q *db.Queries) error {
				if err := q.SetSetting(ctx, db.SetSettingParams{Key: flag, Value: "1"}); err != nil {
					return err
				}
				return announceIncident(ctx, q, now.UnixMilli(), "network.silent", incident, map[string]string{"a": a.ID, "b": b.ID},
					app.NetworkSilent(names[0], names[1], fmt.Sprintf("no handshake for %d min (limit %d min)", int(quiet/time.Minute), int(silentAfter/time.Minute)), strconv.Itoa(wgPort), text), incident, "open", label, false)
			}); err != nil {
				return err
			}
		case quiet < silentAfter && open != "":
			if err := w.St.Tx(ctx, func(q *db.Queries) error {
				if err := q.DeleteSettingKey(ctx, flag); err != nil {
					return err
				}
				return announceIncident(ctx, q, now.UnixMilli(), "network.recovered", incident, nil,
					app.NetworkOK(names[0], names[1], names[0]+" reaches "+names[1]+" again", fmt.Sprintf("%s reaches %s on the private network again.", names[0], names[1])), incident, "close", label, false)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
