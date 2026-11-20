package spec

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var pathRe = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)+$`)

var rootRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

func ValidRoot(r string) bool {
	parts := strings.Split(r, "/")
	return rootRe.MatchString(r) && !slices.Contains(parts, ".") && !slices.Contains(parts, "..")
}

func SplitDomain(entry string) (host, path string) {
	host, rest, ok := strings.Cut(entry, "/")
	if ok {
		path = "/" + rest
	}
	return host, path
}

func PathsNest(a, b string) bool {
	return a != "" && b != "" && (a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/"))
}

func validatePaths(svcs []Service) error {
	type at struct{ svc, entry, path string }
	byHost := map[string][]at{}
	for _, s := range svcs {
		for _, d := range s.Domains {
			host, path := SplitDomain(d)
			for _, o := range byHost[host] {
				if PathsNest(path, o.path) {
					return fmt.Errorf(`%s: "domains" entry %q overlaps %q; one host cannot have a path inside another`, s.Name, d, o.entry)
				}
			}
			byHost[host] = append(byHost[host], at{s.Name, d, path})
		}
	}
	for _, s := range svcs {
		for _, d := range s.Domains {
			bare, isWWW := strings.CutPrefix(d, "www.")
			gone := ""
			switch {
			case strings.Contains(d, "/"):
			case s.WWW == "bare" && isWWW && slices.Contains(s.Domains, bare):
				gone = d
			case s.WWW == "www" && !isWWW && slices.Contains(s.Domains, "www."+d):
				gone = d
			}
			for _, o := range byHost[gone] {
				if o.path != "" {
					return fmt.Errorf(`%s: "www" redirects all of %s, so it cannot have the path %q`, s.Name, gone, o.entry)
				}
			}
		}
	}
	return nil
}
