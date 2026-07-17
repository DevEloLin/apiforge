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
// Format: one KEY=VALUE per line; blank lines and lines starting with '#' are
// ignored; an optional leading `export ` is accepted; surrounding single or
// double quotes on the value are stripped. A real environment variable that is
// ALREADY set takes precedence (so `docker -e` / shell env overrides the file).
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		s = strings.TrimPrefix(s, "export ")
		key, val, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("%s:%d: not KEY=VALUE: %q", path, line, sc.Text())
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, line)
		}
		val = strings.TrimSpace(val)
		val = trimQuotes(val)
		if _, present := os.LookupEnv(key); present {
			continue // real env wins over the file
		}
		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}
	return sc.Err()
}

func trimQuotes(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
