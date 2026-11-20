FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=${VERSION}" -o /yawl ./cmd/yawl

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 yawl && mkdir /data && chown yawl /data
COPY --from=build /yawl /usr/local/bin/yawl
USER yawl
ENV YAWL_LISTEN=0.0.0.0:7000
VOLUME /data
EXPOSE 7000
ENTRYPOINT ["yawl", "run"]
