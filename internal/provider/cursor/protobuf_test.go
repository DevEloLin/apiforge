package cursor

import (
	"bytes"
	"strings"
	"testing"
)

// buildResponsePayload mirrors the server's StreamUnifiedChatResponseWithTools:
// top-level field 2 (message) whose inner field 1 (string) is the text delta.
func buildResponsePayload(text string) []byte {
	inner := strField(1, text)
	return msgField(2, inner)
}

func TestEncodeChatRequest_RoundTrip(t *testing.T) {
	// Arrange
	msgs := []CursorMessage{
		{Text: "hello", Role: "user", BubbleID: "b1"},
		{Text: "hi", Role: "assistant", BubbleID: "b2"},
	}

	// Act
	payload := encodeChatRequest(msgs, "gpt-5.1", "conv-1")

	// Assert — top-level field 1 wraps the inner request
	top := readMessage(payload)
	if len(top) != 1 || top[0].field != 1 || top[0].bytes == nil {
		t.Fatalf("top-level = %+v", top)
	}
	inner := readMessage(top[0].bytes)

	var turns int
	var sawModel, sawConv, sawIsChat, sawMode bool
	for _, f := range inner {
		switch f.field {
		case 1: // conversation turn
			turns++
			turn := readMessage(f.bytes)
			// first turn should carry text + role + bubbleId
			if turns == 1 {
				var text, bubble string
				var role uint64
				for _, tf := range turn {
					switch tf.field {
					case 1:
						text = string(tf.bytes)
					case 2:
						role = tf.value
					case 13:
						bubble = string(tf.bytes)
					}
				}
				if text != "hello" || role != 1 || bubble != "b1" {
					t.Fatalf("turn1 text=%q role=%d bubble=%q", text, role, bubble)
				}
			}
		case 5:
			sawModel = string(readMessage(f.bytes)[0].bytes) == "gpt-5.1"
		case 22:
			sawIsChat = f.value == 1
		case 23:
			sawConv = string(f.bytes) == "conv-1"
		case 46:
			sawMode = f.value == 1
		}
	}
	if turns != 2 {
		t.Fatalf("turns = %d, want 2", turns)
	}
	if !sawModel || !sawConv || !sawIsChat || !sawMode {
		t.Fatalf("missing fields: model=%v conv=%v isChat=%v mode=%v", sawModel, sawConv, sawIsChat, sawMode)
	}
}

func TestExtractResponseText(t *testing.T) {
	got := extractResponseText(buildResponsePayload("delta-text"))
	if got != "delta-text" {
		t.Fatalf("extractResponseText = %q, want delta-text", got)
	}
}

func TestExtractResponseText_ConcatenatesBatchedDeltas(t *testing.T) {
	// A frame batching two field-2 sub-messages must yield both texts, not just the first.
	payload := append(msgField(2, strField(1, "Hello ")), msgField(2, strField(1, "world"))...)
	if got := extractResponseText(payload); got != "Hello world" {
		t.Fatalf("extractResponseText = %q, want 'Hello world'", got)
	}
}

func TestStreamDeltas_MultiFrameAndTrailer(t *testing.T) {
	// Two data frames + one trailer frame (flag 0x02) that must be skipped.
	var buf bytes.Buffer
	buf.Write(frame(buildResponsePayload("Hello "), 0x00))
	buf.Write(frame(buildResponsePayload("world"), 0x00))
	buf.Write(frame([]byte(`{"error":null}`), 0x02)) // trailer => end marker, ignored

	var got strings.Builder
	streamDeltas(&buf, func(s string) { got.WriteString(s) })

	if got.String() != "Hello world" {
		t.Fatalf("streamDeltas = %q, want 'Hello world'", got.String())
	}
}

func TestReadMessage_MalformedLengthNoPanic(t *testing.T) {
	// field 1, wire 2 (length-delimited), then a varint length with bit 63 set
	// (0xFF*9 → huge uint64). Must not panic and must stop cleanly.
	buf := []byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("readMessage panicked on malformed length: %v", r)
		}
	}()
	_ = readMessage(buf) // just must not panic
}

func TestStreamDeltas_OversizedFrameAborts(t *testing.T) {
	// A frame claiming ~4GB must abort without allocating.
	frame := []byte{0x00, 0x7f, 0xff, 0xff, 0xff} // flag + length 0x7FFFFFFF
	got := ""
	streamDeltas(bytes.NewReader(frame), func(s string) { got += s })
	if got != "" {
		t.Fatalf("expected no output from oversized frame, got %q", got)
	}
}

func TestNormalizeToken(t *testing.T) {
	if got := normalizeToken("user_01ABC::jwt.body.sig"); got != "jwt.body.sig" {
		t.Fatalf("web form = %q", got)
	}
	if got := normalizeToken("  bare.jwt.token  "); got != "bare.jwt.token" {
		t.Fatalf("bare form = %q", got)
	}
}

func TestBuildChecksum_Shape(t *testing.T) {
	cs := buildChecksum()
	parts := strings.Split(cs, "/")
	if len(parts) != 2 || len(parts[1]) != 64 {
		t.Fatalf("checksum shape = %q", cs)
	}
	// first part = base64url(ts) + 64-hex machine id
	if len(parts[0]) < 64 {
		t.Fatalf("checksum prefix too short: %q", parts[0])
	}
}
