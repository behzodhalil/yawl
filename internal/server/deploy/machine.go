package deploy

import "github.com/behzodhalil/yawl/internal/spec"

type State string

const (
	Queued       State = "queued"
	Building     State = "building"
	Pulling      State = "pulling"
	Stopping     State = "stopping"
	Starting     State = "starting"
	Checking     State = "checking"
	Switching    State = "switching"
	Draining     State = "draining"
	Restoring    State = "restoring"
	Snapshotting State = "snapshotting"
	Releasing    State = "releasing"
	Live         State = "live"
	Failed       State = "failed"
	RolledBack   State = "rolled_back"
	Superseded   State = "superseded"
)

type Step string

const (
	StepBuild         Step = "build"
	StepPull          Step = "pull"
	StepStopOld       Step = "stop_old"
	StepStart         Step = "start"
	StepCheck         Step = "check"
	StepSwitch        Step = "switch"
	StepDrain         Step = "drain"
	StepEnsure        Step = "ensure"
	StepRestore       Step = "restore"
	StepRestoreVolume Step = "restore_volume"
	StepImport        Step = "import"
	StepSnapshot      Step = "snapshot"
	StepRelease       Step = "release"
	StepSchedule      Step = "schedule"
)

func Steps(k spec.Kind) []Step { return StepsFor(k, Opts{}) }

type Opts struct {
	Restore  bool
	Import   bool
	Release  bool
	Snapshot bool
	Rollback bool
	Pull     bool
	Volumes  bool
}

func StepsFor(k spec.Kind, o Opts) []Step {
	first := StepBuild
	if o.Pull {
		first = StepPull
	}
	if spec.IsDatabase(k) {
		if o.Import {
			return []Step{StepEnsure, StepSnapshot, StepImport}
		}
		if o.Restore {
			return []Step{StepEnsure, StepRestore}
		}
		return []Step{StepEnsure}
	}
	if k == spec.KindCron {
		if o.Rollback {
			return []Step{StepSchedule}
		}
		return []Step{first, StepSchedule}
	}
	if o.Restore {
		if k == spec.KindWorker {
			return []Step{StepStopOld, StepRestoreVolume, StepStart, StepCheck}
		}
		return []Step{StepStopOld, StepRestoreVolume, StepStart, StepCheck, StepSwitch}
	}
	var pre []Step
	release := o.Release && !o.Rollback
	if !o.Rollback {
		pre = append(pre, first)
		if release && o.Snapshot {
			pre = append(pre, StepSnapshot)
		}
		if release && !o.Volumes {
			pre = append(pre, StepRelease)
		}
	}
	stop := []Step{StepStopOld}
	if release && o.Volumes {
		stop = append(stop, StepRelease)
	}
	if k == spec.KindWorker {
		return append(append(pre, stop...), StepStart, StepCheck)
	}
	if o.Volumes {
		return append(append(pre, stop...), StepStart, StepCheck, StepSwitch)
	}
	return append(pre, StepStart, StepCheck, StepSwitch, StepDrain)
}

func After(s Step) State {
	return map[Step]State{StepBuild: Building, StepPull: Pulling, StepStopOld: Stopping, StepStart: Starting,
		StepCheck: Checking, StepSwitch: Switching, StepDrain: Draining, StepEnsure: Starting, StepRestore: Restoring, StepRestoreVolume: Restoring, StepImport: Restoring,
		StepSnapshot: Snapshotting, StepRelease: Releasing, StepSchedule: Starting}[s]
}

func OnFail(s Step) State { return OnFailFor(spec.KindWeb, s) }

func OnFailFor(k spec.Kind, s Step) State {
	if k == spec.KindWorker && (s == StepStart || s == StepCheck) {
		return RolledBack
	}
	if s == StepDrain {
		return RolledBack
	}
	return Failed
}

func OnFailWith(k spec.Kind, o Opts, s Step) State {
	if o.Volumes && k == spec.KindWeb && (s == StepStart || s == StepCheck || s == StepSwitch) {
		return RolledBack
	}
	if o.Volumes && s == StepRelease {
		return RolledBack
	}
	if o.Restore && !spec.IsDatabase(k) && s != StepStopOld {
		return RolledBack
	}
	return OnFailFor(k, s)
}

func Terminal(s State) bool {
	return s == Live || s == Failed || s == RolledBack || s == Superseded
}
