// Copyright (c) 2026, go-avkit
// SPDX-License-Identifier: BSD-3-Clause

package vp8

import (
	"errors"
	"math/rand/v2"
	"testing"
)

// tag builds the three plain bytes at the start of a frame.
func tag(key bool, version uint8, show bool, firstPart uint32) []byte {
	var v uint32
	if !key {
		v |= 1 // ⛔ the bit is set for an INTER frame: zero means the key frame
	}
	v |= uint32(version&0x7) << 1
	if show {
		v |= 1 << 4
	}
	v |= firstPart << 5
	return []byte{byte(v), byte(v >> 8), byte(v >> 16)}
}

// keyTag builds a whole key frame tag: the three bytes, the start code, and the
// size and scale of each axis.
func keyTag(w, h uint16, ws, hs uint8, firstPart uint32, payload int) []byte {
	out := tag(true, 0, true, firstPart)
	out = append(out, startCode[0], startCode[1], startCode[2])
	wv := uint32(w) | uint32(ws)<<14
	hv := uint32(h) | uint32(hs)<<14
	out = append(out, byte(wv), byte(wv>>8), byte(hv), byte(hv>>8))
	return append(out, make([]byte, payload)...)
}

// TestAKeyFrameIsTheZeroOfItsTypeField.
//
// ⛔ The bit is a frame TYPE and zero means the key frame. Reading it as a flag the
// other way round makes every key frame look like an inter frame, and a decoder
// would then look for reference frames that do not exist yet.
func TestAKeyFrameIsTheZeroOfItsTypeField(t *testing.T) {
	k, err := ParseFrameTag(keyTag(320, 240, 0, 0, 0, 0))
	if err != nil {
		t.Fatalf("ParseFrameTag: %v", err)
	}
	if !k.Key {
		t.Error("a zero type field was read as an inter frame")
	}
	inter, err := ParseFrameTag(tag(false, 0, true, 0))
	if err != nil {
		t.Fatalf("inter: %v", err)
	}
	if inter.Key {
		t.Error("a set type field was read as a key frame")
	}
	// An inter frame states no size and stops after three bytes.
	if inter.Width != 0 || inter.HeaderOffset != 3 {
		t.Errorf("%+v", inter)
	}
}

func TestTheTagStatesVersionShowLengthSizeAndScale(t *testing.T) {
	frame := keyTag(1920, 1080, 1, 2, 7, 7)
	got, err := ParseFrameTag(frame)
	if err != nil {
		t.Fatalf("ParseFrameTag: %v", err)
	}
	if got.Width != 1920 || got.Height != 1080 {
		t.Errorf("%dx%d, want 1920x1080", got.Width, got.Height)
	}
	// ⛔ The scale shares each sixteen-bit field with the size: two bits above
	// fourteen. A reader taking all sixteen as the size would report 17312 for a
	// frame 1920 wide the moment a stream asks to be stretched.
	if got.WidthScale != 1 || got.HeightScale != 2 {
		t.Errorf("scales %d,%d, want 1,2", got.WidthScale, got.HeightScale)
	}
	if got.FirstPartLen != 7 || got.HeaderOffset != 10 {
		t.Errorf("first partition %d at offset %d", got.FirstPartLen, got.HeaderOffset)
	}
	if !got.Show {
		t.Error("the shown flag was not read")
	}

	hidden, err := ParseFrameTag(tag(false, 3, false, 0))
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Show || hidden.Version != 3 {
		t.Errorf("%+v, want version 3 and not shown", hidden)
	}
}

func TestFrameTagRefusals(t *testing.T) {
	t.Run("too short for a tag", func(t *testing.T) {
		if _, err := ParseFrameTag([]byte{0, 0}); !errors.Is(err, ErrShortFrame) {
			t.Errorf("err = %v, want ErrShortFrame", err)
		}
	})
	t.Run("too short for a key frame tag", func(t *testing.T) {
		if _, err := ParseFrameTag(tag(true, 0, true, 0)); !errors.Is(err, ErrShortFrame) {
			t.Errorf("err = %v, want ErrShortFrame", err)
		}
	})
	t.Run("the wrong start code", func(t *testing.T) {
		frame := keyTag(320, 240, 0, 0, 0, 0)
		frame[4] ^= 0xFF
		if _, err := ParseFrameTag(frame); !errors.Is(err, ErrStartCode) {
			t.Errorf("err = %v, want ErrStartCode", err)
		}
	})
	// ⛔ A first partition that runs past the end is refused rather than clamped:
	// the arithmetic decoder cannot stop mid-symbol, so handed a short partition it
	// would read zeros and produce a header nobody wrote.
	t.Run("a partition past the end", func(t *testing.T) {
		frame := keyTag(320, 240, 0, 0, 5000, 4)
		if _, err := ParseFrameTag(frame); !errors.Is(err, ErrPartition) {
			t.Errorf("err = %v, want ErrPartition", err)
		}
	})
	t.Run("an inter frame handed to the key frame reader", func(t *testing.T) {
		if _, err := ParseKeyFrameHeader(tag(false, 0, true, 0)); err == nil {
			t.Error("an inter frame was read as a key frame")
		}
	})
}

// TestTheVectorReadsAsItWasEncoded is the interoperating witness: a frame another
// implementation wrote, read field by field.
//
// ⛔ Every value here was measured before it was written down. The dimensions are
// what the container states; the quantiser index is what the encoder was pinned to,
// on the scale it takes; the rest is what the stream holds.
func TestTheVectorReadsAsItWasEncoded(t *testing.T) {
	h, err := ParseKeyFrameHeader(keyFrameVector)
	if err != nil {
		t.Fatalf("ParseKeyFrameHeader: %v", err)
	}
	if h.Tag.Width != 176 || h.Tag.Height != 144 {
		t.Errorf("%dx%d, want 176x144", h.Tag.Width, h.Tag.Height)
	}
	if !h.Tag.Key || !h.Tag.Show {
		t.Errorf("%+v", h.Tag)
	}
	// The first two arithmetic symbols of a key frame. Both are zero in every
	// stream this encoder writes, and they are the first thing a wrong initial
	// state would spoil.
	if h.ColourSpace != 0 || h.ClampingType != 0 {
		t.Errorf("colour space %d clamping %d, want 0 and 0", h.ColourSpace, h.ClampingType)
	}
	if h.Quantiser.YACIndex != 25 {
		t.Errorf("quantiser index %d, want 25", h.Quantiser.YACIndex)
	}
	if h.LoopFilter.Level != 3 || h.LoopFilter.Sharpness != 0 || h.LoopFilter.Simple {
		t.Errorf("filter %+v", h.LoopFilter)
	}
	if !h.LoopFilter.DeltasEnabled {
		t.Error("the filter deltas flag was not read")
	}
	if h.Partitions != 1 {
		t.Errorf("%d partitions, want 1", h.Partitions)
	}
	if h.Segmentation.Enabled {
		t.Error("segmentation reported enabled, and this stream does not use it")
	}
}

// TestEveryTruncationOfTheVectorIsRefusedOrSaysSo.
//
// ⛔ The arithmetic decoder cannot stop mid-symbol: past the end it reads zeros. So
// a truncated frame does not fail where it runs out -- it goes on inventing symbols,
// and the only honest answer is the one asked at the end. Every prefix must
// therefore either be refused outright or come back with an error, and none may
// return a header as though nothing were wrong.
func TestEveryTruncationOfTheVectorIsRefusedOrSaysSo(t *testing.T) {
	for n := 0; n < len(keyFrameVector); n++ {
		if _, err := ParseKeyFrameHeader(keyFrameVector[:n]); err == nil {
			t.Fatalf("%d of %d bytes read cleanly", n, len(keyFrameVector))
		}
	}
	// The premise: the whole of it does read, so the refusals above are about the
	// truncation and not about the vector.
	if _, err := ParseKeyFrameHeader(keyFrameVector); err != nil {
		t.Fatalf("the vector itself does not read: %v", err)
	}
}

// TestASegmentedStreamReadsItsSegmentBlock.
//
// ⛔ The segment block is read only when the frame says so, and the quantisers come
// first -- all four -- then the filter levels. Interleaving them would read each
// value at the other's width, seven bits against six, and every field after the
// block would be misaligned. The witness is the quantiser index and the filter
// level after it, since those are the first fields a misalignment reaches.
func TestASegmentedStreamReadsItsSegmentBlock(t *testing.T) {
	h, err := ParseKeyFrameHeader(segmentedVector)
	if err != nil {
		t.Fatalf("ParseKeyFrameHeader: %v", err)
	}
	if !h.Segmentation.Enabled {
		t.Fatal("this vector was chosen because its stream segments, and it reports otherwise")
	}
	if h.Tag.Width != 176 || h.Tag.Height != 144 {
		t.Errorf("%dx%d, want 176x144", h.Tag.Width, h.Tag.Height)
	}
	if h.Quantiser.YACIndex > 127 {
		t.Errorf("quantiser index %d is outside the range the format states", h.Quantiser.YACIndex)
	}
	if h.LoopFilter.Level > 63 || h.LoopFilter.Sharpness > 7 {
		t.Errorf("filter %+v is outside the ranges the format states", h.LoopFilter)
	}
	// Whatever the block said, the values it can carry are bounded.
	for i, q := range h.Segmentation.Quantiser {
		if q < -127 || q > 127 {
			t.Errorf("segment %d quantiser %d is outside seven bits and a sign", i, q)
		}
	}
	for i, l := range h.Segmentation.FilterLevel {
		if l < -63 || l > 63 {
			t.Errorf("segment %d filter level %d is outside six bits and a sign", i, l)
		}
	}
}

// TestTheReadersStayInsideTheRangesTheFormatStates.
//
// ⛔ These are branches the two vectors do not take: a segment map with its tree
// probabilities, a filter delta update, a quantiser delta. No encoder setting to
// hand produces them -- several were measured -- so rather than assert values that
// nothing witnessed, this asserts what must hold whatever is read.
//
// Any sequence of bytes IS a valid arithmetic-coded stream; it simply decodes to
// arbitrary symbols. So feeding varied bytes reaches the branches, and what is
// checked is the property the format guarantees: every field lands inside the width
// it is stated in. A reader that mixed two field widths up, or read a signed value
// as unsigned, breaks that.
func TestTheReadersStayInsideTheRangesTheFormatStates(t *testing.T) {
	// Fixed patterns first, for the extremes of every flag, then pseudo-random
	// runs with a fixed seed: regular bytes cannot reach every combination of
	// flags, and the combinations are what the branches are.
	seeds := [][]byte{
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		{0x00, 0xFF, 0x00, 0xFF, 0x00, 0xFF, 0x00, 0xFF},
		{0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55},
		{0x80, 0x01, 0x7F, 0xFE, 0x3C, 0xC3, 0x0F, 0xF0},
	}
	r := rand.New(rand.NewPCG(7, 11))
	for i := 0; i < 24; i++ {
		seed := make([]byte, 8)
		for j := range seed {
			seed[j] = byte(r.UintN(256))
		}
		seeds = append(seeds, seed)
	}
	for i, seed := range seeds {
		data := make([]byte, 0, 4096)
		for len(data) < 4096 {
			data = append(data, seed...)
		}
		frame := keyTag(176, 144, 0, 0, uint32(len(data)), len(data))
		copy(frame[10:], data)

		h, err := ParseKeyFrameHeader(frame)
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		if h.LoopFilter.Level > 63 {
			t.Errorf("seed %d: filter level %d needs more than six bits", i, h.LoopFilter.Level)
		}
		if h.LoopFilter.Sharpness > 7 {
			t.Errorf("seed %d: sharpness %d needs more than three bits", i, h.LoopFilter.Sharpness)
		}
		if h.Quantiser.YACIndex > 127 {
			t.Errorf("seed %d: quantiser index %d needs more than seven bits", i, h.Quantiser.YACIndex)
		}
		for _, d := range []int32{h.Quantiser.YDCDelta, h.Quantiser.Y2DCDelta,
			h.Quantiser.Y2ACDelta, h.Quantiser.UVDCDelta, h.Quantiser.UVACDelta} {
			if d < -15 || d > 15 {
				t.Errorf("seed %d: a quantiser delta of %d needs more than four bits and a sign", i, d)
			}
		}
		for _, d := range h.LoopFilter.RefDelta {
			if d < -63 || d > 63 {
				t.Errorf("seed %d: a reference delta of %d needs more than six bits and a sign", i, d)
			}
		}
		for _, d := range h.LoopFilter.ModeDelta {
			if d < -63 || d > 63 {
				t.Errorf("seed %d: a mode delta of %d needs more than six bits and a sign", i, d)
			}
		}
		if h.Partitions != 1 && h.Partitions != 2 && h.Partitions != 4 && h.Partitions != 8 {
			t.Errorf("seed %d: %d partitions, and the field states a power of two up to eight",
				i, h.Partitions)
		}
		for _, q := range h.Segmentation.Quantiser {
			if q < -127 || q > 127 {
				t.Errorf("seed %d: a segment quantiser of %d needs more than seven bits and a sign", i, q)
			}
		}
		for _, l := range h.Segmentation.FilterLevel {
			if l < -63 || l > 63 {
				t.Errorf("seed %d: a segment filter level of %d needs more than six bits and a sign", i, l)
			}
		}
	}
}

// TestAPartitionTooSmallForTheHeaderIsReportedAtTheEnd.
//
// ⛔ Not the same case as a truncated frame, which the tag refuses first: here the
// tag is honest and the partition it declares fits inside the frame -- it is simply
// too small to hold the header. The arithmetic decoder cannot stop mid-symbol, so it
// reads zeros past the end and goes on producing plausible symbols; the only place
// that can be answered is once, at the end.
//
// Truncating a frame never reaches this, because the partition check trips first.
// That is why it needs a case of its own.
func TestAPartitionTooSmallForTheHeaderIsReportedAtTheEnd(t *testing.T) {
	for _, n := range []uint32{1, 2, 3} {
		frame := keyTag(176, 144, 0, 0, n, int(n))
		// The premise: the tag itself is accepted, so what follows is about the
		// partition and not about the frame.
		if _, err := ParseFrameTag(frame); err != nil {
			t.Fatalf("%d-byte partition: the tag was refused, so this tests nothing: %v", n, err)
		}
		if _, err := ParseKeyFrameHeader(frame); err == nil {
			t.Errorf("%d-byte partition: a header was read out of it", n)
		}
	}
}
