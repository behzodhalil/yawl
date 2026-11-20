#!/bin/sh
# Yawl installer: curl -fsSL https://useyawl.com/install.sh | sh
#
# Checks the machine, installs Docker if it is missing, installs the yawl
# binary (verified), starts yawl-server and yawl-agent under systemd, joins
# the agent to its own panel and prints the one-time claim link.
#
#   YAWL_VERSION=1.2.3     a release (default: the latest)
#   YAWL_BINARY=/path      a binary you built, instead of a download
#   YAWL_PUBLIC_IP=1.2.3.4 this machine's public address (default: asked of api.ipify.org)
#   YAWL_INSTALL_DRY=1     run the checks and say what would happen; change nothing
#   YAWL_INSTALL_VERIFY_ONLY=1  download and verify the release, then stop (tests)
#   YAWL_RELEASES=<url>    where releases are (default: https://useyawl.com/releases; tests and mirrors)
#   YAWL_TEST_BUSY, YAWL_TEST_OURS, YAWL_TEST_IPIFY  stand in for /proc/net/tcp, a
#                          running Yawl, and api.ipify.org's answer (tests)
set -eu

RELEASE_KEY_PEM=""
RELEASES=${YAWL_RELEASES:-https://useyawl.com/releases}
RELEASES=${RELEASES%/}
LATEST="$RELEASES/latest"

dry=${YAWL_INSTALL_DRY:-}
say() { printf '%s\n' "$*"; }
fail() { printf '%s\n' "$*" >&2; exit 1; }

join_url=""; join_token=""
if [ -n "${YAWL_JOIN:-}" ]; then
  join_url=${YAWL_JOIN%% *}; join_token=${YAWL_JOIN#* }
  case "$join_url" in http://*|https://*) ;; *) join_url="" ;; esac
  [ -n "$join_url" ] && [ "$join_token" != "$YAWL_JOIN" ] && [ -n "$join_token" ] \
    || fail "YAWL_JOIN is the panel's address and a join token, like 'https://panel.acme.uz TOKEN'."
fi


[ -r /etc/os-release ] || fail "Yawl supports Ubuntu 22.04, 24.04 and 26.04 and Debian 12; this system has no /etc/os-release."
. /etc/os-release
case "${ID:-}:${VERSION_ID:-}" in
  ubuntu:22.04|ubuntu:24.04|ubuntu:26.04|debian:12) say "ok os $ID $VERSION_ID" ;;
  *) fail "Yawl supports Ubuntu 22.04, 24.04 and 26.04 and Debian 12; this is ${ID:-unknown}${VERSION_ID:+ $VERSION_ID}." ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "Yawl runs on amd64 and arm64; this machine is $(uname -m)." ;;
esac
say "ok arch $arch"

# A listening socket on 80 or 443 (state 0A) in /proc/net/tcp*: no ss or netstat needed.
busy_ports() {
  if [ -n "${YAWL_TEST_BUSY+x}" ]; then [ -z "$YAWL_TEST_BUSY" ] || printf '%s ' $YAWL_TEST_BUSY; return; fi
  cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk '$4 == "0A" { split($2, a, ":"); p = a[length(a)]; if (p == "0050") print 80; if (p == "01BB") print 443 }' | sort -u | tr '\n' ' '
}
ours() {
  if [ -n "${YAWL_TEST_OURS+x}" ]; then [ "$YAWL_TEST_OURS" = 1 ]; return; fi
  [ -x /usr/local/bin/yawl ] && command -v docker >/dev/null 2>&1 \
    && [ "$(docker inspect -f '{{.State.Running}}' yawl-caddy 2>/dev/null)" = true ]
}
busy=$(busy_ports)
if [ -n "$busy" ] && ours; then
  say "ok ports 80 443 (Yawl's own Caddy)"
else
  [ -z "$busy" ] || fail "Port(s) ${busy}are in use. Yawl's Caddy serves 80 and 443; stop what listens there first."
  say "ok ports 80 443"
fi

mem=$(awk '/^MemTotal:/ { print $2 }' /proc/meminfo)
[ "${mem:-0}" -ge 900000 ] || fail "Yawl needs at least 1 GB of RAM; this machine has $((${mem:-0} / 1024)) MB."
say "ok memory $((mem / 1024)) MB"

mkdir_for_df=/var/lib
free=$(df -Pk "$mkdir_for_df" | awk 'NR == 2 { print $4 }')
[ "${free:-0}" -ge 10485760 ] || fail "Yawl needs 10 GB free on /var/lib; there are $((${free:-0} / 1048576)) GB."
say "ok disk $((free / 1048576)) GB free"

ufw_active() { command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; }
if [ -n "$dry" ]; then
  say "would set net.ipv4.ip_nonlocal_bind=1 (a database binds its private address before the network is up at boot)"
  if ufw_active; then
    say "would open UDP 51820 in ufw for the private network"
  else
    say "note: the private network uses UDP 51820 between your servers; open it in your cloud's firewall"
  fi
fi

if [ -n "${YAWL_BINARY:-}" ]; then
  [ -f "$YAWL_BINARY" ] || fail "YAWL_BINARY $YAWL_BINARY does not exist."
  say "ok binary $YAWL_BINARY"
elif [ -z "$RELEASE_KEY_PEM" ]; then
  nokey="This installer has no release key, so it cannot verify a download. Set YAWL_BINARY to a yawl binary you built."
  [ -n "$dry" ] && say "note: $nokey" || fail "$nokey"
fi

learn_ip() {
  if [ -n "${YAWL_PUBLIC_IP:-}" ]; then ip=$YAWL_PUBLIC_IP; return; fi
  answer=${YAWL_TEST_IPIFY-$(curl -fsS --max-time 10 https://api.ipify.org 2>/dev/null || true)}
  case "$answer" in
    *[!0-9a-fA-F.:]*|"") ok= ;;
    *.*|*:*) ok=1 ;;
    *) ok= ;;
  esac
  [ -n "$ok" ] && [ "${#answer}" -ge 7 ] && [ "${#answer}" -le 39 ] \
    || fail "Could not learn this machine's public address from api.ipify.org (got: $(printf '%.60s' "$answer")); set YAWL_PUBLIC_IP."
  ip=$answer
}
if [ -n "$dry" ] && { [ -n "${YAWL_PUBLIC_IP:-}${YAWL_TEST_IPIFY+x}" ] || command -v curl >/dev/null 2>&1; }; then
  learn_ip
  say "ok address $ip"
fi

if command -v docker >/dev/null 2>&1; then
  say "ok docker"
else
  say "install docker (from download.docker.com)"
fi

if [ -n "$dry" ]; then
  [ -z "$join_url" ] || say "would install the agent only and join $join_url"
  say "dry run: nothing was changed"
  exit 0
fi

[ "$(id -u)" = 0 ] || fail "Run the installer as root (sudo sh)."


if [ -z "${YAWL_BINARY:-}" ]; then
  for tool in curl openssl; do
    command -v "$tool" >/dev/null 2>&1 || { apt-get update -q >/dev/null && apt-get install -y -q curl openssl >/dev/null; }
  done
  version=${YAWL_VERSION:-}
  if [ -z "$version" ]; then
    version=$(curl -fsSL "$LATEST" | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1)
    [ -n "$version" ] || fail "Could not find the latest Yawl release."
  fi
  version=${version#v}
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  for f in "yawl-linux-$arch" SHA256SUMS SHA256SUMS.sig; do
    curl -fsSL "$RELEASES/v$version/$f" -o "$tmp/$f" || fail "Release v$version has no $f."
  done
  printf '%s\n' "$RELEASE_KEY_PEM" > "$tmp/key.pem"
  openssl pkeyutl -verify -pubin -inkey "$tmp/key.pem" -rawin -in "$tmp/SHA256SUMS" -sigfile "$tmp/SHA256SUMS.sig" >/dev/null 2>&1 \
    || fail "The release signature does not match this installer's key."
  (cd "$tmp" && grep "  yawl-linux-$arch\$" SHA256SUMS | sha256sum -c - >/dev/null 2>&1) \
    || fail "yawl-linux-$arch does not match SHA256SUMS."
  named=$(head -n 1 "$tmp/SHA256SUMS")
  case "$named" in
    "# yawl $version") ;;
    "# yawl "*) fail "This release's SHA256SUMS names v${named#\# yawl }, not v$version." ;;
    *) fail "This release's SHA256SUMS does not name its version; it was made before version binding." ;;
  esac
  say "ok verified v$version"
  YAWL_BINARY="$tmp/yawl-linux-$arch"
fi
if [ -n "${YAWL_INSTALL_VERIFY_ONLY:-}" ]; then
  exit 0
fi


mkdir -p /etc/sysctl.d
printf 'net.ipv4.ip_nonlocal_bind = 1\n' > /etc/sysctl.d/90-yawl.conf
sysctl -q -p /etc/sysctl.d/90-yawl.conf 2>/dev/null \
  || say "note: could not set net.ipv4.ip_nonlocal_bind; a database shared over the private network may need a redeploy after a reboot"
if ufw_active; then
  ufw allow 51820/udp >/dev/null && say "ok ufw: opened UDP 51820 for the private network"
else
  say "note: the private network uses UDP 51820 between your servers; open it in your cloud's firewall"
fi


if ! command -v docker >/dev/null 2>&1; then
  apt-get update -q
  apt-get install -y -q ca-certificates curl gnupg
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
    "$(dpkg --print-architecture)" "$ID" "$VERSION_CODENAME" > /etc/apt/sources.list.d/docker.list
  apt-get update -q
  apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin
  systemctl enable --now docker
fi
command -v curl >/dev/null 2>&1 || { apt-get update -q && apt-get install -y -q curl; }
install -m 0755 "$YAWL_BINARY" /usr/local/bin/yawl

say "installed $(/usr/local/bin/yawl version)"


install -d -m 0700 /etc/yawl /var/lib/yawl /var/lib/yawl/agent

unit_flag() {
  sed -n "s/.*$1[= ]\"\{0,1\}\([^ \"]*\).*/\\1/p" "$2" | head -n 1
}

if [ -n "$join_url" ]; then
cat > /etc/systemd/system/yawl-agent.service <<UNIT
[Unit]
Description=Yawl agent
After=network-online.target docker.service
Requires=docker.service

[Service]
ExecStart=/usr/local/bin/yawl agent
Restart=always
RestartSec=2
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
  if [ ! -f /var/lib/yawl/agent/server_url ]; then
    [ -n "${ip:-}" ] || learn_ip
    /usr/local/bin/yawl agent join "$join_url" "$join_token" --ip "$ip" --name "$(hostname)"
  fi
  systemctl daemon-reload
  systemctl enable yawl-agent
  systemctl restart yawl-agent
  say "Joined $join_url. This server is in the panel's list."
  exit 0
fi

server_unit=/etc/systemd/system/yawl-server.service
panel=127.0.0.1:7000
if [ -f "$server_unit" ]; then
  public=$(unit_flag --public-url "$server_unit")
  listen=$(unit_flag --listen "$server_unit")
  [ -z "$listen" ] || panel="127.0.0.1:${listen##*:}"
  say "kept the existing units (delete them to have them written again)"
else
  learn_ip
  dashed=$(printf '%s' "$ip" | tr '.:' '--')
  public="https://panel.$dashed.sslip.io"

cat > /etc/systemd/system/yawl-server.service <<UNIT
[Unit]
Description=Yawl control plane
After=network-online.target docker.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/yawl server --listen 127.0.0.1:7000 --public-url $public
Restart=always
RestartSec=2
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
fi

if [ ! -f /etc/systemd/system/yawl-agent.service ]; then
cat > /etc/systemd/system/yawl-agent.service <<UNIT
[Unit]
Description=Yawl agent
After=network-online.target docker.service
Requires=docker.service

[Service]
ExecStart=/usr/local/bin/yawl agent
Restart=always
RestartSec=2
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
fi
systemctl daemon-reload
systemctl enable yawl-server
systemctl restart yawl-server
i=0
until curl -fsS -o /dev/null "http://$panel/api/session"; do
  i=$((i + 1)); [ "$i" -lt 60 ] || fail "yawl-server did not start; see journalctl -u yawl-server."
  sleep 1
done

if [ ! -f /var/lib/yawl/agent/server_url ]; then
  [ -n "${ip:-}" ] || learn_ip
  token=$(/usr/local/bin/yawl server join-token --panel --public-url "http://$panel" | awk '{ print $NF }')
  /usr/local/bin/yawl agent join "http://$panel" "$token" --ip "$ip" --name "$(hostname)"
fi
systemctl enable yawl-agent
systemctl restart yawl-agent

errf=$(mktemp)
code=0
link=$(/usr/local/bin/yawl server claim-link --public-url "$public" 2>"$errf") || code=$?
say ""
say "Yawl is running. The panel's certificate may take a minute."
case $code in
  0) say "Claim this panel: $link" ;;
  3) say "This panel already has an owner: $public" ;;
  *) say "Could not make a claim link: $(sed 's/^yawl: //' "$errf")" ;;
esac
rm -f "$errf"
