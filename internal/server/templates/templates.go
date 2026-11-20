package templates

import (
	"embed"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/behzodhalil/yawl/internal/spec"
	"gopkg.in/yaml.v3"
)

//go:embed *.yaml
var files embed.FS

// Icons come from thesvg.org (CC0-1.0), named as there; they identify the
// software and are drawn inline, so a test keeps them free of anything that runs.
//
//go:embed icons/*.svg
var icons embed.FS

type Var struct {
	Service string `yaml:"service" json:"service"`
	Key     string `yaml:"key" json:"key"`
	Kind    string `yaml:"kind" json:"kind"`
	Value   string `yaml:"value" json:"value,omitempty"`
	Label   string `yaml:"label" json:"label,omitempty"`
}

type Template struct {
	ID, Title, Line, Note string
	Memory                string
	Icons                 []string
	Services              []spec.Service
	Vars                  []Var
}

var all = load()

func load() []Template {
	entries, _ := files.ReadDir(".")
	var out []Template
	for _, e := range entries {
		b, _ := files.ReadFile(e.Name())
		var raw struct {
			ID, Title, Line, Note string
			Icons                 []string
			Services              yaml.Node
			Vars                  []Var
		}
		if err := yaml.Unmarshal(b, &raw); err != nil {
			panic(fmt.Sprintf("template %s: %v", e.Name(), err))
		}
		file, _ := yaml.Marshal(map[string]any{"project": "t", "environment": "production", "server": "s", "services": &raw.Services})
		f, err := spec.Parse(file)
		if err != nil {
			panic(fmt.Sprintf("template %s: %v", e.Name(), err))
		}
		var mem spec.Size
		for _, s := range f.Services {
			mem += s.Memory
		}
		if mem >= 1e9 {
			mem = spec.Size(math.Round(float64(mem)/1e8) * 1e8)
		}
		var svgs []string
		for _, name := range raw.Icons {
			svg, err := icons.ReadFile("icons/" + name + ".svg")
			if err != nil {
				panic(fmt.Sprintf("template %s: icon %s: %v", e.Name(), name, err))
			}
			svgs = append(svgs, strings.TrimSpace(string(svg)))
		}
		out = append(out, Template{ID: raw.ID, Title: raw.Title, Line: raw.Line, Note: strings.TrimSpace(raw.Note), Memory: mem.String(), Icons: svgs, Services: f.Services, Vars: raw.Vars})
	}
	slices.SortFunc(out, func(a, b Template) int { return strings.Compare(a.Title, b.Title) })
	return out
}

func All() []Template { return all }

func Get(id string) (Template, bool) {
	i := slices.IndexFunc(all, func(t Template) bool { return t.ID == id })
	if i < 0 {
		return Template{}, false
	}
	return all[i], true
}
