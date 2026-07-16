package cursor

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
)

// Minimal hand-rolled protobuf + Connect-RPC framing for Cursor's AiService.
// Only the fields we need are encoded/decoded (field numbers recovered from the
// decompiled client), avoiding a protoc/buf build step.

// maxFrameBytes bounds a single Connect frame payload so a corrupt/desynced
// 32-bit length prefix can't trigger a multi-GB allocation.
const maxFrameBytes = 64 << 20

// ---- wire-format writers ---------------------------------------------------

func varint(n uint64) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n > 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

func tag(field, wire int) []byte { return varint(uint64(field)<<3 | uint64(wire)) }

func strField(field int, value string) []byte {
	b := []byte(value)
	return concat(tag(field, 2), varint(uint64(len(b))), b)
}

func msgField(field int, body []byte) []byte {
	return concat(tag(field, 2), varint(uint64(len(body))), body)
}

func varField(field int, value uint64) []byte {
	return concat(tag(field, 0), varint(value))
}

func concat(chunks ...[]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

// CursorMessage is one conversation turn.
type CursorMessage struct {
	Text     string
	Role     string // "user" | "assistant"
	BubbleID string
}

// encodeChatRequest builds the StreamUnifiedChatRequestWithTools payload.
func encodeChatRequest(messages []CursorMessage, model, conversationID string) []byte {
	var inner []byte
	for _, m := range messages {
		roleVal := uint64(2)
		if m.Role == "user" {
			roleVal = 1
		}
		turn := concat(
			strField(1, m.Text),
			varField(2, roleVal),
			strField(13, m.BubbleID),
		)
		inner = append(inner, msgField(1, turn)...)
	}
	inner = append(inner, msgField(5, strField(1, model))...) // model_details
	inner = append(inner, varField(22, 1)...)                 // is_chat = true
	inner = append(inner, strField(23, conversationID)...)
	inner = append(inner, varField(46, 1)...) // unified_mode = CHAT
	// Top-level field 1 = stream_unified_chat_request
	return msgField(1, inner)
}

// ---- Connect framing -------------------------------------------------------

// frame wraps a payload in a Connect data frame: [flag][len32-BE][payload].
func frame(payload []byte, flag byte) []byte {
	header := make([]byte, 5)
	header[0] = flag
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	return append(header, payload...)
}

// ---- wire-format reader ----------------------------------------------------

type protoField struct {
	field int
	wire  int
	bytes []byte
	value uint64
}

func readVarintAt(buf []byte, pos int) (uint64, int) {
	var result uint64
	var shift uint
	p := pos
	for p < len(buf) {
		b := buf[p]
		result |= uint64(b&0x7f) << shift
		p++
		if b&0x80 == 0 {
			break
		}
		shift += 7
	}
	return result, p
}

func readMessage(buf []byte) []protoField {
	var fields []protoField
	pos := 0
	for pos < len(buf) {
		key, p1 := readVarintAt(buf, pos)
		pos = p1
		field := int(key >> 3)
		wire := int(key & 0x7)
		switch wire {
		case 0:
			val, p2 := readVarintAt(buf, pos)
			pos = p2
			fields = append(fields, protoField{field: field, wire: wire, value: val})
		case 2:
			l, p2 := readVarintAt(buf, pos)
			// Single uint64 compare guards both overflow (bit-63 set → negative int)
			// and out-of-range, so buf[p2:end] can never panic on a malformed length.
			if l > uint64(len(buf)-p2) {
				return fields
			}
			end := p2 + int(l)
			fields = append(fields, protoField{field: field, wire: wire, bytes: buf[p2:end]})
			pos = end
		case 5:
			pos += 4
		case 1:
			pos += 8
		default:
			return fields
		}
	}
	return fields
}

// extractResponseText pulls the streamed text delta(s) from a
// StreamUnifiedChatResponseWithTools payload (field 2 -> inner field 1). A single
// Connect frame may batch multiple sub-messages, so ALL text deltas are
// concatenated (returning only the first would truncate output).
func extractResponseText(payload []byte) string {
	var out []byte
	for _, f := range readMessage(payload) {
		if f.field == 2 && f.bytes != nil {
			for _, inner := range readMessage(f.bytes) {
				if inner.field == 1 && inner.bytes != nil {
					out = append(out, inner.bytes...)
				}
			}
		}
	}
	return string(out)
}

// streamDeltas incrementally parses Connect frames from r, invoking onText for
// each decoded text delta. Handles gzip (flag&0x01) and skips JSON trailer/error
// frames (flag&0x02) which mark end-of-stream.
func streamDeltas(r io.Reader, onText func(string)) {
	br := bufio.NewReaderSize(r, 64*1024)
	header := make([]byte, 5)
	for {
		if _, err := io.ReadFull(br, header); err != nil {
			return
		}
		flag := header[0]
		length := binary.BigEndian.Uint32(header[1:])
		if length > maxFrameBytes {
			return // corrupt / desynced frame — abort rather than allocate gigabytes
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(br, payload); err != nil {
			return
		}
		if flag&0x02 != 0 {
			continue // trailer / error frame => stream end marker
		}
		if flag&0x01 != 0 {
			if dec, err := gunzip(payload); err == nil {
				payload = dec
			} else {
				continue
			}
		}
		if text := extractResponseText(payload); text != "" {
			onText(text)
		}
	}
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
