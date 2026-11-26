package agentskill

import (
	"bytes"
	_ "embed"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed skill.md
var Body string

const summary = "Deploy, debug and roll back apps on Yawl: every yawl.yaml field, the yawl CLI and MCP tools, and the rules for using them."

var Paths = map[string]string{
	"claude": filepath.Join(".claude", "skills", "yawl", "SKILL.md"),
	"cursor": filepath.Join(".cursor", "rules", "yawl.mdc"),
	"codex":  "AGENTS.md",
}

const (
	sectionStart = "<!-- yawl:start -->"
	sectionEnd   = "<!-- yawl:end -->"
)

func Render(agent string, existing []byte) []byte {
	switch agent {
	case "claude":
		return []byte("---\nname: yawl\ndescription: " + summary + "\n---\n\n" + Body)
	case "cursor":
		return []byte("---\ndescription: " + summary + "\nglobs: yawl.yaml\nalwaysApply: false\n---\n\n" + Body)
	}
	section := sectionStart + "\n#" + strings.ReplaceAll(strings.TrimRight(Body, "\n"), "\n#", "\n##") + "\n" + sectionEnd
	if i, j := bytes.Index(existing, []byte(sectionStart)), bytes.Index(existing, []byte(sectionEnd)); i >= 0 && j > i {
		return append(append(append([]byte{}, existing[:i]...), section...), existing[j+len(sectionEnd):]...)
	}
	if len(bytes.TrimSpace(existing)) == 0 {
		return []byte(section + "\n")
	}
	return append(append(bytes.TrimRight(existing, "\n"), "\n\n"...), section+"\n"...)
}

func ServeText(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write([]byte(Body))
}
