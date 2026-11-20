package spec

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Cores float64

func (c Cores) String() string {
	if c == 0 {
		return "none"
	}
	return strconv.FormatFloat(float64(c), 'f', -1, 64)
}

type Size int64

func (s Size) String() string {
	switch {
	case s == 0:
		return "none"
	case s >= 1e9:
		return strconv.FormatFloat(float64(s)/1e9, 'f', -1, 64) + "GB"
	}
	return strconv.FormatFloat(float64(s)/1e6, 'f', -1, 64) + "MB"
}

var sizeRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(mb|gb)$`)

func ParseSize(text string) (Size, error) {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return 0, nil
	}
	m := sizeRe.FindStringSubmatch(t)
	if m == nil {
		return 0, fmt.Errorf(`"memory" must be a size like 512MB or 1.5GB, got %q`, text)
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	unit := 1e6
	if m[2] == "gb" {
		unit = 1e9
	}
	if v*unit >= math.MaxInt64 { // review M4 (plan 21): past int64 the conversion wraps or saturates
		return 0, fmt.Errorf(`"memory" %q is too large`, text)
	}
	return Size(math.Round(v * unit)), nil
}

func (s *Size) UnmarshalYAML(n *yaml.Node) error {
	var t string
	if err := n.Decode(&t); err != nil {
		return err
	}
	v, err := ParseSize(t)
	*s = v
	return err
}

func (s Service) validateLimits() error {
	if math.IsNaN(float64(s.CPU)) || math.IsInf(float64(s.CPU), 0) { // review M4 (plan 21): .nan < 0.1 is false
		return fmt.Errorf(`%s: "cpu" must be a number of cores like 0.5`, s.Name)
	}
	if s.CPU != 0 && s.CPU < 0.1 {
		return fmt.Errorf(`%s: "cpu" must be at least 0.1`, s.Name)
	}
	if s.Memory != 0 && s.Memory < 64e6 {
		return fmt.Errorf(`%s: "memory" must be at least 64MB`, s.Name)
	}
	if (s.Kind == KindMySQL || s.Kind == KindMariaDB) && s.Memory != 0 && s.Memory < 256e6 {
		return fmt.Errorf(`%s: %s needs a "memory" of at least 256MB; InnoDB alone takes 128MB`, s.Name, map[Kind]string{KindMySQL: "MySQL", KindMariaDB: "MariaDB"}[s.Kind])
	}
	if s.Kind == KindClickHouse && s.Memory != 0 && s.Memory < 1e9 {
		return fmt.Errorf(`%s: ClickHouse needs a "memory" of at least 1GB`, s.Name)
	}
	if s.Kind == KindMongoDB && s.Memory != 0 && s.Memory < 512e6 {
		return fmt.Errorf(`%s: MongoDB needs a "memory" of at least 512MB; its cache alone takes 256MB`, s.Name)
	}
	return nil
}
