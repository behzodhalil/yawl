package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type Fake struct {
	Cleanups []CleanupOpts
	Unused   []string

	Execs  []string
	OnExec func(container string, cmd []string) (Shell, error)

	mu              sync.Mutex
	n               int
	Started         []ContainerSpec
	Stopped         []string
	Graces          []time.Duration
	Networks        []string
	Pruned          []string
	States          map[string]State
	Tails           map[string]string
	Listens         map[string][]Listen
	OnStart         func(id string, s ContainerSpec) State
	StartErr        error
	StartHook       func(s ContainerSpec) error
	FailAfterCreate func(s ContainerSpec) error
	Ran             []Run
	Volumes         map[string]bool
	names           map[string]string
	OnRun           func(s ContainerSpec, stdin []byte) (stdout string, exit int, stderr string, err error)
	Stat            map[string]Stats
	PrepareErr      error
	Pushed          []string
	PushErr         error
	LogLines        map[string][]LogLine
	Digest          string
	Pulled          []string
	PullErr         error
}

func (f *Fake) Pull(_ context.Context, ref, tag, user, token string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Pulled = append(f.Pulled, strings.TrimSpace(ref+" "+tag+" "+user+" "+token))
	return f.Digest, f.PullErr
}

func (f *Fake) NetworkInfo(context.Context, string) (netip.Prefix, netip.Addr, error) {
	return netip.MustParsePrefix("172.20.0.0/16"), netip.MustParseAddr("172.20.0.1"), nil
}

func (f *Fake) List(_ context.Context, labels map[string]string) ([]Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Container
next:
	for i, s := range f.Started {
		for k, v := range labels {
			if s.Labels[k] != v {
				continue next
			}
		}
		id := fmt.Sprintf("c%d", i+1)
		out = append(out, Container{ID: id, Name: s.Name, Image: s.Image, Labels: s.Labels, Created: time.Unix(int64(i), 0), Running: f.States[id].Running})
	}
	return out, nil
}

func (f *Fake) Logs(_ context.Context, id string, since time.Time, fn func(LogLine)) error {
	f.mu.Lock()
	lines := append([]LogLine(nil), f.LogLines[id]...)
	f.mu.Unlock()
	for _, l := range lines {
		if l.Time.After(since) {
			fn(l)
		}
	}
	return nil
}

func (f *Fake) Prepare(context.Context, string) error { return f.PrepareErr }

func (f *Fake) PushAndUntag(_ context.Context, src, ref, user, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.PushErr != nil {
		return f.PushErr
	}
	f.Pushed = append(f.Pushed, strings.TrimSpace(src+" "+ref+" "+user+" "+token))
	return nil
}

func (f *Fake) Follow(ctx context.Context, id string, since time.Time, fn func(LogLine)) error {
	return f.Logs(ctx, id, since, fn)
}

type Run struct {
	Spec  ContainerSpec
	Stdin []byte
}

func (f *Fake) EnsureNetwork(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Networks = append(f.Networks, name)
	return nil
}

func (f *Fake) Start(_ context.Context, s ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StartErr != nil {
		return "", f.StartErr
	}
	if f.StartHook != nil {
		if err := f.StartHook(s); err != nil {
			return "", err
		}
	}
	f.n++
	id := fmt.Sprintf("c%d", f.n)
	f.Started = append(f.Started, s)
	if f.names == nil {
		f.names = map[string]string{}
	}
	if s.Name != "" {
		f.names[s.Name] = id
	}
	for _, m := range s.Mounts {
		if m.Volume {
			if f.Volumes == nil {
				f.Volumes = map[string]bool{}
			}
			f.Volumes[m.Source] = true
		}
	}
	if f.States == nil {
		f.States = map[string]State{}
	}
	st := State{Running: true, PID: 100 + f.n}
	if f.OnStart != nil {
		st = f.OnStart(id, s)
	}
	st.Labels = s.Labels
	f.States[id] = st
	if f.FailAfterCreate != nil {
		if err := f.FailAfterCreate(s); err != nil {
			return id, err
		}
	}
	return id, nil
}

func (f *Fake) Stop(_ context.Context, id string, grace time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, named := f.names[id]; !named {
		if _, ok := f.States[id]; !ok {
			return ErrNotFound
		}
	}
	f.Stopped, f.Graces = append(f.Stopped, id), append(f.Graces, grace)
	if n, ok := f.names[id]; ok {
		id = n
	}
	st := f.States[id]
	st.Running = false
	f.States[id] = st
	return nil
}

func (f *Fake) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.names[id]; ok {
		delete(f.names, id)
		id = n
	}
	delete(f.States, id)
	return nil
}

func (f *Fake) VolumeExists(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Volumes[name], nil
}

func (f *Fake) Inspect(_ context.Context, id string) (State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.names[id]; ok {
		id = n
	}
	st, ok := f.States[id]
	if !ok {
		return State{}, errors.New("no such container")
	}
	return st, nil
}

func (f *Fake) Tail(_ context.Context, id string, _ int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Tails[id], nil
}

func (f *Fake) Listening(_ context.Context, id string) ([]Listen, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Listens[id], nil
}

func (f *Fake) PruneImages(_ context.Context, repo string, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Pruned = append(f.Pruned, repo)
	return nil
}

func (f *Fake) Set(id string, st State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.States[id] = st
}

func (f *Fake) RunOnce(ctx context.Context, s ContainerSpec, stdin io.Reader, stdout io.Writer) (int, string, error) {
	var in []byte
	if stdin != nil {
		in, _ = io.ReadAll(stdin)
	}
	f.mu.Lock()
	f.Ran = append(f.Ran, Run{Spec: s, Stdin: in})
	on := f.OnRun
	f.mu.Unlock()
	if on == nil {
		return 0, "", nil
	}
	type ran struct {
		out, stderr string
		exit        int
		err         error
	}
	done := make(chan ran, 1)
	go func() { out, exit, stderr, err := on(s, in); done <- ran{out, stderr, exit, err} }()
	var r ran
	select {
	case r = <-done:
	case <-ctx.Done():
		return 0, "", ctx.Err()
	}
	if stdout != nil && r.err == nil {
		if _, err := io.WriteString(stdout, r.out); err != nil {
			return 0, "", err
		}
	}
	return r.exit, r.stderr, r.err
}

func (f *Fake) Stats(_ context.Context, id string) (Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Stat[id], nil
}

func (f *Fake) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Volumes, name)
	return nil
}

func (f *Fake) Exec(_ context.Context, container string, cmd []string, cols, rows uint) (Shell, error) {
	f.mu.Lock()
	f.Execs = append(f.Execs, container)
	on := f.OnExec
	f.mu.Unlock()
	if on != nil {
		return on(container, cmd)
	}
	return nil, ErrNotFound
}

func (f *Fake) Cleanup(_ context.Context, o CleanupOpts) (CleanupResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Cleanups = append(f.Cleanups, o)
	return CleanupResult{Containers: 2, Images: 1, Freed: 3 << 20}, nil
}

func (f *Fake) UnusedVolumes(context.Context) ([]string, error) { return f.Unused, nil }
