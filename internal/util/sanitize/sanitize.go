// Package sanitize redacts secrets and absolute paths before they reach logs
// or the /health diagnostics surface.
package sanitize

import "regexp"

var (
	unixPath = regexp.MustCompile(`(?:/[^\s/]+)+/?`)
	winPath  = regexp.MustCompile(`[A-Za-z]:\\[^\s]+`)
	secret   = regexp.MustCompile(`(?i)("?(?:access_token|refresh_token|id_token|api_key|token|authorization)"?\s*[:=]\s*")[^"]+(")`)
)

// Path replaces absolute filesystem paths with "<path>".
func Path(s string) string {
	if s == "" {
		return s
	}
	return winPath.ReplaceAllString(unixPath.ReplaceAllString(s, "<path>"), "<path>")
}

// Secrets masks the value of common credential fields in a string.
func Secrets(s string) string {
	return secret.ReplaceAllString(s, `$1***$2`)
}
