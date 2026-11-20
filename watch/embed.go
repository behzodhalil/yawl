package watch

import (
	"embed"
	"errors"
)

//go:embed all:dist
var dist embed.FS

//go:embed migrations/0001_init.sql
var schema string

var ErrNotBuilt = errors.New("The watcher is not built. Run make watch.")

func Code() ([]byte, error) {
	b, err := dist.ReadFile("dist/index.js")
	if err != nil || len(b) == 0 {
		return nil, ErrNotBuilt
	}
	return b, nil
}

// Schema is the D1 schema, safe to apply again.
func Schema() string { return schema }
