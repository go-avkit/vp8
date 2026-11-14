# vp8

[![ci](https://github.com/go-avkit/vp8/actions/workflows/ci.yml/badge.svg)](https://github.com/go-avkit/vp8/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-avkit/vp8.svg)](https://pkg.go.dev/github.com/go-avkit/vp8)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

Part of [go-avkit](https://go-avkit.github.io): [documentation](https://go-avkit.github.io/docs/).

Pure-Go (CGO=0) reader for the parts of a VP8 frame that are **plain bytes and
bit fields**: the frame tag, and the key-frame header behind it.

```go
tag, err := vp8.ParseFrameTag(frame)   // key?, size, where the coded part begins
if tag.Key {
    h, err := vp8.ParseKeyFrameHeader(frame)   // segmentation, loop filter, quantiser
}
```

## What it is not

**It does not decode frames.** There is no prediction, no residual, no
reconstruction and no loop filter — nothing here turns a bitstream into pixels.
`FrameTag.HeaderOffset` says where the arithmetic-coded part begins, and
[`go-avkit/boolcoder`](https://github.com/go-avkit/boolcoder) is what reads it.

## What is in it

| | |
|---|---|
| `ParseFrameTag`, `FrameTag` | key/inter, version, `show_frame`, first-partition length, size, scale, and where the coded part starts |
| `ParseKeyFrameHeader`, `KeyFrameHeader` | `Segmentation`, `LoopFilter`, `Quantiser` |

Two things it carries deliberately rather than resolving:

- **The scale is carried, not applied.** A frame may ask to be stretched when
  shown; stretching it is a decision about display, not about decoding, so the
  factor is reported and left alone.
- **Width and height appear on a key frame only.** An inter frame takes them
  from the key frame before it, so a caller walking a stream has to remember
  them — this package reads one frame at a time and does not hold that state
  for you.

**Windows, macOS, Linux; six 64-bit architectures.** 100% statement coverage,
gated in CI.

## Licence

BSD-3-Clause.
