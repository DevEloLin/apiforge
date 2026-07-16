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
	// Literal IP: block private/loopback/link-local ranges outright.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("%s: refusing non-public IP %q", label, host)
		}
		return nil
	}
	// Hostname: resolve and reject if ANY resolved address is non-public — closes
	// the domain-bypass (e.g. a name pointing at 127.0.0.1 / 169.254.169.254 /
	// an internal host). DNS failure is not hard-blocked (best-effort; a torn
	// DNS at startup shouldn't disable a legitimately public upstream).
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("%s: host %q resolves to non-public IP %s", label, host, ip)
		}
	}
	return nil
}

// isBlockedIP reports whether ip is loopback/private/link-local/unspecified.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
