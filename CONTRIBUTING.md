# Contributing

## Build and test

You need Go (the version in `go.mod`), Node 22 or newer, and Docker for the container tests.

```
make web watch      # build the dashboard and the watcher
make test           # Go tests
make web-test       # dashboard tests
make watch-test     # watcher and relay tests
make docker-test    # agent tests against a real Docker
```

`make e2e`, `make boxes` and `make hetzner` run whole journeys on containers or a real server; CI does not run them.

## Changes

- Run `gofmt` and `go vet ./...`. CI runs both, plus every test above except Docker.
- After editing `internal/server/store/queries` or a migration, run `sqlc generate` and commit the result.
- Write code that says what it does: names, types, named constants and tests, not comments. A comment is only for a fact about something we cannot change, like a vendor's limit.
- Add a test with every behaviour change.

Security problems go through [SECURITY.md](SECURITY.md), not issues.
