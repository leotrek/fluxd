# Local API

[Docs](README.md) / API · [Codec compatibility](ccsds.md) · [Socket permissions](operations.md)

**Transport:** Unix domain socket · **Default:** `./fluxd.sock` · **Access:** trusted service UID, mode 0600.

## Framing

```text
CONTROL FRAME
┌────────────────────────┬─────────────────────────────┐
│ 4-byte length          │ Exactly length bytes        │
│ unsigned, big-endian   │ UTF-8 JSON                  │
└────────────────────────┴─────────────────────────────┘

SUCCESSFUL OpenAcquisition
┌────────────────────────┬─────────────────────────────┐
│ Framed JSON response   │ Exactly response.bytes      │
│ record + body length   │ binary payload bytes        │
└────────────────────────┴─────────────────────────────┘
```

| Limit | Value / behavior |
|---|---|
| Request / response JSON | ≤16 KiB / ≤4 MiB |
| Operation deadline | 30 seconds, including binary transfer |
| List page | Default 100; maximum 256; sorted IDs |
| Open sessions | One logical handle per connection |
| Invalid framing | Zero/oversized length, malformed JSON, unknown fields or trailing JSON close the connection |

## Operations

Each request and response below is carried in a control frame.

| Operation | Request | Successful response |
|---|---|---|
| Status | `{"op":"GetStatus"}` | `{"status":"ready"}` |
| List | `{"op":"ListAcquisitions","offset":0,"limit":100}` | `records` array of completed records |
| Metadata | `{"op":"GetAcquisition","id":"optical-0001"}` | `record` object |
| Open | `{"op":"OpenAcquisition","id":"optical-0001"}` | `record`, `bytes`, then the binary body |
| Release | `{"op":"ReleaseAcquisition","id":"optical-0001"}` | `{"status":"released"}` |

`ready` in GetStatus describes the control service, not completion of ongoing replay. Offset/limit must be nonnegative; limit 0 selects the default. Zero-valued optional response fields may be omitted, including an empty records page. Known fields irrelevant to an operation are currently ignored.

## Service responsibilities

Fluxd owns acquisition ingestion, compression, storage and serving compressed
payloads. Consumers such as orbitald own their socket clients, decompression,
checksum verification and staging for execution. Services communicate through
this wire protocol and the documented compression formats; orbitald does not
import fluxd Go packages or require its source checkout to build.

## Read one acquisition

`OpenAcquisition` returns the stored compressed stream. The consumer must decompress it using `compression.codec` and its declared standard/profile before interpreting sample values. Verify the encoded SHA-256 while receiving, then verify the reconstructed byte count and raw SHA-256 before using the data. `deflate` uses a zlib-wrapped stream; `identity` passes raw bytes through unchanged.

```mermaid
sequenceDiagram
    participant App as External application
    participant API as fluxd socket
    participant Store as READY store
    App->>API: GetAcquisition(id)
    API-->>App: Framed metadata
    App->>API: OpenAcquisition(id)
    API->>Store: Open immutable payload
    API-->>App: Framed record + bytes=N
    API-->>App: Exactly N binary bytes
    Note over App: Verify encoded hash; decode;<br/>verify raw size and hash
    App->>API: ReleaseAcquisition(id)
    API-->>App: released
```

| Consumer rule | Reason |
|---|---|
| Finish the binary body before sending another request | Preserve message boundaries |
| Treat early EOF as failure | The announced body was not delivered |
| Select the exact codec/standard/profile | Unknown formats must fail |
| Verify `checksum`, `raw_checksum` and raw byte count | Detect transfer or reconstruction errors |
| Release after reading; close on failure | Release session resources |

**One payload is one compression unit.** Transport chunks are not independently decodable. Release does not delete data; disconnect releases resources. Transfer errors close the connection rather than inserting JSON into a binary body.

## Metadata record · schema 1

| Group | Fields / meaning |
|---|---|
| Identity | `schema_version=1`, `acquisition_id`, `sensor_type`, RFC3339 `timestamp` |
| Import provenance (optional) | `source`: `kind`, `path`, `pixel_format`, optional zero-based `frame_index`; imported media timestamps are source file modification times, not video presentation times |
| Geometry | `width`, `height`, `bands` |
| Samples | `sample_bit_depth`, `storage_bit_depth`, `sample_type`, `byte_order`, `layout` |
| Codec | `compression.codec`, `mode`; optional `standard`, `profile` |
| Sizes | `uncompressed_bytes` = physical raw size; `compressed_bytes` = binary body size |
| Integrity | `checksum` = encoded SHA-256; `raw_checksum` = raw SHA-256; lowercase hex |
| Publication | `status=READY` |

For CCSDS, `standard` is `CCSDS 123.0-B-1` and `profile` is `reduced-p0-column-sample-adaptive-v1`. The stream contains its 19-byte header; restore raw container width/endian from JSON. [Sample descriptors and size formulas →](configuration.md#describe-the-raw-samples)

## Errors

```json
{"error":"not_found"}
```

| Code | Meaning |
|---|---|
| `invalid_id` | Unsafe or malformed identifier |
| `invalid_pagination` | Invalid offset/limit |
| `unknown_operation` | Unsupported operation |
| `not_found` | Acquisition unavailable; file-open failures also map here |
| `release_required` | Connection already has an open handle |
| `not_open` | Release ID does not match the current handle |

Internal filesystem paths are not exposed. Capacity/admission or transport failures may close the connection. Applications and container runtimes remain external to fluxd.
