// Copyright (c) 2026, go-avkit
// SPDX-License-Identifier: BSD-3-Clause

package vp8

import (
	"os"
	"path/filepath"
	"testing"
)

// The fuzz targets below hold the frame readers to what a reader of downloaded
// files needs: no frame, however malformed, panics, and what is handed back
// stays inside the frame it came from. An error is always an acceptable answer.
// The seeds are the frames of a short libvpx encode (testdata/fuzzseed): one key
// frame and three inter frames.
//
//	go test -run='^$' -fuzz='^FuzzParseKeyFrameHeader$' -fuzztime=60s

func frameSeeds(f *testing.F) {
	f.Helper()
	paths, err := filepath.Glob("testdata/fuzzseed/*")
	if err != nil || len(paths) == 0 {
		f.Fatalf("no seed: %v", err)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			f.Fatalf("seed: %v", err)
		}
		f.Add(data)
	}
}

func FuzzParseFrameTag(f *testing.F) {
	frameSeeds(f)
	f.Fuzz(func(t *testing.T, frame []byte) {
		tag, err := ParseFrameTag(frame)
		if err != nil {
			return
		}
		if tag.HeaderOffset+int(tag.FirstPartLen) > len(frame) {
			t.Fatalf("first partition %d from %d runs past a %d-byte frame",
				tag.FirstPartLen, tag.HeaderOffset, len(frame))
		}
	})
}

func FuzzParseKeyFrameHeader(f *testing.F) {
	frameSeeds(f)
	f.Fuzz(func(t *testing.T, frame []byte) {
		h, err := ParseKeyFrameHeader(frame)
		if err != nil {
			return
		}
		if !h.Tag.Key {
			t.Fatal("a key frame header was read from an inter frame")
		}
		switch h.Partitions {
		case 1, 2, 4, 8:
		default:
			t.Fatalf("%d partitions", h.Partitions)
		}
	})
}
