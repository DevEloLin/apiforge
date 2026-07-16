package cursor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"time"
)

// Cursor request checksum. Ports the "Jyh" obfuscation cipher from the
// decompiled Cursor client: a 6-byte big-endian timestamp (ms/1e6 ≈ 16-min
// precision) is run through the cipher, base64url-encoded, then concatenated
// with two stable 64-hex machine ids. The server does not validate machine-id
// contents, so deterministic per-install values are fine.

func jyh(bytes []byte) []byte {
	e := byte(165)
	for t := 0; t < len(bytes); t++ {
		v := (bytes[t] ^ e) + byte(t%256)
		bytes[t] = v
		e = v
	}
	return bytes
}

func timestampBytes(nowMs int64) []byte {
	ts := nowMs / 1_000_000
	buf := make([]byte, 6)
	n := ts
	for i := 5; i >= 0; i-- {
		buf[i] = byte(n & 0xff)
		n /= 256
	}
	return buf
}

func stableID(salt string) string {
	host, _ := os.Hostname()
	if host == "" {
		host = "apiforge"
	}
	sum := sha256.Sum256([]byte(salt + ":" + host))
	return hex.EncodeToString(sum[:])
}

var (
	machineID    = stableID("apiforge-machine")
	macMachineID = stableID("apiforge-mac-machine")
)

// buildChecksum produces the x-cursor-checksum header value.
func buildChecksum() string {
	ts := base64.RawURLEncoding.EncodeToString(jyh(timestampBytes(time.Now().UnixMilli())))
	return ts + machineID + "/" + macMachineID
}
