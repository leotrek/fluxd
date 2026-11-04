# Import images, videos and raw data

Build all tools with `make build`, then run the following commands from the repository root. `fluximport` imports a single file or a directory into the same durable store used by `fluxd`. It exits when the batch completes. **Stop the daemon using that state directory before importing**; concurrent access is rejected by the store lock. No ingestion API or directory watcher is required.

### Directories and supported files

```text
data/
├── input/
│   ├── images/     # PNG and JPEG files
│   ├── videos/     # H.264, MP4, MKV, MOV, AVI, HEVC files
│   └── raw/        # headerless .raw files with matching .json descriptors
└── state/
    ├── image/      # imported image acquisitions
    ├── video/      # one acquisition per decoded video frame
    └── raw/        # imported raw acquisitions
```

The input folders are included in the repository; stores are created on demand. Input media and generated stores are ignored by Git. Source files are left in place. You can also use files or directories anywhere else with `-input`.

| Input | Import support | Output samples |
|---|---|---|
| PNG, JPEG (`.png`, `.jpg`, `.jpeg`) | Built-in Go decoders | Selected image pixel format |
| H.264 / AVC (`.h264`, `.264`), MP4, MKV, MOV, AVI, HEVC (`.hevc`, `.h265`) | Requires `ffmpeg` **and** `ffprobe` on PATH | RGB8 frames from the first video stream |
| Headerless `.raw` + same-stem `.json` | Built in | Original sample representation from the descriptor |
| TIFF / GeoTIFF, camera RAW (DNG/CR2/NEF), GIF | No built-in importer | Convert externally to a supported image or raw samples |

Video container and codec support depends on your FFmpeg build. Install FFmpeg using your operating system's package manager, then check `ffmpeg -version` and `ffprobe -version`. Images and raw imports do not require either executable. Directory imports select the extensions listed above, ignore unrelated files, and process files in sorted path order. `-recursive` includes nested folders; directory traversal skips symlinks. A single file is passed directly to the selected decoder regardless of extension.

### Images

Copy your PNG/JPEG files into `data/input/images/`, then import:

```sh
./bin/fluximport -kind image -input ./data/input/images

# Single file, preserving an 8-bit alpha channel:
./bin/fluximport -kind image -input /absolute/path/image.png -pixel-format rgba8

# Preserve a 16-bit grayscale PNG as little-endian 16-bit samples:
./bin/fluximport -kind image -input /absolute/path/sensor.png -pixel-format gray16

# Include subdirectories and select a different codec and store:
./bin/fluximport -kind image -input ./data/input/images -recursive \
  -backend deflate -state-dir ./data/state/image-deflate
```

| `-pixel-format` | Representation | Conversion effects |
|---|---|---|
| `rgb8` (default) | 3 interleaved unsigned 8-bit bands | Drops alpha; reduces higher precision to 8 bits |
| `rgba8` | 4 interleaved unsigned 8-bit bands | Preserves 8-bit alpha; reduces higher precision to 8 bits |
| `gray8` | 1 unsigned 8-bit band | Converts color to grayscale |
| `gray16` | 1 unsigned 16-bit band, little-endian | Preserves 16-bit grayscale input; converts color to grayscale |

Choose the format explicitly when alpha or precision matters. Full 16-bit RGB/RGBA import is not implemented; use raw import for that representation. Orientation tags, color profiles and other image metadata are not retained or applied. Lossless compression preserves the **converted sample bytes**, rather than the original PNG/JPEG file.

### Videos, including H.264

Copy videos into `data/input/videos/`. Start with a small frame limit because decoded video can be much larger than its encoded source:

```sh
./bin/fluximport -kind video -input ./data/input/videos -max-frames 10

# Import all frames from a single H.264 file:
./bin/fluximport -kind video -input /absolute/path/recording.h264

# Use a separate store and a different lossless backend:
./bin/fluximport -kind video -input /absolute/path/recording.mp4 \
  -max-frames 100 -backend deflate -state-dir ./data/state/video-deflate
```

Each frame becomes one RGB8/BIP acquisition. Its acquisition folder also contains `frame.rgb`: uncompressed, headerless RGB24 bytes (R, G, B per pixel). `source.rgb_path` gives its absolute path and `source.path` identifies the original video. Both paths appear in import results and stored metadata. Re-running an import adds missing RGB files to existing acquisitions after verifying their sample checksums. RGB files consume additional disk space and are excluded from `-max-storage-bytes`, which limits compressed payloads only. IDs contain a source-content hash and a zero-based frame number; metadata also records the source path and frame index. Frames retain decoder order without frame-rate resampling. Automatic rotation is disabled; decoded dimensions must remain constant throughout a video. Video only supports `-pixel-format rgb8` currently.

This is a frame import feature, not a video archive/player: audio, presentation timestamps, frame durations, rotation tags and original encoded bytes are not stored. The acquisition timestamp is the source file modification time, **not** the frame presentation time or sensor acquisition time. Keep the original video when those properties matter. Flux does not use motion between frames, so H.264 may be much smaller than the resulting acquisitions. Decoding and RGB8 conversion may change precision and color representation before lossless Flux compression.

### Raw sensor data

Put pairs such as `frame-0001.raw` and `frame-0001.json` into `data/input/raw/`. The descriptor must match the samples exactly. For example, this describes a headerless 640 × 480, three-band RGB8 file containing exactly **921,600 bytes**:

```json
{
  "schema_version": 1,
  "acquisition_id": "frame-0001",
  "sensor_type": "optical",
  "timestamp": "2026-11-04T12:00:00Z",
  "width": 640,
  "height": 480,
  "bands": 3,
  "sample_bit_depth": 8,
  "storage_bit_depth": 8,
  "sample_type": "uint",
  "byte_order": "little",
  "layout": "BIP"
}
```

```sh
./bin/fluximport -kind raw -input ./data/input/raw
./bin/fluximport -kind raw -input /absolute/path/frame-0001.raw -backend identity
```

Raw import preserves the descriptor's ID, timestamp, sensor type, precision, signedness, layout and byte order. Supported precisions are 8/10/12/14/16-bit integers in 8- or 16-bit containers; layouts are BSQ/BIL/BIP. Packed 10/12/14-bit samples and floating-point samples are unsupported. See [descriptor details](configuration.md#describe-the-raw-samples). A camera RAW file is not a headerless sensor array; renaming files does not convert them.

### Options, limits and repeated imports

Run `./bin/fluximport -help` for all flags:

| Option | Default | Purpose |
|---|---|---|
| `-kind` | `image` | `image`, `video`, or `raw` |
| `-input` | required | Source file or directory |
| `-state-dir` | `./data/state/<kind>` | Destination store; separate by kind by default |
| `-backend` | `ccsds-123` | `ccsds-123`, `deflate`, or `identity` |
| `-pixel-format` | `rgb8` | Image conversion format; video requires `rgb8`; ignored for raw |
| `-recursive` | `false` | Include nested input directories |
| `-max-frames` | `0` | Frames per video; 0 imports all; ignored for images/raw |
| `-max-acquisition-bytes` | `268435456` (256 MiB) | Maximum raw bytes per image/frame/acquisition; also bounds image decoder workspace estimate |
| `-max-storage-bytes` | `4294967296` (4 GiB) | Total compressed payload capacity; excludes metadata/filesystem overhead |

Image decoding holds the decoded image and converted samples in memory; the workspace check estimates up to eight bytes per pixel separately from the output buffer. Video import holds one decoded RGB frame in Go memory, while FFmpeg uses additional memory. These flags are not process RSS limits. There is no frame-count cap beyond the store's existing 10,000-acquisition limit; use `-max-frames` for initial tests. Raise capacity limits explicitly for larger batches.

Image/video IDs depend on source file content and pixel format, so unchanged imports are skipped and changed files receive new IDs. Identical files at different paths deduplicate; the first stored source path remains in metadata. The backend is not part of the ID: use separate stores to compare codecs. Raw IDs come from your JSON: use a new ID when changing raw input. Re-importing a video with a larger frame limit adds remaining frames, though decoding still starts at the beginning.

Imports fail on the first selected invalid file, decoder failure, or capacity error. Previously completed acquisitions remain available; a failed acquisition is not published. Input files must remain unchanged during import. Ctrl+C stops the command and its decoder subprocess.

### Inspect where the data is stored

Each successful import prints the acquisition ID, storage path, uncompressed size and compressed size. The compression ratio is `uncompressed_bytes / compressed_bytes`, measured against decoded/raw samples, not the source media file. Some data grows after encoding.

```text
data/state/image/acquisitions/<image-id>/
├── payload          # encoded sample bytes
└── metadata.json    # geometry, codec, sizes, checksums and source provenance
```

To inspect images through the local API after importing:

```sh
./bin/fluxd -config examples/import-images.json
```

In another terminal:

```sh
./bin/fluxctl -op ListAcquisitions
# Copy an ID from the list:
./bin/fluxctl -op GetAcquisition -id IMAGE_ID
```

For video or raw stores, start the daemon with `examples/import-videos.json` or `examples/import-raw.json` instead. These example configurations share `./fluxd.sock`, so run one at a time. For a custom destination, change `state_dir` in the serving config to match your import command.

The daemon serves stored data without replaying an input. `fluxctl` inspects metadata; an application must retrieve and decode the payload to reconstruct samples. See the [local API](api.md) and the development reconstruction client in `dev/consumer`.

