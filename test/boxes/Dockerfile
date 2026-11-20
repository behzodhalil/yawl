FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive container=docker
RUN apt-get update -q \
 && apt-get install -y -q systemd systemd-sysv dbus ca-certificates curl gnupg git python3 iproute2 \
 && install -m 0755 -d /etc/apt/keyrings \
 && curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc \
 && chmod a+r /etc/apt/keyrings/docker.asc \
 && printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable\n' "$(dpkg --print-architecture)" \
      > /etc/apt/sources.list.d/docker.list \
 && apt-get update -q \
 && apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin \
 && systemctl enable docker \
 && systemctl mask getty.target console-getty.service systemd-firstboot.service \
 && rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
