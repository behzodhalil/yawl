package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Version is bumped whenever a message changes shape. Both sides refuse a mismatch.
const Version = 1

var ErrVersion = errors.New("protocol version mismatch")

const (
	TypeChallenge = "challenge"
	TypeHello     = "hello"
	TypeCommand   = "command"
	TypeResult    = "result"
	TypeHeartbeat = "heartbeat"
	TypeRenew     = "renew"   // agent -> server: a CSR for a fresh certificate
	TypeRenewed   = "renewed" // server -> agent: the certificate
)

type Envelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Version int             `json:"version"`
	Body    json.RawMessage `json:"body"`
}

// Challenge is the server's first message. The agent proves who it is by
// signing AuthMessage(Nonce) with the key its certificate was issued for.
// Challenge's shape is frozen: an agent of any version must be able to read
// ServerVersion from it, to update itself to the panel's version.
type Challenge struct {
	Nonce         []byte `json:"nonce"`
	ServerVersion string `json:"server_version,omitempty"`
}

type Hello struct {
	AgentVersion string `json:"agent_version"`
	Protocol     int    `json:"protocol"`
	ServerID     string `json:"server_id"`
	CertPEM      string `json:"cert_pem"`
	Sig          []byte `json:"sig"` // ECDSA ASN.1 over sha256(AuthMessage(nonce))
}

// AuthMessage is what an agent signs; the prefix keeps the signature from
// being valid for anything else the key might sign.
func AuthMessage(nonce []byte) []byte { return append([]byte("yawl-agent-auth\x00"), nonce...) }

type Renew struct {
	CSRPEM string `json:"csr_pem"`
}

type Renewed struct {
	CertPEM string `json:"cert_pem"`
}

type Heartbeat struct {
	Load1       float64 `json:"load1"`
	DiskUsedPct int     `json:"disk_used_pct"`
	Containers  int     `json:"containers"`
	// The machine's facts for its Overview; an older agent sends none.
	OSName  string `json:"os_name,omitempty"` // /etc/os-release PRETTY_NAME
	OSID    string `json:"os_id,omitempty"`   // and ID: ubuntu, debian
	Kernel  string `json:"kernel,omitempty"`
	Cores   int    `json:"cores,omitempty"`
	UptimeS int64  `json:"uptime_s,omitempty"`
	Reboot  bool   `json:"reboot,omitempty"`
}

const (
	CmdBuild           = "Build"
	CmdPull            = "Pull"
	CmdStart           = "Start"
	CmdHealthCheck     = "HealthCheck"
	CmdSwitchRoute     = "SwitchRoute"
	CmdDrain           = "Drain"
	CmdStop            = "Stop"
	CmdPrune           = "Prune"
	CmdReveal          = "Reveal"
	CmdEnsureDatabase  = "EnsureDatabase"
	CmdBackup          = "Backup"
	CmdRestore         = "Restore"
	CmdDeleteBackups   = "DeleteBackups"
	CmdCopyBackup      = "CopyBackup" // one backup object to a copy destination
	CmdCleanup         = "Cleanup"    // stopped containers, untagged images, build cache, unused networks
	CmdUnusedVolumes   = "UnusedVolumes"
	CmdTailscaleUp     = "TailscaleUp"
	CmdTailscaleDown   = "TailscaleDown"
	CmdTailscaleStatus = "TailscaleStatus"
	CmdOSStatus        = "OSStatus"
	CmdOSUpgrade       = "OSUpgrade"
	CmdLogDrain        = "LogDrain"
	CmdSnapshot        = "Snapshot"
	CmdImport          = "Import" // an outside dump into a database
	CmdRelease         = "Release"
	CmdEnsureDomain    = "EnsureDomain"
	CmdRemoveRoute     = "RemoveRoute"
	CmdStatusPage      = "StatusPage"
	CmdLogs            = "Logs"
	CmdProbe           = "Probe"
	CmdInspect         = "Inspect"
	CmdReport          = "Report"
	CmdHash            = "Hash"
	CmdWatch           = "Watch"
	CmdCronSchedule    = "CronSchedule"
	CmdCronRemove      = "CronRemove"
	CmdCronRuns        = "CronRuns"
	CmdCronRunNow      = "CronRunNow"
	CmdMetrics         = "Metrics"
	CmdRemoveVolume    = "RemoveVolume"
	CmdBackupVolume    = "BackupVolume"
	CmdRestoreVolume   = "RestoreVolume"
	CmdQuery           = "Query" // SQL from the panel's Data tab
)

// Unrecorded commands are answered fresh every time and never stored by the
// agent: they are reads or are repeated on a timer, so a stored answer would
// be stale and the store would grow without end.
func Unrecorded(name string) bool {
	switch name {
	case CmdTailscaleStatus, CmdOSStatus, CmdOSUpgrade, CmdCleanup, CmdUnusedVolumes, CmdLogs, CmdProbe, CmdReveal, CmdEnsureDomain, CmdRemoveRoute, CmdStatusPage, CmdInspect, CmdReport, CmdHash, CmdWatch, CmdCronRuns, CmdCronRunNow, CmdMetrics, CmdNetwork,
		CmdFirewall, CmdFirewallKeep, CmdFirewallStatus, CmdFirewallUnban, CmdShipWAL,
		CmdQuery:
		return true
	}
	return false
}

// Command is idempotent by Key: an agent that has finished a key replies with
// the stored result instead of acting again. Key = deployID + ":" + step.
type Command struct {
	Key  string          `json:"key"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type Result struct {
	Key    string          `json:"key"`
	OK     bool            `json:"ok"`
	Cause  string          `json:"cause,omitempty"`
	Output string          `json:"output,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

type BuildArgs struct {
	Repo, Branch, Commit, Image string
	Token                       string      `json:",omitempty"` // GitHub installation token, this command only
	Vars                        []SealedVar `json:",omitempty"` // the build-flagged ones
	Keys                        []string    `json:",omitempty"` // every var name the service has, for missing-key warnings
	Env                         string      `json:",omitempty"` // the environment's name, for those warnings
	Builder                     string      `json:",omitempty"` // "railpack" asks for Railpack
	Root                        string      `json:",omitempty"` // the repo folder to build, "" its root
	NoCache                     bool        `json:",omitempty"` // every step built again
	Push                        *PushTo     `json:",omitempty"` // a build server sends the image on
}

// PushTo is where a build server pushes what it built, for the target to pull.
type PushTo struct {
	Ref  string
	Auth *RegistryAuth `json:",omitempty"`
}
type StartArgs struct {
	Service, Env, Project, Deploy, Image, Kind string
	Port                                       int
	Vars                                       []SealedVar // opened by the agent at start
	Volumes                                    []Volume    // named volumes, mounted
	Networks                                   []string    `json:",omitempty"` // other environments' networks, for a database there on this server
	CPU                                        float64     `json:",omitempty"` // cores; 0 is no limit
	Memory                                     int64       `json:",omitempty"` // bytes; 0 is no limit
	Replicas                                   int         `json:",omitempty"` // copies; 0 and 1 are one
	Cmd                                        []string    `json:",omitempty"` // replaces the image's CMD; empty runs the image's own
}

// Volume is a service's named directory: Docker volume VolumeName(…) at Path.
type Volume struct{ Name, Path string }

var volPartRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// VolumeName is the Docker volume of a service's volume: yawl_<p>_<e>_<s>_<n>.
// A part never holds "_", so two services never share one, and none is ever a
// database's yawl-…-data. The agent checks every part it is sent.
func VolumeName(project, env, service, name string) (string, error) {
	for _, p := range []string{project, env, service, name} {
		if !volPartRe.MatchString(p) {
			return "", fmt.Errorf("not a volume name part: %q", p)
		}
	}
	return "yawl_" + project + "_" + env + "_" + service + "_" + name, nil
}

// SealedVar is an environment variable whose value only the agent can open.
type SealedVar struct {
	Key, Box string
	Build    bool `json:",omitempty"` // also passed to the build as a BuildKit secret
	// Source: Box is a secret manager's token, and the agent fetches its
	// variables; Key then names the source.
	Source *SecretSource `json:",omitempty"`
}

// SecretSource is a secret manager the agent reads variables from: doppler
// (the token is a service token for one config), infisical (URL, Workspace,
// Env, Path; the token is "clientId:clientSecret"), vault (URL, Path of a KV
// v2 secret like secret/data/shop).
type SecretSource struct {
	Provider       string
	URL, Path      string `json:",omitempty"`
	Workspace, Env string `json:",omitempty"`
}
type HealthArgs struct {
	Container, Kind, Path string
	Port                  int
	Timeout               time.Duration
}

// SwitchArgs points every host of a service (its free sslip.io host and each
// domain whose DNS is right) at Container.
type SwitchArgs struct {
	Hosts     []string
	Container string
	Port      int
	Options   RouteOptions `json:",omitempty"`
	WaitCert  bool         `json:",omitempty"`
}

// RouteOptions shape what a web service's hosts answer.
type RouteOptions struct {
	NoIndex     bool              `json:",omitempty"` // X-Robots-Tag: noindex on every answer
	Maintenance string            `json:",omitempty"` // the service's name: a 503 page instead of the app
	BasicUser   string            `json:",omitempty"` // with BasicHash: HTTP basic auth
	BasicHash   string            `json:",omitempty"` // a bcrypt hash, base64-encoded as Caddy reads it
	Redirect    map[string]string `json:",omitempty"` // host -> the host it answers 301 to (www)
	Status      string            `json:",omitempty"` // a status page's id: its file is served, nothing proxied
}
type DrainArgs struct {
	Container string
	Timeout   time.Duration
}
type StopArgs struct{ Container string }

// PruneArgs removes the repository's image tags that are not in Keep: the
// images of the service's newest live deploys, which a rollback may need.
type PruneArgs struct {
	Repo string
	Keep []string
}

// RevealArgs asks the agent to open Box and seal the value to To, a key the
// requester made for this one request. The server only relays both boxes.
type RevealArgs struct{ Box, To string }
type RevealData struct{ Box string }

// WatchArgs hands an agent the watcher's address and its own signing key: hex HMAC-SHA256(panel key, "agent:"+server id).
type WatchArgs struct {
	URL string
	Key string
}

// CronJob is a cron job as the agent keeps and runs it: CronSchedule adds or replaces it by ID, the service's id.
type CronJob struct {
	CPU                              float64 `json:",omitempty"` // cores; 0 is no limit
	Memory                           int64   `json:",omitempty"` // bytes; 0 is no limit
	ID, Project, Env, Service, Image string
	Schedule, Timezone               string // five fields, on Timezone's wall clock
	Cmd                              []string
	Vars                             []SealedVar
	Timeout                          time.Duration
	Label                            string   // project/env/service, for the watcher's texts
	Networks                         []string `json:",omitempty"` // as StartArgs.Networks
}

// CronRemoveArgs names a job by ID; CronRuns and CronRunNow take it too.
type CronRemoveArgs struct{ ID string }

// CronRun is one run of a job as the agent keeps it: times in unix ms, the
// last 50 lines of output. A skipped run never started a container.
type CronRun struct {
	ID                                 string
	ScheduledAt, StartedAt, FinishedAt int64
	Exit                               int
	Cause, Output                      string
	Skipped, TimedOut                  bool
}

// CronRunsData is CronRuns' answer: the newest 20 runs first.
type CronRunsData struct {
	Running bool
	Runs    []CronRun
}

// BackupVolumeArgs backs a service's volume up as BackupArgs does a database; RestoreVolumeArgs fills it from a backup.
type BackupVolumeArgs struct {
	Project, Env, Service, Name string
	Storage                     Storage
	Key                         string
	RecoveryPubkey              string
}

type RestoreVolumeArgs struct {
	Project, Env, Service, Name string
	Storage                     Storage
	Key                         string
	SHA256                      string // of the object as backed up: anything else is refused before it decrypts
	RecoveryKey                 string // sealed to this agent, for this command only
}

// RemoveVolumeArgs names a Docker volume to remove: a purged database's.
type RemoveVolumeArgs struct{ Name string }

// MetricsArgs asks for the samples after Since (unix ms); the agent keeps
// the last 60 minutes.
type MetricsArgs struct {
	Since    int64
	LogWatch []LogWatch `json:",omitempty"` // the log alerts to count lines for
}

// LogWatch is one log alert's rule as the agent counts it: lines of the
// service containing Text, ignoring case, or its stderr lines when Text is "".
type LogWatch struct {
	Rule, Project, Env, Service, Text string
}

// LogHit is a minute's count for one rule, and the first line it counted (masked).
type LogHit struct {
	Rule  string
	Lines int
	First string
}

type MetricsData struct{ Samples []Sample }

// Sample is one minute of a machine: its own numbers, each app container's,
// and each web host's requests from Caddy.
type Sample struct {
	// HostsOK: Caddy's counters were read twice in a row, so Hosts are the
	// minute's numbers; false is a gap in the request graphs, never a zero.
	HostsOK    bool
	At         int64
	Machine    MachineSample
	Containers []ContainerSample
	Visits     []VisitSample `json:",omitempty"` // web hosts from Caddy's access log
	Hosts      []HostSample
	OOMs       []OOMSample `json:",omitempty"` // memory-limit kills since the last sample
	LogHits    []LogHit    `json:",omitempty"` // lines counted for log alerts since the last sample
}

// OOMSample is how many times Docker killed a service's containers for
// running out of their memory limit since the last sample.
type OOMSample struct {
	Project, Env, Service string
	Kills                 int
}

type MachineSample struct {
	CPU                            float64 // % of all cores
	Mem, MemTotal, Disk, DiskTotal uint64
	Load                           float64
	// NetRx, NetTx are the minute's bytes/s over the physical interfaces; nil: no number this minute (the first read, a counter reset, an older agent).
	NetRx, NetTx *int64 `json:",omitempty"`
}

type ContainerSample struct {
	Project, Env, Service string
	CPU                   float64 // % of one core
	Mem                   uint64
}

// VisitSample is one minute of a web host from Caddy's access log. Visitors is the day's distinct count so far, never who.
type VisitSample struct {
	Host           string
	S2, S3, S4, S5 int64            // requests by status class
	Bytes          int64            // response bytes
	Views          int64            // page views: GET, 2xx, HTML, not a bot
	Visitors       int64            // distinct visitors so far on Day (UTC)
	Day            string           // "2006-01-02"
	Paths, Refs    map[string]int64 `json:",omitempty"` // this minute's top 10 page views by path, by referring host
}

type HostSample struct {
	Host      string
	Req, Err5 int64
	P95ms     int64
	Gap       bool // its counters went back (Caddy restarted or reloaded): no number this minute
}

type HashArgs struct {
	Key  string
	Vars []SealedVar
}
type HashData struct{ Hashes []string }

// InspectArgs asks an agent to read a repository before its first deploy.
type InspectArgs struct {
	Repo, Branch string
	Token        string   `json:",omitempty"` // GitHub installation token, this command only
	Keys         []string `json:",omitempty"` // keys the environment already has
	Env          string   `json:",omitempty"`
}

type InspectData struct {
	Commit     string   `json:"commit"`
	Dockerfile bool     `json:"dockerfile"`
	Language   string   `json:"language"`
	ReadsPort  bool     `json:"reads_port"`
	Port       int      `json:"port"`
	HealthPath string   `json:"health_path"`
	Missing    []string `json:"missing"`
	Warnings   []string `json:"warnings"`
	Builder    string   `json:"builder"`
	Static     bool     `json:"static"`
	Notes      []string `json:"notes"`
}

// ReportData is what an agent runs, for a panel restored from a backup.
type ReportData struct{ Running []Running }

type Running struct{ Project, Env, Service, Deploy, Image, Container string }

type BuildData struct {
	Image, Commit string
	Subject       string   `json:",omitempty"` // the commit message's first line; agents before it send none
	Warnings      []string `json:",omitempty"` // missing keys, language versions: never a block
}
type StartData struct{ Container string }

func NewCommand(key, name string, args any) (Command, error) {
	b, err := json.Marshal(args)
	return Command{Key: key, Name: name, Args: b}, err
}

func Encode(typ, id string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Type: typ, ID: id, Version: Version, Body: b})
}

func Decode(b []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return e, err
	}
	if e.Version != Version {
		return e, fmt.Errorf("%w: got %d, want %d", ErrVersion, e.Version, Version)
	}
	return e, nil
}

// EnsureDatabaseArgs creates or starts a Postgres container. Password is the
// agent box of its PASSWORD, empty on first create; the agent then makes one.
type EnsureDatabaseArgs struct {
	Kind               string  `json:",omitempty"` // "" and "postgres" are Postgres
	PITR               bool    `json:",omitempty"` // archive WAL for point in time recovery
	CPU                float64 `json:",omitempty"` // cores; 0 is no limit
	Memory             int64   `json:",omitempty"` // bytes; 0 is no limit
	Project, Env, Name string
	Version            int
	Password           string
	RecoveryPubkey     string // to seal what the agent generates
	// OutsidePort publishes the database on the server's PublicIP, over TLS,
	// for the Allow CIDRs only; 0 keeps it inside the environment's network.
	OutsidePort int      `json:",omitempty"`
	Allow       []string `json:",omitempty"`
	PublicIP    string   `json:",omitempty"`
	// PrivatePort publishes the database on PrivateIP (the server's mesh
	// address) for other environments, over TLS, and
	// ShareWith seals that URL to each referencing server's agent.
	PrivateIP   string            `json:",omitempty"`
	PrivatePort int               `json:",omitempty"`
	ShareWith   map[string]string `json:",omitempty"` // server id -> agent public key
	// ShareHere seals, for apps of other environments on this server, a URL
	// naming the database's container, unique where its alias is not.
	ShareHere bool `json:",omitempty"`
}

// EnsureDatabaseData carries the boxes of a generated password, else nothing;
// and, with outside access on, the boxes of the outside URL.
type EnsureDatabaseData struct {
	PasswordBox, PasswordRecoveryBox, URLBox, URLRecoveryBox string            `json:",omitempty"`
	OutsideURLBox, OutsideURLRecoveryBox                     string            `json:",omitempty"`
	Shares                                                   map[string]string `json:",omitempty"` // server id -> private URL sealed to it
	Here                                                     string            `json:",omitempty"` // ShareHere's URL, sealed to this agent
}

// Storage is where backups go. The server holds the keys (sealed with its
// own key) and hands them over for one command; the agent keeps nothing.
type Storage struct {
	Endpoint, Bucket, Region, AccessKey, SecretKey string
	Insecure                                       bool `json:",omitempty"` // plain http: an endpoint written http://, on a private network
}

type BackupArgs struct {
	Kind               string `json:",omitempty"` // "" is Postgres
	Project, Env, Name string
	Version            int
	Password           string // agent box
	Storage            Storage
	Key                string // object key
	RecoveryPubkey     string // the dump is encrypted to it
	Base               bool   `json:",omitempty"` // a pg_basebackup tar for PITR, not a dump
}

type BackupData struct {
	Size   int64
	SHA256 string
}

// RestoreArgs loads a backup into a new database that EnsureDatabase made.
type RestoreArgs struct {
	Kind               string              `json:",omitempty"` // "" is Postgres
	Ensure             *EnsureDatabaseArgs `json:",omitempty"` // not Postgres: start it again after its volume is loaded
	Project, Env, Name string              // the new database
	Version            int
	Password           string // agent box of its PASSWORD
	Storage            Storage
	Key                string       // object key of the backup
	SHA256             string       // of the object as backed up: anything else is refused before it decrypts
	RecoveryKey        string       // "yawl-recovery-…" sealed to this agent by the owner's CLI, for this command only
	PITR               *PITRRestore `json:",omitempty"` // Key is a base backup: replay WAL to a time
}

// PITRRestore is what a restore to a time adds: the WAL batches to replay,
// the time ("2006-01-02 15:04:05+00"), and the source's PASSWORD box, which
// the base's roles still carry until the new password is set.
type PITRRestore struct {
	WAL            []string
	WALSHA256      []string // of each batch in WAL, in order
	At             string
	SourcePassword string
}

// TailscaleUpArgs joins the tailnet: AuthKey is the agent's box of the key.
type TailscaleUpArgs struct{ AuthKey, Hostname string }

// OSStatus is what the server's own files say of its updates.
type OSStatus struct {
	Updates    int      // waiting to be installed; -1 when the server does not say
	Security   int      // of them security updates; -1 when unknown
	Reboot     bool     // an installed update waits for a restart
	RebootPkgs []string `json:",omitempty"`
	Auto       bool     // unattended-upgrades installs security updates each day
	Installer  bool     // unattended-upgrades is installed
	Running    bool     // an install is running now
	Log        string   `json:",omitempty"` // the end of unattended-upgrades' log
}

type TailscaleStatus struct {
	Running bool
	Outside bool   `json:",omitempty"` // a Tailscale installed outside Yawl runs here
	Address string `json:",omitempty"` // the server's tailnet address
}

// CleanupArgs: AllCache removes all build cache, for a nearly full disk.
type CleanupArgs struct {
	AllCache bool `json:",omitempty"`
}

type CleanupData struct {
	Containers, Images int
	Freed              int64
}

type UnusedVolumesData struct{ Names []string }

// CopyBackupArgs copies the object Key from the main storage to a copy.
type CopyBackupArgs struct {
	From, To Storage
	Key      string
}

type DeleteBackupsArgs struct {
	Storage Storage
	Keys    []string
}

// DeleteBackupsData lists the keys that are gone; a partial failure is in Output.
type DeleteBackupsData struct{ Deleted []string }

// ImportArgs loads an outside dump into a running database: from URL, or from Path, a file name in the agent's imports folder.
type ImportArgs struct {
	Kind, Project, Env, Name string // Kind "" is Postgres
	Version                  int
	Password                 string // agent box of the database's PASSWORD
	URL                      string `json:",omitempty"`
	Path                     string `json:",omitempty"`
}

// QueryArgs runs SQL in a one-off client of a database.
// Write off runs as yawl_read, in the database's own read-only transaction.
type QueryArgs struct {
	Kind, Project, Env, Name string // Kind "" is Postgres
	Version                  int
	Password                 string // agent box of the database's PASSWORD
	SQL                      string
	Write                    bool `json:",omitempty"`
}

// QueryData is the last statement's result: its rows (nil is NULL), or what
// it changed when it returns none. More: a limit cut the rows.
type QueryData struct {
	Columns []string    `json:"columns"`
	Rows    [][]*string `json:"rows"`
	More    bool        `json:"more,omitempty"`
	Changed *int64      `json:"changed,omitempty"`
}

type SnapshotArgs struct {
	Kind               string `json:",omitempty"` // "" is Postgres; MySQL and MongoDB too
	Project, Env, Name string
	Version            int
	Password           string
}

type SnapshotData struct {
	Path string
	Size int64
}

// ReleaseArgs runs a service's release command once, in its new image.
type ReleaseArgs struct {
	CPU                                float64 `json:",omitempty"` // cores; 0 is no limit
	Memory                             int64   `json:",omitempty"` // bytes; 0 is no limit
	Project, Env, Service, Image, Kind string
	Deploy                             string // names the one-off container, so a resent command finds it
	Port                               int
	Cmd                                []string
	Vars                               []SealedVar
	Timeout                            time.Duration
	Volumes                            []Volume `json:",omitempty"` // mounted: the old version stopped before it
	Networks                           []string `json:",omitempty"` // as StartArgs.Networks
}

// EnsureDomainArgs checks that Host resolves to ExpectIP (the server's public
// IP) and only then routes it to Container, so Caddy asks for a certificate
// only when Let's Encrypt can succeed.
type EnsureDomainArgs struct {
	Host, ExpectIP, Container string
	Port                      int
	Upstream                  string       `json:",omitempty"` // a fixed address instead of a container: the panel itself
	Options                   RouteOptions `json:",omitempty"`
}

// EnsureDomainData: DNS is "ok" or "wrong" (with what the record should be);
// Cert is "issued" or "pending". Wrong DNS is an answer, not a failure.
type EnsureDomainData struct{ DNS, DNSDetail, Cert string }

type RemoveRouteArgs struct{ Host string }

// StatusPageArgs writes a status page's HTML where Caddy serves it from;
// empty HTML removes the file.
type StatusPageArgs struct{ ID, HTML string }

// LogsArgs reads a service's kept logs, across all its containers (stopped
// ones too), lines after Since only, newest Limit of them. Vars are the
// service's own: the agent opens them to mask their values in what it returns.
type LogsArgs struct {
	Project, Env, Service, Query string
	Since                        time.Time
	Limit                        int
	Vars                         []SealedVar
	Drain                        bool   `json:",omitempty"` // read the server's log drain, not Docker
	Stream                       string `json:",omitempty"` // "stderr": only those lines
}

type LogsData struct {
	Lines []LogsLine
	Drain bool `json:",omitempty"` // this server sends its logs to a drain, which can be read
}

// LogDrainArgs sends every app's logs on this server to Loki;
// Off stops it. The token is sealed to the agent.
type LogDrainArgs struct {
	URL, User, TokenBox, Server string
	Off                         bool `json:",omitempty"`
}

type LogsLine struct {
	Time         time.Time
	Deploy, Text string
	Stream       string `json:",omitempty"` // "stderr"; empty is stdout, as every line of an older agent reads (ROADMAP 35)
}

// ProbeArgs is one health probe of a live service: web is a GET of Path
// below 500; worker and postgres are a running, not restarting, container.
type ProbeArgs struct {
	Container, Kind, Path string
	Port                  int
}

const FirewallTrial = 2 * time.Minute

// CmdNetwork describes the whole private network to one agent: its address and every peer. Idempotent; sent every minute. The answer
// is each peer's last handshake, so it is not recorded by key.
const CmdNetwork = "Network"

type NetworkArgs struct {
	Address    string // this server's, in 10.73.0.0/16
	ListenPort int
	Peers      []Peer
}

type Peer struct{ PublicKey, Endpoint, AllowedIP string }

type NetworkData struct {
	Handshakes map[string]int64 `json:"handshakes"` // peer public key -> unix ms, 0 = never
}

// The firewall. CmdFirewall sends the whole wanted state,
// as CmdNetwork does, so it is idempotent and resent every minute; a Trial
// goes back by itself after 2 minutes unless CmdFirewallKeep names its Version.
const (
	CmdFirewall       = "Firewall"
	CmdFirewallKeep   = "FirewallKeep"
	CmdFirewallStatus = "FirewallStatus"
	CmdFirewallUnban  = "FirewallUnban"
)

// FwRule lets Proto ("tcp", "udp" or "both") to Port in, from From (IPs or
// CIDRs; empty is everyone).
type FwRule struct {
	Proto string   `json:"proto"`
	Port  int      `json:"port"`
	From  []string `json:"from,omitempty"`
}

type FirewallArgs struct {
	On      bool
	Allow   []FwRule // the owner's
	Block   []string // IPs and CIDRs dropped before any allow
	SSHFrom []string // empty is everyone
	Peers   []string // the other servers' public IPs, for UDP 51820
	Kept    []FwRule // what Yawl keeps open besides the base: Postgres outside ports
	Trial   bool
	Version string
}

type FirewallKeepArgs struct{ Version string }

type FirewallUnbanArgs struct{ IP string }

type FwPort struct {
	Proto    string `json:"proto"`
	Port     int    `json:"port"`
	Address  string `json:"address"`
	Process  string `json:"process,omitempty"`
	Exposure string `json:"exposure"` // "everyone", "some" or "closed"
}

type FwBan struct {
	IP    string `json:"ip"`
	Until int64  `json:"until"` // unix ms
}

type FirewallStatus struct {
	On         bool     `json:"on"`
	Available  bool     `json:"available"`
	UFW        bool     `json:"ufw"`
	Version    string   `json:"version"`
	TrialUntil int64    `json:"trial_until,omitempty"` // unix ms; 0 without a trial
	Bans       []FwBan  `json:"bans"`
	Ports      []FwPort `json:"ports"`
}

// CmdShipWAL uploads the WAL files a PITR Postgres finished: one encrypted batch, then those files are removed. Sent every minute.
const CmdShipWAL = "ShipWAL"

type ShipWALArgs struct {
	Project, Env, Name string
	Storage            Storage
	KeyPrefix          string // the batch's key is KeyPrefix + "<first>.tar.gz.age"
	RecoveryPubkey     string
	Remove             []string `json:",omitempty"` // the files of the batch the server last recorded: now they may go
}

type WALBatch struct {
	Key, First, Last string
	Size             int64
	SHA256           string
	Files            []string // in the batch, still in the volume until the server has recorded it
}

const WALBatchMax = 64

type ShipWALData struct {
	Batch   *WALBatch `json:",omitempty"` // nil: nothing finished to ship
	Pending int64     // bytes waiting before this batch
}

// RegistryAuth is a registry login, sent only inside a pull.
type RegistryAuth struct{ Username, Token string }

// PullArgs pull Ref (with Auth when its registry has a login) and tag the
// image as Tag, Yawl's own name for the deploy.
type PullArgs struct {
	Ref, Tag string
	Auth     *RegistryAuth `json:",omitempty"`
}

type PullData struct{ Digest string }

// A terminal in a container rides the agent's connection:
// the server sends TypeTermOpen, TypeTermIn, TypeTermResize, TypeTermClose;
// the agent answers TypeTermOut and, once, TypeTermEnd.
const (
	TypeTermOpen   = "term.open"
	TypeTermIn     = "term.in"
	TypeTermResize = "term.resize"
	TypeTermClose  = "term.close"
	TypeTermOut    = "term.out"
	TypeTermEnd    = "term.end"
)

type TermOpen struct {
	ID, Container string
	Cols, Rows    int
}

type TermData struct {
	ID   string
	Data []byte
}

type TermResize struct {
	ID         string
	Cols, Rows int
}

type TermEnd struct{ ID, Cause string }
