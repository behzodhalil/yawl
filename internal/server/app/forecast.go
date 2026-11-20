package app

import (
	"time"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

const BytesGB = 1e9

func Forecast(rows []db.MetricHour, total uint64) (days int, gbPerDay float64, growing bool) {
	if len(rows) < 24 || total == 0 {
		return 0, 0, false
	}
	t0 := rows[0].At
	var n, sx, sy, sxx, sxy float64
	for _, r := range rows {
		x := float64(r.At-t0) / float64(time.Hour/time.Millisecond)
		y := float64(r.Disk)
		n, sx, sy, sxx, sxy = n+1, sx+x, sy+y, sxx+x*x, sxy+x*y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, 0, false
	}
	perHour := (n*sxy - sx*sy) / den
	gbPerDay = perHour * 24 / BytesGB
	if gbPerDay <= 0.1 {
		return 0, gbPerDay, false
	}
	left := float64(total) - float64(rows[len(rows)-1].Disk)
	return max(0, int(left/(perHour*24))), gbPerDay, true
}
