package deploy

import (
	"slices"
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

func TestWebOrder(t *testing.T) {
	if got, want := Steps(spec.KindWeb), []Step{StepBuild, StepStart, StepCheck, StepSwitch, StepDrain}; !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestWorkerOrder(t *testing.T) {
	if got, want := Steps(spec.KindWorker), []Step{StepBuild, StepStopOld, StepStart, StepCheck}; !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestNoSkip(t *testing.T) {
	for _, k := range []spec.Kind{spec.KindWeb, spec.KindWorker} {
		s := Steps(k)
		check := slices.Index(s, StepCheck)
		if check < 0 {
			t.Fatalf("%s has no check", k)
		}
		if sw := slices.Index(s, StepSwitch); sw >= 0 && sw < check {
			t.Fatalf("%s switches before checking", k)
		}
		if start := slices.Index(s, StepStart); start > check {
			t.Fatalf("%s checks before starting", k)
		}
	}
}

func TestFailBeforeSwitchKeepsOld(t *testing.T) {
	for _, s := range []Step{StepBuild, StepStart, StepCheck, StepSwitch} {
		if OnFail(s) != Failed {
			t.Fatalf("%s: %s", s, OnFail(s))
		}
	}
}

func TestFailWhileDrainingRollsBack(t *testing.T) {
	if OnFail(StepDrain) != RolledBack {
		t.Fatal(OnFail(StepDrain))
	}
}

func TestWorkerFailAfterStopRestoresOld(t *testing.T) {
	if OnFailFor(spec.KindWorker, StepStart) != RolledBack || OnFailFor(spec.KindWorker, StepCheck) != RolledBack {
		t.Fatal("worker failure after stop_old must roll back")
	}
	if OnFailFor(spec.KindWorker, StepBuild) != Failed {
		t.Fatal("worker build failure leaves the old one running")
	}
}

func TestTerminal(t *testing.T) {
	for _, s := range []State{Live, Failed, RolledBack, Superseded} {
		if !Terminal(s) {
			t.Fatal(s)
		}
	}
	if Terminal(Queued) || Terminal(Checking) {
		t.Fatal("queued/checking are not terminal")
	}
}

func TestPostgresSteps(t *testing.T) {
	if got := StepsFor(spec.KindPostgres, Opts{}); !slices.Equal(got, []Step{StepEnsure}) {
		t.Fatalf("got %v", got)
	}
	if got := StepsFor(spec.KindPostgres, Opts{Restore: true}); !slices.Equal(got, []Step{StepEnsure, StepRestore}) {
		t.Fatalf("got %v", got)
	}
	if !slices.Equal(Steps(spec.KindWeb), StepsFor(spec.KindWeb, Opts{})) {
		t.Fatal("Steps and StepsFor disagree for web")
	}
	for _, s := range []Step{StepEnsure, StepRestore} {
		if OnFailFor(spec.KindPostgres, s) != Failed {
			t.Fatalf("%s failure: %s", s, OnFailFor(spec.KindPostgres, s))
		}
	}
}

func allOpts() []Opts {
	var out []Opts
	for i := 0; i < 8; i++ {
		out = append(out, Opts{Release: i&1 != 0, Snapshot: i&2 != 0, Rollback: i&4 != 0})
	}
	return out
}

func TestStepsTable(t *testing.T) {
	web := map[Opts][]Step{
		{}:                              {StepBuild, StepStart, StepCheck, StepSwitch, StepDrain},
		{Release: true}:                 {StepBuild, StepRelease, StepStart, StepCheck, StepSwitch, StepDrain},
		{Release: true, Snapshot: true}: {StepBuild, StepSnapshot, StepRelease, StepStart, StepCheck, StepSwitch, StepDrain},
		{Snapshot: true}:                {StepBuild, StepStart, StepCheck, StepSwitch, StepDrain},
	}
	worker := map[Opts][]Step{
		{}:                              {StepBuild, StepStopOld, StepStart, StepCheck},
		{Release: true}:                 {StepBuild, StepRelease, StepStopOld, StepStart, StepCheck},
		{Release: true, Snapshot: true}: {StepBuild, StepSnapshot, StepRelease, StepStopOld, StepStart, StepCheck},
		{Snapshot: true}:                {StepBuild, StepStopOld, StepStart, StepCheck},
	}
	for _, o := range allOpts() {
		wantWeb, wantWorker := web[Opts{Release: o.Release, Snapshot: o.Snapshot}], worker[Opts{Release: o.Release, Snapshot: o.Snapshot}]
		if o.Rollback {
			wantWeb, wantWorker = []Step{StepStart, StepCheck, StepSwitch, StepDrain}, []Step{StepStopOld, StepStart, StepCheck}
		}
		if got := StepsFor(spec.KindWeb, o); !slices.Equal(got, wantWeb) {
			t.Fatalf("web %+v: %v", o, got)
		}
		if got := StepsFor(spec.KindWorker, o); !slices.Equal(got, wantWorker) {
			t.Fatalf("worker %+v: %v", o, got)
		}
	}
}

func TestP3NoReleaseBeforeSnapshot(t *testing.T) {
	for _, k := range []spec.Kind{spec.KindWeb, spec.KindWorker} {
		for _, o := range allOpts() {
			s := StepsFor(k, o)
			if o.Snapshot && o.Release && !o.Rollback {
				if i, j := slices.Index(s, StepSnapshot), slices.Index(s, StepRelease); i < 0 || j < 0 || i > j {
					t.Fatalf("%s %+v: %v", k, o, s)
				}
			}
			if sw, c := slices.Index(s, StepSwitch), slices.Index(s, StepCheck); sw >= 0 && sw < c {
				t.Fatalf("%s %+v switches before checking", k, o)
			}
		}
	}
}

func TestOnFailSnapshotRelease(t *testing.T) {
	for _, k := range []spec.Kind{spec.KindWeb, spec.KindWorker} {
		for _, s := range []Step{StepSnapshot, StepRelease} {
			if OnFailFor(k, s) != Failed {
				t.Fatalf("%s %s: %s", k, s, OnFailFor(k, s))
			}
		}
	}
	if After(StepSnapshot) != Snapshotting || After(StepRelease) != Releasing {
		t.Fatal("states")
	}
}

func TestCronOrder(t *testing.T) {
	if got, want := StepsFor(spec.KindCron, Opts{}), []Step{StepBuild, StepSchedule}; !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if got, want := StepsFor(spec.KindCron, Opts{Rollback: true}), []Step{StepSchedule}; !slices.Equal(got, want) {
		t.Fatalf("rollback got %v", got)
	}
	if OnFailFor(spec.KindCron, StepSchedule) != Failed || After(StepSchedule) != Starting {
		t.Fatal("schedule step states")
	}
}

func TestVolumeWebOrder(t *testing.T) {
	if got, want := StepsFor(spec.KindWeb, Opts{Volumes: true}), []Step{StepBuild, StepStopOld, StepStart, StepCheck, StepSwitch}; !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	for _, s := range []Step{StepStart, StepCheck, StepSwitch} {
		if OnFailWith(spec.KindWeb, Opts{Volumes: true}, s) != RolledBack {
			t.Fatalf("%s: the old version was stopped; it must be started again", s)
		}
	}
	if OnFailWith(spec.KindWeb, Opts{}, StepStart) != Failed {
		t.Fatal("blue/green start failure keeps the old one serving")
	}
}

func TestVolumeRestoreSteps(t *testing.T) {
	web := StepsFor(spec.KindWeb, Opts{Restore: true, Rollback: true, Volumes: true})
	if want := []Step{StepStopOld, StepRestoreVolume, StepStart, StepCheck, StepSwitch}; !slices.Equal(web, want) {
		t.Fatalf("web %v", web)
	}
	if got := StepsFor(spec.KindWorker, Opts{Restore: true, Rollback: true}); !slices.Equal(got, []Step{StepStopOld, StepRestoreVolume, StepStart, StepCheck}) {
		t.Fatalf("worker %v", got)
	}
	for _, s := range []Step{StepRestoreVolume, StepStart, StepCheck, StepSwitch} {
		if OnFailWith(spec.KindWorker, Opts{Restore: true}, s) != RolledBack || OnFailWith(spec.KindWeb, Opts{Restore: true, Volumes: true}, s) != RolledBack {
			t.Fatalf("%s not rolled back", s)
		}
	}
	if OnFailWith(spec.KindWeb, Opts{Restore: true, Volumes: true}, StepStopOld) != Failed {
		t.Fatal("a failed stop has nothing to put back")
	}
}

func TestVolumeReleaseAfterStop(t *testing.T) {
	o := Opts{Volumes: true, Release: true, Snapshot: true}
	if got, want := StepsFor(spec.KindWeb, o), []Step{StepBuild, StepSnapshot, StepStopOld, StepRelease, StepStart, StepCheck, StepSwitch}; !slices.Equal(got, want) {
		t.Fatalf("web %v", got)
	}
	if got, want := StepsFor(spec.KindWorker, o), []Step{StepBuild, StepSnapshot, StepStopOld, StepRelease, StepStart, StepCheck}; !slices.Equal(got, want) {
		t.Fatalf("worker %v", got)
	}
	if got, want := StepsFor(spec.KindWorker, Opts{Release: true}), []Step{StepBuild, StepRelease, StepStopOld, StepStart, StepCheck}; !slices.Equal(got, want) {
		t.Fatalf("worker without volumes %v", got)
	}
	for _, k := range []spec.Kind{spec.KindWeb, spec.KindWorker} {
		if OnFailWith(k, o, StepRelease) != RolledBack {
			t.Fatalf("%s: a failed release after the stop must start the old version", k)
		}
	}
	if OnFailWith(spec.KindWeb, Opts{Release: true}, StepRelease) != Failed {
		t.Fatal("without volumes the old version never stopped")
	}
}

func TestPullOrder(t *testing.T) {
	for _, c := range []struct {
		k    spec.Kind
		o    Opts
		want []Step
	}{
		{spec.KindWeb, Opts{Pull: true}, []Step{StepPull, StepStart, StepCheck, StepSwitch, StepDrain}},
		{spec.KindWeb, Opts{Pull: true, Release: true}, []Step{StepPull, StepRelease, StepStart, StepCheck, StepSwitch, StepDrain}},
		{spec.KindCron, Opts{Pull: true}, []Step{StepPull, StepSchedule}},
		{spec.KindWeb, Opts{Pull: true, Rollback: true}, []Step{StepStart, StepCheck, StepSwitch, StepDrain}},
	} {
		if got := StepsFor(c.k, c.o); !slices.Equal(got, c.want) {
			t.Errorf("%s %+v: %v", c.k, c.o, got)
		}
	}
}

func TestValkeySteps(t *testing.T) {
	if got := StepsFor(spec.KindValkey, Opts{}); !slices.Equal(got, []Step{StepEnsure}) {
		t.Fatalf("got %v", got)
	}
	if got := StepsFor(spec.KindValkey, Opts{Restore: true}); !slices.Equal(got, []Step{StepEnsure, StepRestore}) {
		t.Fatalf("got %v", got)
	}
	if OnFailWith(spec.KindValkey, Opts{Restore: true}, StepRestore) != Failed {
		t.Fatal("a failed restore of a new database is failed, not rolled back")
	}
}

func TestImportSteps(t *testing.T) {
	for _, k := range []spec.Kind{spec.KindPostgres, "mysql", "mongodb"} {
		if got := StepsFor(k, Opts{Import: true}); !slices.Equal(got, []Step{StepEnsure, StepSnapshot, StepImport}) {
			t.Fatalf("%s: %v", k, got)
		}
	}
}
