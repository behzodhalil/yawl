package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/spec"
)

func (r *Runner) buildServer(ctx context.Context, j *job, build []proto.SealedVar) (string, string, error) {
	name, err := r.St.Q.GetSetting(ctx, "build.server")
	if errors.Is(err, sql.ErrNoRows) || name == "" {
		return "", "", nil
	} else if err != nil {
		return "", "", err
	}
	bs, err := r.St.Q.GetServerByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	} else if err != nil {
		return "", "", err
	}
	if bs.ID == j.svc.ServerID {
		return "", "", nil
	}
	home, _ := r.St.Q.GetServer(ctx, j.svc.ServerID)
	if len(build) > 0 {
		return "", fmt.Sprintf("Built on %s, not %s: %s is a build variable sealed to %s.", home.Name, bs.Name, build[0].Key, home.Name), nil
	}
	if bs.Offline == 1 {
		return "", fmt.Sprintf("Built on %s: the build server %s is offline.", home.Name, bs.Name), nil
	}
	return bs.ID, "", nil
}

func (r *Runner) buildElsewhere(ctx context.Context, j *job, serverID string, ba proto.BuildArgs) (proto.Result, error) {
	repo, err := r.St.Q.GetSetting(ctx, "build.repository")
	if err != nil {
		return proto.Result{Cause: "a build server is set without a repository to push to; set one in Integrations, Builds"}, nil
	}
	ba.Push = &proto.PushTo{Ref: repo + ":" + j.d.ID}
	if r.V != nil {
		if ba.Push.Auth, err = app.RegistryAuth(ctx, r.St.Q, r.V, spec.RegistryHost(repo)); err != nil {
			return proto.Result{}, err
		}
	}
	cmd, err := proto.NewCommand(j.d.ID+":build@"+serverID, proto.CmdBuild, ba)
	if err != nil {
		return proto.Result{}, err
	}
	bctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	res, err := r.GW.Send(bctx, serverID, cmd)
	cancel()
	if err != nil || !res.OK {
		return res, err
	}
	pull, err := r.send(ctx, j, "build-pull", proto.CmdPull, proto.PullArgs{Ref: ba.Push.Ref, Tag: ba.Image, Auth: ba.Push.Auth}, 15*time.Minute)
	if err != nil || !pull.OK {
		return pull, err
	}
	var b proto.BuildData
	json.Unmarshal(res.Data, &b)
	b.Image = ba.Image
	res.Data, _ = json.Marshal(b)
	return res, nil
}

func withWarning(res proto.Result, w string) proto.Result {
	if !res.OK {
		return res
	}
	var b proto.BuildData
	json.Unmarshal(res.Data, &b)
	b.Warnings = append(b.Warnings, w)
	res.Data, _ = json.Marshal(b)
	return res
}

const rootMin = "0.1.13"

func (r *Runner) rootUnknown(ctx context.Context, root, serverID string) string {
	if root == "" {
		return ""
	}
	s, err := r.St.Q.GetServer(ctx, serverID)
	if err != nil || !olderThan(s.AgentVersion, rootMin) {
		return ""
	}
	return fmt.Sprintf("%s runs agent %s, which cannot build the folder %s; agents follow the panel on their next connect, so deploy again in a minute.", s.Name, s.AgentVersion, root)
}

func olderThan(v, min string) bool {
	a, b := strings.Split(v, "."), strings.Split(min, ".")
	for i := range b {
		if i >= len(a) {
			return true
		}
		x, err := strconv.Atoi(a[i])
		if err != nil {
			return false
		}
		if y, _ := strconv.Atoi(b[i]); x != y {
			return x < y
		}
	}
	return false
}
