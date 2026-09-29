// Copyright (c) 2026, go-avkit
// SPDX-License-Identifier: BSD-3-Clause

// Package vp8 reads a VP8 bitstream in pure Go, with no libvpx or ffmpeg linkage
// and no external binaries.
//
// Almost all of a VP8 frame is arithmetic-coded, and that coding is
// go-avkit/boolcoder's -- shared with VP9. Only the first three bytes of a frame,
// and seven more on a key frame, are plain bit fields; everything after them goes
// through the coder. That makes this package the end-to-end witness for the shared
// engine: a header read correctly is a header whose every symbol came out of it in
// the right order.
package vp8

import (
	"errors"
	"fmt"

	"github.com/go-avkit/boolcoder"
)

// Errors a frame can be refused with.
var (
	// ErrShortFrame means a frame is too small to hold the header it must have.
	ErrShortFrame = errors.New("vp8: frame is too short for its header")
	// ErrStartCode means a key frame's start code is not the one VP8 states.
	ErrStartCode = errors.New("vp8: wrong start code")
	// ErrPartition means the first partition does not fit inside the frame.
	ErrPartition = errors.New("vp8: first partition runs past the end of the frame")
)

// startCode is the three bytes that follow a key frame's tag.
var startCode = [3]byte{0x9D, 0x01, 0x2A}

// FrameTag is what the plain bytes at the start of a frame state.
//
// ⛔ Key is true when the bit on the wire is ZERO. The field is a frame TYPE and
// zero means the key frame; reading it as a flag the other way round makes every
// key frame look like an inter frame, and a decoder would then look for reference
// frames that do not exist yet.
type FrameTag struct {
	Key          bool
	Version      uint8
	Show         bool
	FirstPartLen uint32
	// Width and Height are stated on a key frame only, and an inter frame takes
	// them from the key frame before it.
	Width, Height uint16
	// Scale is what the frame asks to be stretched by when shown: zero means not
	// at all. It is carried rather than applied, because applying it is a
	// decision about display and not about decoding.
	WidthScale, HeightScale uint8
	// HeaderOffset is where the arithmetic-coded part begins.
	HeaderOffset int
}

// ParseFrameTag reads the plain bytes at the start of a frame.
func ParseFrameTag(frame []byte) (FrameTag, error) {
	var t FrameTag
	if len(frame) < 3 {
		return t, fmt.Errorf("%w: %d bytes, and a tag needs three", ErrShortFrame, len(frame))
	}
	// Three bytes, least significant first: a frame type, three bits of version,
	// a shown flag, and nineteen bits of length.
	tag := uint32(frame[0]) | uint32(frame[1])<<8 | uint32(frame[2])<<16
	t.Key = tag&1 == 0
	t.Version = uint8(tag >> 1 & 0x7)
	t.Show = tag>>4&1 == 1
	t.FirstPartLen = tag >> 5
	t.HeaderOffset = 3

	if t.Key {
		if len(frame) < 10 {
			return t, fmt.Errorf("%w: %d bytes, and a key frame tag needs ten", ErrShortFrame, len(frame))
		}
		if frame[3] != startCode[0] || frame[4] != startCode[1] || frame[5] != startCode[2] {
			return t, fmt.Errorf("%w: %02x %02x %02x", ErrStartCode, frame[3], frame[4], frame[5])
		}
		// Fourteen bits of size and two of scale, each little-endian.
		w := uint32(frame[6]) | uint32(frame[7])<<8
		h := uint32(frame[8]) | uint32(frame[9])<<8
		t.Width, t.WidthScale = uint16(w&0x3FFF), uint8(w>>14)
		t.Height, t.HeightScale = uint16(h&0x3FFF), uint8(h>>14)
		t.HeaderOffset = 10
	}
	if int(t.FirstPartLen)+t.HeaderOffset > len(frame) {
		return t, fmt.Errorf("%w: %d bytes claimed from offset %d, frame holds %d",
			ErrPartition, t.FirstPartLen, t.HeaderOffset, len(frame))
	}
	return t, nil
}

// Segmentation is what a frame says about dividing macroblocks into segments that
// carry their own quantiser and filter strength.
type Segmentation struct {
	Enabled     bool
	UpdateMap   bool
	UpdateData  bool
	Absolute    bool     // the values replace the frame's rather than adjusting it
	Quantiser   [4]int32 // per segment
	FilterLevel [4]int32 // per segment
	TreeProbs   [3]uint8 // how the map itself is coded
}

// LoopFilter is what a frame says about the filter applied across block edges.
type LoopFilter struct {
	Simple    bool
	Level     uint8
	Sharpness uint8
	// Deltas adjust the level per reference frame and per prediction mode.
	DeltasEnabled bool
	RefDelta      [4]int32
	ModeDelta     [4]int32
}

// Quantiser is the frame's base index and the deltas from it.
type Quantiser struct {
	YACIndex  uint8 // 0 to 127
	YDCDelta  int32
	Y2DCDelta int32
	Y2ACDelta int32
	UVDCDelta int32
	UVACDelta int32
}

// KeyFrameHeader is what a key frame states before its token probabilities.
//
// It stops there deliberately. What follows is four by eight by three by eleven
// conditional probability updates -- over a thousand reads whose only witness would
// be that nothing after them is misread -- and none of it is needed to say what a
// frame is, how large, how strongly filtered or how coarsely quantised. Where this
// stops is stated rather than implied, because the coder cannot tell a caller that
// it stopped early: there is no end marker to land on.
type KeyFrameHeader struct {
	Tag          FrameTag
	ColourSpace  uint8
	ClampingType uint8
	Segmentation Segmentation
	LoopFilter   LoopFilter
	Partitions   uint8 // the number of DCT partitions, 1, 2, 4 or 8
	Quantiser    Quantiser
}

// ParseKeyFrameHeader reads a key frame's tag and the arithmetic-coded header
// after it.
func ParseKeyFrameHeader(frame []byte) (KeyFrameHeader, error) {
	var h KeyFrameHeader
	tag, err := ParseFrameTag(frame)
	if err != nil {
		return h, err
	}
	if !tag.Key {
		return h, fmt.Errorf("%w: this is an inter frame", ErrShortFrame)
	}
	h.Tag = tag

	d := boolcoder.NewDecoder(frame[tag.HeaderOffset : tag.HeaderOffset+int(tag.FirstPartLen)])
	h.ColourSpace = uint8(d.Literal(1))
	h.ClampingType = uint8(d.Literal(1))
	h.Segmentation = readSegmentation(d)
	h.LoopFilter = readLoopFilter(d)
	// Two bits give the log of the partition count, so one, two, four or eight.
	h.Partitions = 1 << d.Literal(2)
	h.Quantiser = readQuantiser(d)

	// ⛔ Asked once, at the end. The coder cannot stop mid-symbol, so a partition
	// too short does not fail where it runs out -- it goes on inventing symbols
	// from zeros. This is the only place the question can be answered.
	if err := d.Err(); err != nil {
		return h, err
	}
	return h, nil
}

// readSegmentation reads the segment map and data, each only if the frame says so.
func readSegmentation(d *boolcoder.Decoder) Segmentation {
	var s Segmentation
	s.Enabled = d.Literal(1) == 1
	if !s.Enabled {
		return s
	}
	s.UpdateMap = d.Literal(1) == 1
	s.UpdateData = d.Literal(1) == 1
	if s.UpdateData {
		s.Absolute = d.Literal(1) == 1
		// ⛔ The quantisers come first, all four, and only then the filter levels.
		// Interleaving them would read each value at the other's width -- seven
		// bits against six -- and every field after the block would be misaligned.
		for i := range s.Quantiser {
			if d.Literal(1) == 1 {
				s.Quantiser[i] = d.Signed(7)
			}
		}
		for i := range s.FilterLevel {
			if d.Literal(1) == 1 {
				s.FilterLevel[i] = d.Signed(6)
			}
		}
	}
	if s.UpdateMap {
		for i := range s.TreeProbs {
			s.TreeProbs[i] = 255
			if d.Literal(1) == 1 {
				s.TreeProbs[i] = uint8(d.Literal(8))
			}
		}
	}
	return s
}

// readLoopFilter reads the filter's type, strength and per-reference deltas.
func readLoopFilter(d *boolcoder.Decoder) LoopFilter {
	var f LoopFilter
	f.Simple = d.Literal(1) == 1
	f.Level = uint8(d.Literal(6))
	f.Sharpness = uint8(d.Literal(3))
	f.DeltasEnabled = d.Literal(1) == 1
	if !f.DeltasEnabled {
		return f
	}
	if d.Literal(1) == 0 { // the deltas are not being updated this frame
		return f
	}
	for i := range f.RefDelta {
		if d.Literal(1) == 1 {
			f.RefDelta[i] = d.Signed(6)
		}
	}
	for i := range f.ModeDelta {
		if d.Literal(1) == 1 {
			f.ModeDelta[i] = d.Signed(6)
		}
	}
	return f
}

// readQuantiser reads the base index and the five deltas from it.
func readQuantiser(d *boolcoder.Decoder) Quantiser {
	var q Quantiser
	q.YACIndex = uint8(d.Literal(7))
	delta := func() int32 {
		if d.Literal(1) == 1 {
			return d.Signed(4)
		}
		return 0
	}
	q.YDCDelta = delta()
	q.Y2DCDelta = delta()
	q.Y2ACDelta = delta()
	q.UVDCDelta = delta()
	q.UVACDelta = delta()
	return q
}
