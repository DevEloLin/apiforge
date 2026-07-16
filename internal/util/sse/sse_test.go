package sse

import (
	"strings"
	"testing"
)

func TestFrames_ParsesDataFrames(t *testing.T) {
	in := "data: a\n\ndata: b\ndata: c\n\n"
	var got []string
	for ev := range Frames(strings.NewReader(in)) {
		got = append(got, ev.Data)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b\nc" {
		t.Fatalf("frames = %q", got)
	}
}

func TestFrames_TrailingFrameWithoutBlankLine(t *testing.T) {
	// A final frame with no closing blank line (upstream closed) must still emit.
	var got []string
	for ev := range Frames(strings.NewReader("data: last")) {
		got = append(got, ev.Data)
	}
	if len(got) != 1 || got[0] != "last" {
		t.Fatalf("frames = %q, want [last]", got)
	}
}

func TestFrames_HugeLineNotTruncated(t *testing.T) {
	// A single data: line far larger than the old 8MB scanner cap must survive
	// intact (e.g. an inline base64 image).
	big := strings.Repeat("x", 12*1024*1024)
	var got string
	for ev := range Frames(strings.NewReader("data: " + big + "\n\n")) {
		got = ev.Data
	}
	if len(got) != len(big) {
		t.Fatalf("huge line truncated: got %d bytes, want %d", len(got), len(big))
	}
}

func TestLines_NDJSON(t *testing.T) {
	var got []string
	for l := range Lines(strings.NewReader("{\"a\":1}\n{\"b\":2}\n")) {
		got = append(got, l)
	}
	if len(got) != 2 || got[0] != `{"a":1}` || got[1] != `{"b":2}` {
		t.Fatalf("lines = %q", got)
	}
}
