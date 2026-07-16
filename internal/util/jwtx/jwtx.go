// Package jwtx decodes JWT payloads without verifying signatures — we only
// read non-secret claims (expiry, account id) from tokens we already trust.
package jwtx

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// DecodePayload returns the JWT's middle segment as a claims map, or nil.
func DecodePayload(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// tolerate padded variants
		raw, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}

// ExpiryMs returns the `exp` claim in epoch milliseconds, or ok=false if the
// token has no numeric exp (treat as non-expiring / unknown).
func ExpiryMs(token string) (ms int64, ok bool) {
	claims := DecodePayload(token)
	if claims == nil {
		return 0, false
	}
	exp, has := claims["exp"]
	if !has {
		return 0, false
	}
	if f, isNum := exp.(float64); isNum {
		return int64(f) * 1000, true
	}
	return 0, false
}
