package runtime

import (
	"testing"

	"github.com/moby/moby/api/types/container"
)

func TestStatsRaw(t *testing.T) {
	var st container.StatsResponse
	st.CPUStats.CPUUsage.TotalUsage, st.CPUStats.SystemUsage, st.CPUStats.OnlineCPUs = 3_000, 20_000, 4
	st.MemoryStats.Usage = 500
	st.MemoryStats.Stats = map[string]uint64{"inactive_file": 200}
	if got := statsOf(st); got != (Stats{CPU: 3_000, System: 20_000, CPUs: 4, Mem: 300}) {
		t.Fatalf("%+v", got)
	}
}
