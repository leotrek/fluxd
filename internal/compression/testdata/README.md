# Codec regression vectors

`ccsds-profile-vectors.json` contains 60 static cases:

| Dimension | Coverage |
|---|---|
| Effective precision | 8, 10, 12, 14, 16 bits |
| Sample type | Signed and unsigned |
| Raw ordering | BSQ, BIL, BIP |
| Byte order | Little and big endian |
| Values | Nonconstant samples, boundaries and entropy-counter rescaling |

Tests compare encoded bytes and reconstruct the original raw samples. No external implementation, generator or runtime is required.

**Provenance:** independently generated predictor/entropy output, with a local B-1 header adapter. The source fingerprint is preserved in `reference_sha256`; author attribution remains in the JSON and [MIT license](REFERENCE-LICENSE). The original generator source SHA-256 is `4f5b9666e3cd4831ff85cff4bb9a641d1ffb9e8fb8003762752ec67fb0492012`.

These vectors are not an official conformance corpus. Independent complete B-1 file interoperability remains unverified.
