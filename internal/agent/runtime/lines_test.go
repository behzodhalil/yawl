package runtime

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func TestReadLogs(t *testing.T) {
	var raw bytes.Buffer
	frame := func(fd byte) func(string) { // Docker's framing: stream, three zeros, big-endian size
		return func(s string) {
			raw.Write(binary.BigEndian.AppendUint32([]byte{fd, 0, 0, 0}, uint32(len(s))))
			raw.WriteString(s)
		}
	}
	out, errw := frame(1), frame(2)
	ts := func(s int) string {
		return time.Date(2026, 11, 5, 10, 0, s, 0, time.UTC).Format(time.RFC3339Nano) + " "
	}
	out(ts(0) + "too old\n")
	out(ts(1) + "a\n")
	errw(ts(2) + "boom\r\n")
	out(ts(3) + strings.Repeat("x", 3<<20))
	out("y\n")
	errw(ts(4) + "half")
	errw(" line\n")
	var got []LogLine
	err := readLogs(&raw, time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC), func(l LogLine) { got = append(got, l) })
	if err != nil || len(got) != 4 || got[0].Text != "a" || got[0].Stream != "" || got[1].Text != "boom" || got[1].Stream != "stderr" ||
		len(got[2].Text) != 256<<10-len(ts(3)) || got[2].Stream != "" || got[3].Text != "half line" || got[3].Stream != "stderr" {
		t.Fatalf("%v %d", err, len(got))
	}
}
