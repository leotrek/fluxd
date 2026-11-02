#!/usr/bin/env python3
"""Create a scratch Docker archive accepted by ctr images import; no image pull."""
import hashlib, io, json, pathlib, sys, tarfile
binary, output, arch = sys.argv[1:]
if arch not in ('arm64', 'amd64', 'arm'):
    raise SystemExit('architecture must match the Linux containerd host')
def add(t, name, data, mode=0o644):
    info = tarfile.TarInfo(name)
    info.size, info.mode, info.mtime = len(data), mode, 0
    t.addfile(info, io.BytesIO(data))
layer = io.BytesIO()
with tarfile.open(fileobj=layer, mode='w') as t:
    add(t, 'consumer', pathlib.Path(binary).read_bytes(), 0o755)
blob = layer.getvalue()
config = json.dumps({'architecture': arch, 'os': 'linux', 'config': {'Entrypoint': ['/consumer']}, 'rootfs': {'type': 'layers', 'diff_ids': ['sha256:'+hashlib.sha256(blob).hexdigest()]}}, sort_keys=True).encode()
name = hashlib.sha256(config).hexdigest()+'.json'
manifest = [{'Config': name, 'RepoTags': ['fluxd-consumer:baseline'], 'Layers': ['layer.tar']}]
with tarfile.open(output, 'w') as t:
    add(t, 'layer.tar', blob)
    add(t, name, config)
    add(t, 'manifest.json', json.dumps(manifest).encode())
