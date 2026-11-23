package spec

import (
	"fmt"
	"regexp"
	"strings"
)

// A reference as Docker reads it: an optional registry host (with a port), a
// lowercase path, an optional tag and an optional sha256 digest. Host labels start and end alphanumeric; path parts are joined
// by ".", "_", "__" or dashes, as in Docker's reference grammar.
var (
	imageHost = `[a-z0-9](?:[a-z0-9-]*[a-z0-9])?`
	imagePart = `[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*`
	imageRe   = regexp.MustCompile(`^(?:` + imageHost + `(?:\.` + imageHost + `)*(?::[0-9]+)?/)?` + imagePart + `(?:/` + imagePart + `)*(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(?:@sha256:[a-f0-9]{64})?$`)
)

// RegistryHost is the registry a reference is pulled from: its first part
// when that looks like a host (a dot, a port, or localhost), else docker.io.
func RegistryHost(ref string) string {
	first, _, ok := strings.Cut(ref, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return first
	}
	return "docker.io"
}

func (s Service) validateImage() error {
	switch {
	case s.Image == "":
		return nil
	case IsDatabase(s.Kind):
		return fmt.Errorf(`%s: a database has no "image"`, s.Name)
	case s.Repo != "":
		return fmt.Errorf(`%s: "repo" and "image" cannot both be set`, s.Name)
	case !imageRe.MatchString(s.Image):
		return fmt.Errorf(`%s: "image" must be a reference like grafana/grafana:11, got %q`, s.Name, s.Image)
	}
	return nil
}

func ImageName(ref string) string {
	name, _, _ := strings.Cut(ref, "@")
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i]
	}
	return name
}
