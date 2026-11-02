# Optional independent complete-file interoperability

Status on 2026-10-24: **not executed**. Java 25 is installed, but EMPORDA
is not present and shell DNS access to GitHub fails. Existing static vectors
exercise independent predictor/entropy output with a local header adapter;
they do not establish complete-file interoperability.

Upstream candidate: https://github.com/gici-uab/emporda (GICI, Java, B-1).
Pin the source commit and record the archive/JAR SHA-256, Java version, exact
adapter source and argv alongside results. Do not patch either implementation
to produce a passing result without recording the change.

The opt-in Go test invokes a separately installed executable using argv arrays:

```json
{
  "decode": ["/absolute/path/emporda-adapter", "decode", "{input}", "{output}", "{metadata}"],
  "encode": ["/absolute/path/emporda-adapter", "encode", "{input}", "{output}", "{metadata}"]
}
```

```sh
FLUXD_INTEROP_CONFIG=/absolute/path/runner.json \
  go test -tags interop -count=1 -v ./dev/interop
```

The adapter must translate metadata into the independent tool's documented
CLI/configuration, select reduced prediction, P=0, column sums and
sample-adaptive entropy, and preserve the original raw layout, container width,
signedness and byte order. The adapter and EMPORDA are not bundled; this is a
runner contract, not a completed EMPORDA integration. Never point this at fluxd's
own codec and describe it as independent validation.

Both directions compare complete reconstructed raw bytes exactly, including
boundary values and signed containers. Decoder-only installations skip reverse
encoding explicitly. Failed commands, timeouts, unsupported profiles and byte
mismatches fail the corresponding test. The default suite does not run Java.

| Synthetic configuration | Geometry | Samples / raw format | Native → independent | Independent → native |
|---|---|---|---|---|
| Optical | 32×16×4 | 12-bit unsigned, BIL, 16-bit little endian | Not run | Not run |
| Thermal | 32×16×1 | 14-bit signed, BSQ, 16-bit big endian | Not run | Not run |
| Hyperspectral | 32×16×64 | 16-bit unsigned, BIP, 16-bit little endian | Not run | Not run |

This three-case starting matrix is not exhaustive or a substitute for real
sensor inputs, official conformance vectors, or the full precision/layout matrix.
