package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		r, w, _ := os.Pipe()
		out, args := os.Stdout, os.Args
		os.Stdout, os.Args = w, []string{"yawl", arg}
		main()
		w.Close()
		os.Stdout, os.Args = out, args
		b, _ := io.ReadAll(r)
		if !strings.Contains(string(b), "mcp ") {
			t.Fatalf("%s: %q", arg, b)
		}
	}
}
