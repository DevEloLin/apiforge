// Package httpx provides a shared HTTP client tuned for a streaming proxy:
// generous per-call timeouts via context, connection reuse, and no automatic
// body buffering (callers stream response bodies with io.Copy).
package httpx

import (
	"net"
	"net/http"
	"time"
)

// Client is the shared upstream client. Transport keeps connections warm and
// does NOT set a hard client-level Timeout (that would kill long SSE streams);
// per-request deadlines come from the request context instead.
var Client = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second, // time to first byte
	},
}
