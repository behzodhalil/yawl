# test/boxes

The acceptance run on two local "machines" (`make boxes`, build tag `boxes`).

- `box.go`: the harness. It builds `yawl-box:<Dockerfile hash>` from `Dockerfile` and starts `box-one` (172.30.0.11), `box-two` (172.30.0.12) and `box-s3` (172.30.0.20:9000) on `yawl-boxes`. It talks to them through the Docker API only (no `os/exec`).
- `box_test.go`: the harness's own tests (`go test -tags boxes -run TestBox ./test/boxes/`).
- `accept_test.go`: `TestBoxes`, one ordered run: install, the installer again, a second box, a move with a database, and an update under load. A failed run prints each box's containers and journals.
