package exec

import (
	"context"
	"errors"
	"net"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

const (
	tsContainer = "yawl-tailscale"
	tsVolume    = "yawl-tailscale-state"
	tsImage     = "yawl/tailscale:stable"
)

func tailnetAddr() string {
	i, err := net.InterfaceByName("tailscale0")
	if err != nil {
		return ""
	}
	addrs, _ := i.Addrs()
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil {
			return ip.IP.String()
		}
	}
	return ""
}

func (d *Dispatcher) addr() string {
	if d.TailnetAddr != nil {
		return d.TailnetAddr()
	}
	return tailnetAddr()
}

func (d *Dispatcher) tailscaleUp(ctx context.Context, a proto.TailscaleUpArgs) proto.Result {
	key, cause := d.open(proto.SealedVar{Key: "TS_AUTHKEY", Box: a.AuthKey})
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	if _, err := d.rt.Inspect(ctx, tsContainer); err != nil && d.addr() != "" { // a second tailscaled would fight the first
		return proto.Result{Cause: "Tailscale is already installed outside Yawl on this server; leave it there, or remove it first"}
	}
	d.rt.Stop(ctx, tsContainer, stopGrace)
	d.rt.Remove(ctx, tsContainer)
	d.rt.RemoveVolume(ctx, tsVolume)
	if _, err := d.rt.Pull(ctx, "tailscale/tailscale:stable", tsImage, "", ""); err != nil {
		return proto.Result{Cause: "could not pull tailscale/tailscale:stable: " + err.Error()}
	}
	_, err := d.rt.Start(ctx, runtime.ContainerSpec{Name: tsContainer, Image: tsImage, HostNetwork: true,
		// TS_AUTH_ONCE: the key logs in once; a restart uses the saved state, not a key that may have expired
		Env:     []string{"TS_AUTHKEY=" + string(key), "TS_HOSTNAME=" + a.Hostname, "TS_STATE_DIR=/var/lib/tailscale", "TS_USERSPACE=false", "TS_AUTH_ONCE=true"},
		Mounts:  []runtime.Mount{{Source: tsVolume, Target: "/var/lib/tailscale", Volume: true}},
		CapAdd:  []string{"NET_ADMIN", "NET_RAW"},
		Devices: []string{"/dev/net/tun"},
	})
	if err != nil {
		d.rt.Remove(ctx, tsContainer)
		d.rt.RemoveVolume(ctx, tsVolume)
		return proto.Result{Cause: err.Error()}
	}
	return proto.Result{OK: true}
}

func (d *Dispatcher) tailscaleDown(ctx context.Context) proto.Result {
	if err := d.rt.Stop(ctx, tsContainer, stopGrace); err != nil && !errors.Is(err, runtime.ErrNotFound) {
		return proto.Result{Cause: err.Error()}
	}
	d.rt.Remove(ctx, tsContainer)
	if err := d.rt.RemoveVolume(ctx, tsVolume); err != nil {
		return proto.Result{Cause: err.Error()}
	}
	return proto.Result{OK: true}
}

func (d *Dispatcher) tailscaleStatus(ctx context.Context) proto.Result {
	st, err := d.rt.Inspect(ctx, tsContainer)
	out := proto.TailscaleStatus{Running: err == nil && st.Running}
	switch {
	case out.Running:
		out.Address = d.addr()
	case err != nil:
		out.Address = d.addr()
		out.Outside = out.Address != ""
	}
	return ok(out)
}
