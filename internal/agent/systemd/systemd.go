package systemd

import (
	"context"

	"github.com/godbus/dbus/v5"
)

const (
	dest    = "org.freedesktop.systemd1"
	manager = "org.freedesktop.systemd1.Manager"
)

type Units struct{}

func call(ctx context.Context, f func(*dbus.Conn) error) error {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return err
	}
	defer conn.Close()
	return f(conn)
}

func (Units) Start(ctx context.Context, name string) error {
	return call(ctx, func(c *dbus.Conn) error {
		var job dbus.ObjectPath
		return c.Object(dest, "/org/freedesktop/systemd1").CallWithContext(ctx, manager+".StartUnit", 0, name, "replace").Store(&job)
	})
}

func (Units) State(ctx context.Context, name string) string {
	var state string
	call(ctx, func(c *dbus.Conn) error {
		var path dbus.ObjectPath
		if err := c.Object(dest, "/org/freedesktop/systemd1").CallWithContext(ctx, manager+".LoadUnit", 0, name).Store(&path); err != nil {
			return err
		}
		var v dbus.Variant // GetProperty takes no context: a hung systemd would hold the command (Plan 60 review)
		err := c.Object(dest, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.systemd1.Unit", "ActiveState").Store(&v)
		if err == nil {
			state, _ = v.Value().(string)
		}
		return err
	})
	return state
}
