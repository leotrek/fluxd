# Containerd consumer validation (development only)

Status on 2026-10-24: **not run**. Host is macOS; no host `containerd`, `ctr`
or `nerdctl` is installed, Colima is stopped, and Docker has no running server.
`colima ssh -- ctr version` fails with `colima not running`. A Linux VM or Linux
machine with an accessible containerd service is required. A macOS Unix socket
cannot simply be bind-mounted through a VM filesystem share: run fluxd inside
the same Linux VM as containerd.

On that Linux host, from this repository (Go, Python 3, and privileged access to
containerd required), build a scratch image without a registry/image download:

```sh
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -o bin/consumer ./dev/consumer
python3 dev/containerd/image.py bin/consumer /tmp/fluxd-consumer.tar "$(go env GOARCH)"
sudo ctr --namespace fluxd-validation images import /tmp/fluxd-consumer.tar
make build
go run ./examples
./bin/fluxd -config examples/config.json
```

In a second terminal, wait until `GetAcquisition` returns a READY record:

```sh
./bin/fluxctl -op GetAcquisition -id optical-0001
sudo ctr --namespace fluxd-validation run --rm \
  --user "$(id -u):$(id -g)" \
  --mount "type=bind,src=$PWD,dst=/fluxd,options=rbind:ro" \
  docker.io/library/fluxd-consumer:baseline fluxd-consumer-ok \
  /consumer -socket /fluxd/fluxd.sock -id optical-0001
```

The development consumer selects the metadata codec, verifies encoded and raw
SHA-256 plus byte counts, and computes sample count, sum, minimum and maximum
while decoding in bounded workspace. Successful JSON output appears only after
integrity checks and ReleaseAcquisition. Compare processing results against the
source, for example for the generated optical fixture:

```sh
python3 - <<'PY'
import struct
v = [s[0] for s in struct.iter_unpack('<H', open('optical.raw', 'rb').read())]
print(dict(samples=len(v), sum=sum(v), min=min(v), max=max(v)))
PY
```

Repeat the ctr command with a unique task name and `-fail-after 64`; require a
nonzero exit and the `injected consumer failure` diagnostic. Repeat the healthy
command with another task name; require identical verified sample statistics.
This checks a process failure followed by a fresh container against retained data.
Also kill a running large-data consumer using `ctr tasks kill --signal SIGKILL`
and rerun; injection alone does not cover abrupt task termination.

Use `/usr/bin/time -v sudo ctr ...` around each run to capture launch-to-exit wall
time and client resource use. `consumer_seconds` starts inside the executable;
the difference is an estimate of runtime/launch/teardown overhead, not pure startup.
Collect task cgroup CPU/memory/io.stat while the task is alive for actual consumer
resources. Do not treat ctr client RSS as container RSS. Record containerd/runc,
kernel, image digest, UID, mounts, hardware and warm/cold image-cache conditions.
No container runtime is required by the production daemon.
