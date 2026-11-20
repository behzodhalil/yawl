package spec

import (
	"bytes"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func Marshal(f File) ([]byte, error) {
	doc := &yaml.Node{Kind: yaml.MappingNode}
	add := func(m *yaml.Node, key string, v any) error {
		var n yaml.Node
		if err := n.Encode(v); err != nil {
			return err
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &n)
		return nil
	}
	for _, kv := range [][2]string{{"project", f.Project}, {"environment", f.Environment}, {"server", f.Server}} {
		if kv[1] != "" {
			add(doc, kv[0], kv[1])
		}
	}
	if f.Preview {
		add(doc, "preview", true)
	}
	if f.PreviewDomain != "" {
		add(doc, "preview_domain", f.PreviewDomain)
	}
	list := &yaml.Node{Kind: yaml.SequenceNode}
	for _, s := range f.Services {
		m := &yaml.Node{Kind: yaml.MappingNode}
		v, t := reflect.ValueOf(s), reflect.TypeOf(s)
		for i := range t.NumField() {
			fv, ft := v.Field(i), t.Field(i)
			key, _, _ := strings.Cut(ft.Tag.Get("yaml"), ",")
			if key == "" || key == "-" || !ft.IsExported() {
				continue
			}
			if ft.Name == "AutoDeploy" {
				if !s.AutoDeploy {
					add(m, key, false)
				}
				continue
			}
			if fv.IsZero() || (fv.Kind() == reflect.Slice && fv.Len() == 0) {
				continue
			}
			val := fv.Interface()
			switch x := val.(type) {
			case time.Duration:
				val = x.String()
			case Size:
				val = x.String()
			case Cores:
				val = float64(x)
			}
			if err := add(m, key, val); err != nil {
				return nil, err
			}
		}
		list.Content = append(list.Content, m)
	}
	doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "services"}, list)
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
