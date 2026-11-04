# fluxd

fluxd compresses sensor data, stores it on local disk, and serves it to applications through a Unix socket. It runs offline.

## Where is the storage?

**Data is stored in ordinary folders on the machine running fluxd.** There is no external database or cloud storage.

| How you add data | Default storage folder |
|---|---|
| Daemon / example replay | `./fluxd-state/` |
| Image import | `./data/state/image/` |
| Video import | `./data/state/video/` |
| Raw import | `./data/state/raw/` |

Paths starting with `./` are relative to **the directory where you run the command**, not the configuration file. Running from this repository puts these folders inside it.

Each stored acquisition has two files:

```text
<storage-folder>/acquisitions/<id>/
├── payload          # compressed samples
└── metadata.json    # dimensions, codec, sizes and checksums
```

Change the daemon's folder with `"state_dir": "/path/to/storage"` in its JSON config. For imports, use `-state-dir /path/to/storage`. To serve imported data, the daemon's `state_dir` must point to that same folder. `fluxd.sock` is the API connection, not the data store.

## Build and run

Requires Go 1.24 or newer.

Run from the repository root:

```sh
make build
go run ./examples
./bin/fluxd -config examples/config.json
```

This imports the generated optical sample into `./fluxd-state/` and keeps the daemon running. In another terminal:

```sh
./bin/fluxctl -op ListAcquisitions
./bin/fluxctl -op GetAcquisition -id optical-0001
```

Stop with **Ctrl+C**. Stored data survives restarts; existing acquisition IDs are skipped. `fluxctl` displays metadata; applications retrieve and decode payloads through the [local API](docs/api.md).

## Import your own files

Stop any daemon using the destination store before importing. Source files are left in place.

```sh
# PNG/JPEG → ./data/state/image/
./bin/fluximport -kind image -input ./data/input/images

# Video frames → ./data/state/video/ (requires ffmpeg and ffprobe)
./bin/fluximport -kind video -input ./data/input/videos -max-frames 10

# Headerless .raw files with matching .json descriptors → ./data/state/raw/
./bin/fluximport -kind raw -input ./data/input/raw
```

You can use any file or directory with `-input`. Video imports also save uncompressed `frame.rgb` files beside each payload; results include `source.rgb_path` and the original video in `source.path`. These extra files use additional disk space outside the compressed storage limit. Re-run an import to add RGB files to existing video acquisitions.

Image/video imports default to RGB8 samples; compression preserves those samples, not the original media file. Video imports store frames without audio or timing. See [import options and supported formats](docs/importing.md) for precision, alpha, raw descriptors and limits.

To serve imported images:

```sh
./bin/fluxd -config examples/import-images.json
```

Use `examples/import-videos.json` or `examples/import-raw.json` for the other stores. Run one example daemon at a time; they share `./fluxd.sock`.

## More information

- [Configuration](docs/configuration.md): storage, replay and resource limits.
- [Operations](docs/operations.md): installation, systemd and troubleshooting.
- [Local API](docs/api.md): connecting an application and retrieving data.
- [Documentation index](docs/README.md): architecture, development and benchmarks.

The default codec is a lossless CCSDS 123.0-B-1 subset. Full-file interoperability with independent decoders is not verified; see [codec support and validation](docs/ccsds.md).
