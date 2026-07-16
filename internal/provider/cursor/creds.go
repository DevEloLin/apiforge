package cursor

import "strings"

// normalizeToken accepts either the web form `user_<ULID>::<JWT>` or the bare
// CLI JWT and returns the JWT portion.
func normalizeToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "::"); i >= 0 {
		return raw[i+2:]
	}
	return raw
}
