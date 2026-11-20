package spec

import (
	"fmt"
	"github.com/behzodhalil/yawl/internal/cron"
	"net/netip"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Kind string

const (
	KindWeb        Kind = "web"
	KindWorker     Kind = "worker"
	KindPostgres   Kind = "postgres"
	KindCron       Kind = "cron"
	KindValkey     Kind = "valkey"
	KindMySQL      Kind = "mysql"
	KindMongoDB    Kind = "mongodb"
	KindMariaDB    Kind = "mariadb"
	KindClickHouse Kind = "clickhouse"
)

var databaseVersions = map[Kind][]int{KindPostgres: {17, 18, 16}, KindValkey: {8}, KindMySQL: {8}, KindMongoDB: {8}, KindMariaDB: {11}, KindClickHouse: {25}}

var alwaysBackedUp = map[Kind]string{KindPostgres: "Postgres", KindMySQL: "MySQL", KindMongoDB: "MongoDB", KindMariaDB: "MariaDB"}

func IsDatabase(k Kind) bool { _, ok := databaseVersions[k]; return ok }

func BackupChosen(k Kind) bool {
	_, always := alwaysBackedUp[k]
	return IsDatabase(k) && !always
}

func (s Service) PreviewOn() bool { return s.Preview == nil || *s.Preview }

func (s Service) BackupOn() bool {
	if _, ok := alwaysBackedUp[s.Kind]; ok {
		return true
	}
	return s.Backup != nil && *s.Backup
}

type Service struct {
	Name          string        `yaml:"name"`
	Kind          Kind          `yaml:"kind"`
	Repo          string        `yaml:"repo"`
	Image         string        `yaml:"image"` // in place of repo: a prebuilt image (design record 24)
	Branch        string        `yaml:"branch"`
	Port          int           `yaml:"port"`
	HealthPath    string        `yaml:"health_path"`
	HealthTimeout time.Duration `yaml:"health_timeout"`
	DrainTimeout  time.Duration `yaml:"drain_timeout"`
	AutoDeploy    bool          `yaml:"auto_deploy"` // deploy on a push to Branch; true unless written false
	Version       int           `yaml:"version"`     // postgres: 16, 17 or 18
	BackupAt      string        `yaml:"backup_at"`   // postgres: daily backup time, "HH:MM" UTC
	Backup        *bool         `yaml:"backup"`      // a database but Postgres: back up nightly (record 29, B4)
	Builder       string        `yaml:"builder"`     // an app with no Dockerfile: "railpack" (record 32)
	Root          string        `yaml:"root"`        // an app from a repo: the folder it builds from, like landing
	NoIndex       bool          `yaml:"noindex"`     // web: X-Robots-Tag noindex (record 38)
	Preview       *bool         `yaml:"preview"`     // false: pull requests' previews leave it out
	WWW           string        `yaml:"www"`         // web: "bare" or "www", the name the other answers 301 to (record 38)
	Watch         []string      `yaml:"watch"`       // an app from a repo: a push deploys it only when a changed file matches (record 40)
	PITR          bool          `yaml:"pitr"`        // postgres: 7 days to any second (record 31)
	Replicas      int           `yaml:"replicas"`    // web, worker: copies on the server, 1 to 10; 0 is one (record 43)
	Tags          []string      `yaml:"tags"`        // labels to filter and deploy by (record 46)
	Keep          *Keep         `yaml:"keep"`        // a database or volumes: how many backups (record 49); nil is 7 daily, 4 weekly

	Release        []string      `yaml:"release"`         // web, worker: argv run on the new image before start
	ReleaseTimeout time.Duration `yaml:"release_timeout"` // default 10m when Release is set
	Domains        []string      `yaml:"domains"`         // web only
	OutsidePort    int           `yaml:"outside_port"`    // postgres only; 0 = no outside access
	Allow          []string      `yaml:"allow"`           // postgres only: CIDRs allowed through OutsidePort

	// web and worker only (design record 19): named directories kept across
	// deploys, backed up nightly at BackupAt (default 03:30 UTC).
	Volumes []Volume `yaml:"volumes"`

	// cron only (design record 16, W7). An empty Timezone is the panel's own.
	Schedule string        `yaml:"schedule"`
	Timezone string        `yaml:"timezone"`
	Command  []string      `yaml:"command"` // also web and worker: replaces the image's CMD; empty runs the image's own
	Timeout  time.Duration `yaml:"timeout"` // default 1h

	// every kind (design record 23): 0 is no limit
	CPU    Cores `yaml:"cpu"`
	Memory Size  `yaml:"memory"`
}

type Volume struct {
	Name string `yaml:"name" json:"name"`
	Path string `yaml:"path" json:"path"`
}

func (s *Service) UnmarshalYAML(n *yaml.Node) error {
	type plain Service
	p := plain{AutoDeploy: true}
	if err := n.Decode(&p); err != nil {
		return err
	}
	*s = Service(p)
	return nil
}

var githubRe = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:)([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

func GitHubRepo(url string) (string, bool) {
	m := githubRe.FindStringSubmatch(url)
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1] + "/" + m[2]), true
}

var httpsRepoRe = regexp.MustCompile(`^https://([A-Za-z0-9.-]+\.[A-Za-z]{2,}(?::\d+)?)/((?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

func RepoKey(url string) (string, bool) {
	if name, ok := GitHubRepo(url); ok {
		return name, true
	}
	m := httpsRepoRe.FindStringSubmatch(url)
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1] + "/" + m[2]), true
}

type File struct {
	Project     string `yaml:"project"`
	Environment string `yaml:"environment"`
	Server      string `yaml:"server"`
	Preview     bool   `yaml:"preview"` // every pull request gets a copy of this environment (design record 21)
	// PreviewDomain gives each preview's web services <service>-pr-<n>.<this>,
	// under a wildcard DNS record the owner points at the server.
	PreviewDomain string    `yaml:"preview_domain"`
	Services      []Service `yaml:"services"`
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

func Parse(b []byte) (File, error) {
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("yawl.yaml: %w", err)
	}
	return f, Validate(&f)
}

func Validate(f *File) error {
	for i := range f.Services {
		f.Services[i].defaults()
	}
	return f.validate()
}

func ValidHost(h string) bool { return hostRe.MatchString(h) && !strings.HasSuffix(h, ".sslip.io") }

var hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func (s Service) validateOutside() error {
	if s.OutsidePort == 0 {
		if len(s.Allow) > 0 {
			return fmt.Errorf(`%s: "allow" needs "outside_port"`, s.Name)
		}
		return nil
	}
	if s.OutsidePort < 1024 || s.OutsidePort > 65535 {
		return fmt.Errorf(`%s: "outside_port" must be 1024 to 65535, got %d`, s.Name, s.OutsidePort)
	}
	if len(s.Allow) == 0 {
		return fmt.Errorf(`%s: "allow" must list who may connect when "outside_port" is set`, s.Name)
	}
	for _, a := range s.Allow {
		p, err := netip.ParsePrefix(a)
		if err != nil || p != p.Masked() {
			return fmt.Errorf(`%s: "allow" entry %q: allow entries are CIDRs like 10.0.0.1/32`, s.Name, a)
		}
		if p.Addr().Is6() { // plan 5 minor: Docker's proxy turns a v6 client into the bridge gateway, so HBA never matches it
			return fmt.Errorf(`%s: "allow" entry %q: allow entries are IPv4 CIDRs`, s.Name, a)
		}
	}
	return nil
}

var volNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

func (s Service) validateVolumes() error {
	if s.Kind != KindWeb && s.Kind != KindWorker {
		return fmt.Errorf(`%s: only a web service or a worker has "volumes"`, s.Name)
	}
	if len(s.Volumes) > 4 {
		return fmt.Errorf(`%s: at most 4 volumes`, s.Name)
	}
	seen, paths := map[string]bool{}, map[string]bool{}
	for _, v := range s.Volumes {
		p := path.Clean(v.Path) // plan 16 minor (M4): Docker refuses / and a path mounted twice, at every start
		switch {
		case !volNameRe.MatchString(v.Name):
			return fmt.Errorf(`%s: "volumes" name %q must be lowercase letters, digits and dashes`, s.Name, v.Name)
		case !strings.HasPrefix(v.Path, "/"):
			return fmt.Errorf(`%s: "volumes" path %q must be absolute`, s.Name, v.Path)
		case p == "/":
			return fmt.Errorf(`%s: "volumes" path cannot be /`, s.Name)
		case seen[v.Name]:
			return fmt.Errorf(`%s: "volumes" name %q appears twice`, s.Name, v.Name)
		case paths[p]:
			return fmt.Errorf(`%s: "volumes" path %q appears twice`, s.Name, p)
		}
		seen[v.Name], paths[p] = true, true
	}
	return nil
}

func (s Service) validateCron() error {
	if _, err := cron.Parse(s.Schedule); err != nil {
		return fmt.Errorf(`%s: "schedule" %v`, s.Name, err)
	}
	if s.Timezone != "" {
		if _, err := cron.Zone(s.Timezone); err != nil {
			return fmt.Errorf(`%s: "timezone" %q is not a time zone`, s.Name, s.Timezone)
		}
	}
	for _, f := range []struct {
		name string
		set  bool
	}{{"port", s.Port != 0}, {"health_path", s.HealthPath != ""}, {"release", len(s.Release) > 0}, {"version", s.Version != 0}, {"backup_at", s.BackupAt != ""}} {
		if f.set {
			return fmt.Errorf(`%s: a cron job has no "%s"`, s.Name, f.name)
		}
	}
	switch {
	case len(s.Command) == 0:
		return fmt.Errorf(`%s: a cron job needs a "command"`, s.Name)
	case s.Timeout < time.Minute || s.Timeout > 24*time.Hour:
		return fmt.Errorf(`%s: "timeout" must be between 1m and 24h`, s.Name)
	case s.Image == "" && !strings.HasPrefix(s.Repo, "https://") && !strings.HasPrefix(s.Repo, "git@") && !strings.HasPrefix(s.Repo, "file://"):
		return fmt.Errorf(`%s: "repo" must be an https:// or git@ URL`, s.Name)
	}
	return nil
}

var backupAtRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func (s *Service) defaults() {
	if s.Image != "" {
		s.AutoDeploy = false
	}
	if s.Kind == KindCron {
		if s.Branch == "" && s.Image == "" {
			s.Branch = "main"
		}
		if s.Timeout == 0 {
			s.Timeout = time.Hour
		}
		return
	}
	if _, always := alwaysBackedUp[s.Kind]; IsDatabase(s.Kind) && !always && s.Backup != nil && !*s.Backup {
		s.Backup = nil
	}
	if IsDatabase(s.Kind) {
		if s.Version == 0 {
			s.Version = databaseVersions[s.Kind][0]
		}
		if s.BackupAt == "" {
			s.BackupAt = "03:00"
		}
		return
	}
	if s.Branch == "" && s.Image == "" {
		s.Branch = "main"
	}
	if len(s.Volumes) > 0 && s.BackupAt == "" {
		s.BackupAt = "03:30"
	}
	if s.Kind == KindWeb && s.Port == 0 {
		s.Port = 10000
	}
	if s.HealthPath == "" {
		s.HealthPath = "/"
	}
	if s.HealthTimeout == 0 {
		s.HealthTimeout = 60 * time.Second
	}
	if s.DrainTimeout == 0 {
		s.DrainTimeout = 30 * time.Second
	}
	if len(s.Release) > 0 && s.ReleaseTimeout == 0 {
		s.ReleaseTimeout = 10 * time.Minute
	}
}

func (f File) validate() error {
	for _, fv := range [][2]string{{"project", f.Project}, {"environment", f.Environment}, {"server", f.Server}} {
		if !nameRe.MatchString(fv[1]) {
			return fmt.Errorf(`"%s" must be lowercase letters, digits and dashes, got %q`, fv[0], fv[1])
		}
	}
	if f.PreviewDomain != "" && !f.Preview {
		return fmt.Errorf(`"preview_domain" needs "preview: true"`)
	} else if f.PreviewDomain != "" && (!hostRe.MatchString(f.PreviewDomain) || strings.HasSuffix(f.PreviewDomain, ".sslip.io")) {
		return fmt.Errorf(`"preview_domain" %q must be a lowercase domain like preview.acme.uz`, f.PreviewDomain)
	}
	if IsPreviewName(f.Environment) {
		return fmt.Errorf("%s is the name of a preview environment; pick another name.", f.Environment)
	}
	seen, hosts, ports := map[string]bool{}, map[string]bool{}, map[int]bool{}
	for _, s := range f.Services {
		if !nameRe.MatchString(s.Name) {
			return fmt.Errorf(`service "name" must be lowercase letters, digits and dashes, got %q`, s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf(`service "name" %q appears twice`, s.Name)
		}
		if err := s.validateLimits(); err != nil {
			return err
		}
		if err := s.validateImage(); err != nil {
			return err
		}
		if !s.PreviewOn() && !f.Preview {
			return fmt.Errorf(`%s: "preview: false" leaves a service out of previews; this environment has no "preview: true"`, s.Name)
		}
		if (s.NoIndex || s.WWW != "") && s.Kind != KindWeb {
			return fmt.Errorf(`%s: "noindex" and "www" are a web service's`, s.Name)
		} else if s.WWW != "" && s.WWW != "bare" && s.WWW != "www" {
			return fmt.Errorf(`%s: "www" is bare or www`, s.Name)
		}
		if err := s.validateTags(); err != nil {
			return err
		}
		if err := s.validateKeep(); err != nil {
			return err
		}
		if s.Replicas > 1 && s.Kind != KindWeb && s.Kind != KindWorker {
			return fmt.Errorf(`%s: "replicas" is for a web service or a worker`, s.Name)
		} else if s.Replicas < 0 || s.Replicas > 10 {
			return fmt.Errorf(`%s: "replicas" is 1 to 10`, s.Name)
		} else if s.Replicas > 1 && len(s.Volumes) > 0 {
			return fmt.Errorf(`%s has a volume; replicas share nothing, so it can have only one copy`, s.Name)
		}
		if s.Builder != "" && (IsDatabase(s.Kind) || s.Image != "") {
			return fmt.Errorf(`%s: "builder" is for apps built from a repo`, s.Name)
		} else if s.Builder != "" && s.Builder != "railpack" {
			return fmt.Errorf(`%s: "builder" is railpack or left out`, s.Name)
		}
		if s.Root != "" && (IsDatabase(s.Kind) || s.Image != "") {
			return fmt.Errorf(`%s: "root" is for apps built from a repo`, s.Name)
		} else if s.Root != "" && !ValidRoot(s.Root) {
			return fmt.Errorf(`%s: "root" is a folder in the repo, like landing or apps/web`, s.Name)
		}
		seen[s.Name] = true
		if len(s.Domains) > 0 && s.Kind != KindWeb {
			return fmt.Errorf(`%s: only a web service has "domains"`, s.Name)
		}
		for _, h := range s.Domains {
			host, path := SplitDomain(h)
			switch {
			case !hostRe.MatchString(host):
				return fmt.Errorf(`%s: "domains" entry %q must be a lowercase host name like shop.acme.uz`, s.Name, h)
			case path != "" && !pathRe.MatchString(path):
				return fmt.Errorf(`%s: "domains" entry %q: a path starts with / and has letters, digits, -, _ and . between its slashes, like acme.uz/api`, s.Name, h)
			case strings.Contains(path+"/", "/./") || strings.Contains(path+"/", "/../"):
				return fmt.Errorf(`%s: "domains" entry %q: a part of a path cannot be . or ..`, s.Name, h)
			case strings.HasSuffix(host, ".sslip.io"):
				return fmt.Errorf(`%s: "domains" entry %q: sslip.io hosts are given by Yawl itself`, s.Name, h)
			case hosts[h]:
				return fmt.Errorf(`%s: "domains" entry %q appears twice`, s.Name, h)
			}
			hosts[h] = true
		}
		if len(s.Volumes) > 0 {
			if err := s.validateVolumes(); err != nil {
				return err
			}
		}
		if len(s.Command) > 0 && IsDatabase(s.Kind) {
			return fmt.Errorf(`%s: "command" is for a web service, a worker or a cron job`, s.Name)
		}
		if s.Kind != KindCron {
			for _, f := range []struct {
				name string
				set  bool
			}{{"schedule", s.Schedule != ""}, {"timezone", s.Timezone != ""}, {"timeout", s.Timeout != 0}} {
				if f.set {
					return fmt.Errorf(`%s: only a cron job has "%s"`, s.Name, f.name)
				}
			}
		}
		if IsDatabase(s.Kind) && s.Kind != KindPostgres && (s.OutsidePort != 0 || len(s.Allow) > 0) {
			return fmt.Errorf(`%s: "outside_port" and "allow" are for Postgres only`, s.Name)
		}
		if s.Kind != KindPostgres && (s.OutsidePort != 0 || len(s.Allow) > 0) {
			return fmt.Errorf(`%s: only a database has "outside_port" and "allow"`, s.Name)
		}
		if s.PITR && s.Kind != KindPostgres {
			return fmt.Errorf(`%s: "pitr" is for Postgres only`, s.Name)
		}
		if !IsDatabase(s.Kind) && s.Backup != nil {
			return fmt.Errorf(`%s: only a database has "backup"`, s.Name)
		}
		if s.Kind == KindCron {
			if err := s.validateCron(); err != nil {
				return err
			}
			continue
		}
		if IsDatabase(s.Kind) {
			if s.Kind == KindPostgres {
				if err := s.validateOutside(); err != nil {
					return err
				}
				if s.OutsidePort != 0 && ports[s.OutsidePort] {
					return fmt.Errorf(`%s: "outside_port" %d appears twice`, s.Name, s.OutsidePort)
				}
				ports[s.OutsidePort] = true
			}
			if name, always := alwaysBackedUp[s.Kind]; always && s.Backup != nil && !*s.Backup {
				return fmt.Errorf(`%s: "backup" is always on for %s`, s.Name, name)
			}
			if len(s.Release) > 0 {
				return fmt.Errorf(`%s: a database has no "release"`, s.Name)
			}
			switch {
			case !slices.Contains(databaseVersions[s.Kind], s.Version):
				return fmt.Errorf(`%s: "version" must be %s, got %d`, s.Name, versionWords(databaseVersions[s.Kind]), s.Version)
			case !backupAtRe.MatchString(s.BackupAt):
				return fmt.Errorf(`%s: "backup_at" must be HH:MM in UTC, got %q`, s.Name, s.BackupAt)
			case s.Repo != "" || s.Branch != "":
				return fmt.Errorf(`%s: a database has no "repo"`, s.Name)
			case s.Port != 0:
				return fmt.Errorf(`%s: a database has no "port"; apps reach it by reference`, s.Name)
			}
			continue
		}
		switch {
		case s.Kind != KindWeb && s.Kind != KindWorker:
			return fmt.Errorf(`%s: "kind" must be web, worker, cron, postgres, valkey, mysql, mariadb, mongodb or clickhouse, got %q`, s.Name, s.Kind)
		case s.Version != 0:
			return fmt.Errorf(`%s: only a database has a "version"`, s.Name)
		case s.BackupAt != "" && len(s.Volumes) == 0:
			return fmt.Errorf(`%s: only a database or a service with volumes has a "backup_at"`, s.Name)
		case s.BackupAt != "" && !backupAtRe.MatchString(s.BackupAt):
			return fmt.Errorf(`%s: "backup_at" must be HH:MM in UTC, got %q`, s.Name, s.BackupAt)
		case s.Kind == KindWorker && s.Port != 0:
			return fmt.Errorf(`%s: a worker has no "port"`, s.Name)
		case s.Port < 0 || s.Port > 65535:
			return fmt.Errorf(`%s: "port" out of range`, s.Name)
		case !strings.HasPrefix(s.HealthPath, "/"):
			return fmt.Errorf(`%s: "health_path" must start with /`, s.Name)
		case s.Image == "" && !strings.HasPrefix(s.Repo, "https://") && !strings.HasPrefix(s.Repo, "git@") && !strings.HasPrefix(s.Repo, "file://"):
			return fmt.Errorf(`%s: "repo" must be an https:// or git@ URL`, s.Name)
		}
	}
	return validatePaths(f.Services)
}

func IsPreviewName(env string) bool { return strings.HasPrefix(env, "pr-") }

func versionWords(vs []int) string {
	sorted := slices.Sorted(slices.Values(vs))
	words := make([]string, len(sorted))
	for i, v := range sorted {
		words[i] = strconv.Itoa(v)
	}
	if len(words) == 1 {
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,29}$`)

func (s Service) validateTags() error {
	if len(s.Tags) > 10 {
		return fmt.Errorf("%s: at most 10 tags", s.Name)
	}
	seen := map[string]bool{}
	for _, t := range s.Tags {
		if !tagRe.MatchString(t) {
			return fmt.Errorf(`%s: tag %q is lowercase letters, digits and dashes, up to 30`, s.Name, t)
		}
		if seen[t] {
			return fmt.Errorf(`%s: tag %q appears twice`, s.Name, t)
		}
		seen[t] = true
	}
	return nil
}

type Keep struct {
	Daily   int `yaml:"daily" json:"daily"`
	Weekly  int `yaml:"weekly" json:"weekly"`
	Monthly int `yaml:"monthly" json:"monthly"`
}

func (k *Keep) UnmarshalYAML(n *yaml.Node) error {
	type plain Keep
	p := plain{Daily: 7, Weekly: 4}
	if err := n.Decode(&p); err != nil {
		return err
	}
	*k = Keep(p)
	return nil
}

func (k *Keep) String() string {
	if k == nil {
		return "7 daily, 4 weekly"
	}
	parts := []string{fmt.Sprintf("%d daily", k.Daily)}
	if k.Weekly > 0 {
		parts = append(parts, fmt.Sprintf("%d weekly", k.Weekly))
	}
	if k.Monthly > 0 {
		parts = append(parts, fmt.Sprintf("%d monthly", k.Monthly))
	}
	return strings.Join(parts, ", ")
}

func (s Service) validateKeep() error {
	if s.Keep == nil {
		return nil
	}
	if !IsDatabase(s.Kind) && len(s.Volumes) == 0 {
		return fmt.Errorf(`%s: "keep" is for a database or a service with volumes`, s.Name)
	}
	for _, c := range []struct {
		name      string
		v, lo, hi int
	}{{"daily", s.Keep.Daily, 1, 90}, {"weekly", s.Keep.Weekly, 0, 52}, {"monthly", s.Keep.Monthly, 0, 24}} {
		if c.v < c.lo || c.v > c.hi {
			return fmt.Errorf("%s: keep %s is %d to %d", s.Name, c.name, c.lo, c.hi)
		}
	}
	return nil
}
