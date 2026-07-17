package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnvFile loads KEY=VALUE lines from path into the process environment so a
// plain binary can be configured from a file (no systemd/docker required).
//
// Format:
//   - one KEY=VALUE per line; blank lines and lines starting with '#' are ignored;
//   - an optional leading `export ` is accepted;
//   - INLINE comments are supported: for an unquoted value, a '#' preceded by
//     whitespace starts a comment (e.g. `PORT=8899   # the port` → "8899");
//   - single/double quotes around a value are stripped and preserve inner spaces
//     and '#' (quote a value that must contain a literal '#' or trailing spaces).
//
// A real environment variable that is ALREADY set takes precedence (so a
// `docker -e` / shell env value overrides the file).
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // allow long values (e.g. CUSTOM_PROVIDERS JSON)
	ln := 0
	for sc.Scan() {
		ln++
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		s = strings.TrimPrefix(s, "export ")
		key, rawVal, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("%s:%d: not KEY=VALUE: %q", path, ln, sc.Text())
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, ln)
		}
		if _, present := os.LookupEnv(key); present {
			continue // real env wins over the file
		}
		if err := os.Setenv(key, parseValue(rawVal)); err != nil {
			return err
		}
	}
	return sc.Err()
}

// parseValue turns the raw text after '=' into the final value: it honors
// surrounding quotes (kept verbatim inside), and for unquoted values strips a
// trailing ` #comment`.
func parseValue(raw string) string {
	v := strings.TrimLeft(raw, " \t")
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if i := strings.IndexByte(v[1:], q); i >= 0 {
			return v[1 : 1+i] // content between the quotes; ignore anything after
		}
		return v[1:] // unterminated quote — best effort
	}
	// Unquoted: a whole-value comment, or an inline ` #comment`.
	if v[0] == '#' {
		return ""
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			v = v[:i]
			break
		}
	}
	return strings.TrimRight(v, " \t")
}
