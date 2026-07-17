package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const configFileName = "apiforge.env"

// RealEnvKeys snapshots the keys already in the process environment, so a
// file-loaded value never overrides a real (shell / docker -e / systemd
// Environment=) variable. Capture this BEFORE loading any config file.
func RealEnvKeys() map[string]bool {
	keys := make(map[string]bool)
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok {
			keys[k] = true
		}
	}
	return keys
}

// ConfigFiles returns the env files to load for a config directory (nginx-style):
// the main apiforge.env (if present) followed by conf.d/*.env in sorted order.
// Only existing files are returned.
func ConfigFiles(dir string) []string {
	var out []string
	if main := filepath.Join(dir, configFileName); fileExists(main) {
		out = append(out, main)
	}
	drop, _ := filepath.Glob(filepath.Join(dir, "conf.d", "*.env"))
	sort.Strings(drop)
	out = append(out, drop...)
	return out
}

// DiscoverConfigFiles searches standard locations (like nginx/haproxy/wireguard
// use /etc/<name>/) and returns the env files from the FIRST directory that has a
// config, or nil if none. Search order:
//
//	$APIFORGE_CONFIG_DIR  →  /etc/apiforge  →  $XDG_CONFIG_HOME/apiforge
//	(~/.config/apiforge)  →  ~/.apiforge  →  ./apiforge.env or ./.apiforge.env
func DiscoverConfigFiles() []string {
	var dirs []string
	if d := os.Getenv("APIFORGE_CONFIG_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, "/etc/apiforge", filepath.Join(configHome(), "apiforge"))
	if h := home(); h != "" {
		dirs = append(dirs, filepath.Join(h, ".apiforge"))
	}
	for _, d := range dirs {
		if files := ConfigFiles(d); len(files) > 0 {
			return files
		}
	}
	// Current-directory fallback (dev convenience), including the hidden dotfile.
	for _, f := range []string{configFileName, "." + configFileName} {
		if fileExists(f) {
			return []string{f}
		}
	}
	return nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// LoadEnvFile loads KEY=VALUE lines from path into the process environment.
//
//   - blank lines and lines starting with '#' are ignored;
//   - an optional leading `export ` is accepted;
//   - inline `#comment` (preceded by whitespace) is stripped from unquoted values;
//   - single/double quotes are stripped, preserving inner spaces and '#';
//   - a key in `protected` is left untouched (real env wins); otherwise the file
//     value is set, OVERWRITING a value from an earlier file (later drop-ins win).
func LoadEnvFile(path string, protected map[string]bool) error {
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
		if protected[key] {
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
