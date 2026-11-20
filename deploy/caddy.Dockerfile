# Caddy with Cloudflare's DNS module, for a Yawl Cloud cell server's one
# wildcard certificate. caddy:2, which agents run by
# default, has no DNS module. Build anywhere with Docker, push to the
# operator's registry, and name it in the agent's YAWL_CADDY_IMAGE:
#   docker build -f deploy/caddy.Dockerfile -t <registry>/caddy-cloudflare:2.10 deploy
# prefer_wildcard, which the agent turns on, needs Caddy 2.8 or later. The
# builder image's CADDY_VERSION picks Caddy; the module is its newest, and
# `docker run --rm <image> caddy list-modules --versions` says which went in.
ARG CADDY=2.10
FROM caddy:${CADDY}-builder AS build
RUN xcaddy build --with github.com/caddy-dns/cloudflare

FROM caddy:${CADDY}
COPY --from=build /usr/bin/caddy /usr/bin/caddy
