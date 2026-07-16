// Package ssrf guards operator-supplied upstream base URLs against pointing at
// private/loopback/link-local addresses (SSRF) — a bad *_BASE_URL override
// must not let a client reach internal services.
package ssrf

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// AssertPublicURL returns an error if raw is not an https?://public-host URL.
// label is used in the error message for diagnostics.
func AssertPublicURL(raw, label string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: invalid URL %q: %w", label, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s: URL scheme must be http/https, got %q", label, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%s: URL has no host", label)
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("%s: refusing loopback host %q", label, host)
	}
	// If it's a literal IP, block private/loopback/link-local ranges. Hostnames
	// resolve at request time; we only hard-block obvious private literals here.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("%s: refusing non-public IP %q", label, host)
		}
	}
	return nil
}
