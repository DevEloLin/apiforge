// Package idgen produces the ids the OpenAI/Anthropic wire formats expect
// (chatcmpl-…, resp_…, call_…) and RFC-4122 v4 session UUIDs.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// OpenAI returns an id like "<prefix>-<24 hex chars>".
func OpenAI(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}

// NowSeconds is the current Unix time in seconds (the `created` field).
func NowSeconds() int64 { return time.Now().Unix() }

// UUID returns a random RFC-4122 version-4 UUID (Codex `session_id`).
func UUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
