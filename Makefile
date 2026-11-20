# Tests that need Docker run inside a Linux container on the Docker host's own
# network and PID namespace, so /proc/<pid>/net/tcp and container IPs are real.
.PHONY: test docker-test wg-test fw-test e2e web web-test watch watch-test release site-release accept-releases hcloud-token hetzner boxes boxes-clean banner banner-text github-release

E2E_RUN ?= .
E2E_TIMEOUT ?= 35m

LINUX = docker run --rm --network host --pid host -e YAWL_E2E_HOLD \
	-v /var/run/docker.sock:/var/run/docker.sock -v /run/yawl:/run/yawl \
	-v $(CURDIR):/src -v yawl-gomod:/go/pkg/mod -v yawl-gocache:/root/.cache/go-build \
	-w /src golang:1

test:
	go test ./...

docker-test:
	$(LINUX) go test -tags docker -count=1 ./internal/agent/... ./install/

wg-test:
	docker run --rm --privileged -v $(CURDIR):/src -v yawl-gomod:/go/pkg/mod -v yawl-gocache:/root/.cache/go-build -w /src golang:1 go test -tags wglive -count=1 ./internal/agent/wg/

fw-test:
	docker run --rm --privileged -v $(CURDIR):/src -v yawl-gomod:/go/pkg/mod -v yawl-gocache:/root/.cache/go-build -w /src golang:1 go test -tags fwlive -count=1 ./internal/agent/fw/

web:
	cd web && npm ci && npm run build

web-test:
	cd web && npm test

watch:
	cd watch && npm ci && npm run build

watch-test:
	cd watch && npm ci && npm test && npm run relay:check

e2e: web watch
	$(LINUX) go test -tags e2e -count=1 -timeout $(E2E_TIMEOUT) -v -run '$(E2E_RUN)' ./test/e2e/

image: web watch
	docker build -f deploy/yawl.Dockerfile --build-arg VERSION=$(VERSION) -t yawl:$(VERSION) .

override VERSION := $(patsubst v%,%,$(VERSION))
release: web watch
	@test -n "$(VERSION)" -a -n "$(KEY)" || (echo "usage: make release VERSION=1.2.3 KEY=<private key path>"; exit 1)
	$(eval PUB := $(shell go run ./tools/sign pub $(KEY)))
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath \
			-ldflags "-X main.version=$(VERSION) -X github.com/behzodhalil/yawl/internal/update.ReleaseKey=$(PUB)" \
			-o dist/v$(VERSION)/yawl-linux-$$arch ./cmd/yawl || exit 1; \
	done
	go run ./tools/sign sums --version $(VERSION) dist/v$(VERSION) $(KEY)
	go run ./tools/sign stamp install/install.sh $(KEY) > dist/v$(VERSION)/install.sh

SITE ?= $(HOME)/Projects/yawl-site
site-release:
	@test -n "$(VERSION)" -a -f dist/v$(VERSION)/SHA256SUMS.sig || (echo "make release VERSION=$(VERSION) KEY=... first"; exit 1)
	mkdir -p $(SITE)/public/releases/v$(VERSION)
	cd dist/v$(VERSION) && cp yawl-linux-amd64 yawl-linux-arm64 SHA256SUMS SHA256SUMS.sig install.sh $(SITE)/public/releases/v$(VERSION)/
	cp dist/v$(VERSION)/install.sh $(SITE)/public/install.sh
	printf '{"tag_name": "v%s"}\n' $(VERSION) > $(SITE)/public/releases/latest

github-release:
	@test -n "$(VERSION)" -a -f dist/v$(VERSION)/SHA256SUMS.sig || (echo "make release VERSION=$(VERSION) KEY=... first"; exit 1)
	@git diff --quiet HEAD || (echo "commit and push first: the release is tagged at HEAD"; exit 1)
	gh release create v$(VERSION) --target $$(git rev-parse HEAD) --title v$(VERSION) --generate-notes \
		$(addprefix dist/v$(VERSION)/,yawl-linux-amd64 yawl-linux-arm64 SHA256SUMS SHA256SUMS.sig install.sh)

HZ_A = 0.0.1-accept
HZ_B = 0.0.2-accept
accept-releases:
	@mkdir -p dist; test -f dist/hetzner.key || go run ./tools/sign keygen dist/hetzner.key >/dev/null
	$(MAKE) release VERSION=$(HZ_A) KEY=dist/hetzner.key
	$(MAKE) release VERSION=$(HZ_B) KEY=dist/hetzner.key

hcloud-token:
	@test -n "$$HCLOUD_TOKEN" || { echo "hetzner needs HCLOUD_TOKEN; a run costs about an hour of a cx22." >&2; exit 1; }

hetzner: hcloud-token accept-releases
	YAWL_HZ_DIST=$(CURDIR)/dist YAWL_HZ_A=$(HZ_A) YAWL_HZ_B=$(HZ_B) go test -tags hetzner -count=1 -timeout 90m -v ./test/hetzner/

# boxes runs the same journeys on two Docker containers that boot systemd:
# no cloud, no token, about 20 minutes. It needs 6 GB
# in Docker Desktop's VM. boxes-clean removes the boxes, their network, their
# image caches and the box image.
boxes: accept-releases
	$$(command -v caffeinate >/dev/null && echo caffeinate -i) env YAWL_BOX_DIST=$(CURDIR)/dist YAWL_BOX_A=$(HZ_A) YAWL_BOX_B=$(HZ_B) go test -tags boxes -count=1 -timeout 60m -v -run TestBoxes$$ ./test/boxes/

boxes-clean:
	-docker rm -f box-one box-two box-s3
	-docker network rm yawl-boxes
	-docker volume rm -f yawl-box-one-docker yawl-box-two-docker yawl-box-one-containerd yawl-box-two-containerd
	-docker images -q yawl-box | xargs -r docker rmi -f

banner:
	node tools/banner/hall.mjs

banner-text:
	@test -n "$(FONT)" || (echo "usage: make banner-text FONT=<folder with IoskeleyMono-Regular.ttf, -Medium.ttf, -SemiBold.ttf>"; exit 1)
	python3 tools/banner/text.py $(FONT)
	node tools/banner/hall.mjs
