package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	goruntime "runtime"
	"slices"

	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/moby/client"
	"github.com/opencontainers/go-digest"
	"github.com/tonistiigi/fsutil"
)

// Railpack builds what Yawl's generator does not know.
// Its plan step downloads and runs mise, which an agent may not: it runs as a BuildKit step, on a release BuildKit fetches and checks.
// Its images are named by digest as well as tag: a tag moved on the registry
// builds nothing new.
const (
	railpackVersion  = "v0.43.0"
	railpackBuilder  = "ghcr.io/railwayapp/railpack-builder:mise-2026.10.14@sha256:03df9e9aa1e786c2e0dd12c5409acad3bbd5a0fa8a75c1117c7ddab5c0c3fde4"
	railpackFrontend = "ghcr.io/railwayapp/railpack-frontend:" + railpackVersion + "@sha256:355ef61fae6f2551103cd8b10c710cb07167dd353d561f15c69b8d7ac1c676cc"
	railpackPlan     = "railpack-plan.json"
)

var railpackSHA256 = map[string]string{
	"amd64": "248a8f60f36854aabed6124113631078a802de3461660855fc4de9c26065c1fb",
	"arm64": "732ece14eb4f4676a43ebba19431f954edf7b130acb76159ef0f2ff86ac42ac9",
}

func prepareArch(arch string) (url string, sum digest.Digest, err error) {
	sha, ok := railpackSHA256[arch]
	if !ok {
		return "", "", fmt.Errorf("Railpack has no build for %s", arch)
	}
	name := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[arch]
	return fmt.Sprintf("https://github.com/railwayapp/railpack/releases/download/%s/railpack-%s-%s-unknown-linux-musl.tar.gz", railpackVersion, railpackVersion, name),
		digest.Digest("sha256:" + sha), nil
}

// prepareState runs railpack prepare on the repository (the local "context")
// and leaves only the plan in /out. Each build variable reaches it as a
// BuildKit secret in its environment: its value is read by the plan (PHP
// picks its drivers from DB_CONNECTION) and is never in the
// definition, whose hash of the values plans again when one changes.
func prepareState(env map[string][]byte, arch string) llb.State {
	tgz := releaseState(arch)
	// not /tmp/railpack: Railpack makes its own cache directory at that path
	script := "mkdir -p /opt/yawl-railpack && tar -xzf /dl/railpack.tar.gz -C /opt/yawl-railpack railpack && a=() && for k in \"$@\"; do a+=(--env \"$k=${!k}\"); done && " +
		"exec /opt/yawl-railpack/railpack prepare /app --plan-out /out/" + railpackPlan + ` "${a[@]}"`
	keys := slices.Sorted(maps.Keys(env))
	opts := []llb.RunOption{llb.Args(append([]string{"bash", "-c", script, "railpack"}, keys...)),
		llb.AddMount("/dl", tgz, llb.Readonly), llb.AddMount("/app", llb.Local("context"), llb.Readonly),
		llb.AddEnv("YAWL_SECRETS_HASH", secretsHash(env))}
	for _, k := range keys {
		opts = append(opts, llb.AddSecret(k, llb.SecretID(k), llb.SecretAsEnv(true)))
	}
	return llb.Image(railpackBuilder).Run(opts...).AddMount("/out", llb.Scratch())
}

func releaseState(arch string) llb.State {
	url, sum, _ := prepareArch(arch)
	return llb.HTTP(url, llb.Checksum(sum), llb.Filename("railpack.tar.gz"))
}

func secretsHash(env map[string][]byte) string {
	h := sha256.New()
	for _, k := range slices.Sorted(maps.Keys(env)) {
		fmt.Fprintf(h, "%s=%s\n", k, env[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// railpackAttrs builds the plan with Railpack's frontend, the version that
// wrote it. It reads no label: options, so the image has no yawl.commit.
func railpackAttrs(hash string) map[string]string {
	return map[string]string{"source": railpackFrontend, "filename": railpackPlan, "build-arg:secrets-hash": hash}
}

func railpackSolve(ctx context.Context, c *client.Client, dir string, r Request) error {
	if _, _, err := prepareArch(goruntime.GOARCH); err != nil {
		return err
	}
	b, err := connect(ctx, c)
	if err != nil {
		return err
	}
	defer b.Close()
	ctxFS, err := fsutil.NewFS(dir)
	if err != nil {
		return err
	}
	planDir, err := os.MkdirTemp("", "yawl-railpack-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(planDir)
	rel, err := releaseState(goruntime.GOARCH).Marshal(ctx)
	if err != nil {
		return err
	}
	if err := runSolve(ctx, b, rel, bk.SolveOpt{}, r); err != nil {
		return fmt.Errorf("Railpack %s could not be downloaded: %w", railpackVersion, err)
	}
	def, err := prepareState(r.Secrets, goruntime.GOARCH).Marshal(ctx)
	if err != nil {
		return err
	}
	if err := runSolve(ctx, b, def, bk.SolveOpt{LocalMounts: map[string]fsutil.FS{"context": ctxFS},
		Session: []session.Attachable{secretsprovider.FromMap(r.Secrets)},
		Exports: []bk.ExportEntry{{Type: bk.ExporterLocal, OutputDir: planDir}}}, r); err != nil {
		return fmt.Errorf("Railpack could not plan this repository: %w", err)
	}
	planFS, err := fsutil.NewFS(planDir)
	if err != nil {
		return err
	}
	return runSolve(ctx, b, nil, bk.SolveOpt{
		Frontend:      "gateway.v0",
		FrontendAttrs: railpackNoCache(railpackAttrs(secretsHash(r.Secrets)), r.NoCache, r.Image),
		LocalMounts:   map[string]fsutil.FS{"context": ctxFS, "dockerfile": planFS},
		Session:       []session.Attachable{secretsprovider.FromMap(r.Secrets)},
		Exports:       []bk.ExportEntry{{Type: "moby", Attrs: map[string]string{"name": r.Image}}},
	}, r)
}

// railpackNoCache gives a build without cache a cache key of its own (the
// image, one per deploy): Railpack's frontend reads no no-cache attribute.
func railpackNoCache(attrs map[string]string, noCache bool, key string) map[string]string {
	if noCache {
		attrs["build-arg:cache-key"] = key
	}
	return attrs
}
