package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "apiforge.env")
	content := `# comment
export API_KEYS=sk-file        # inline comment must be stripped
PORT="8913"
QUEUE_WAIT_MS = 5000
EMPTY=
HASHVAL="sk-a#b c"             # quoted value keeps '#' and spaces
BAREURL=https://x.example.com/v1
RALREADY=fromfile
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// A real env var must win over the file.
	t.Setenv("RALREADY", "fromenv")
	t.Setenv("API_KEYS", "")
	os.Unsetenv("API_KEYS")
	t.Setenv("PORT", "")
	os.Unsetenv("PORT")
	t.Setenv("QUEUE_WAIT_MS", "")
	os.Unsetenv("QUEUE_WAIT_MS")

	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if got := os.Getenv("API_KEYS"); got != "sk-file" {
		t.Errorf("API_KEYS = %q, want sk-file (export + inline comment stripped)", got)
	}
	if got := os.Getenv("PORT"); got != "8913" {
		t.Errorf("PORT = %q, want 8913 (quotes stripped)", got)
	}
	if got := os.Getenv("QUEUE_WAIT_MS"); got != "5000" {
		t.Errorf("QUEUE_WAIT_MS = %q, want 5000 (spaces around = trimmed)", got)
	}
	if got := os.Getenv("HASHVAL"); got != "sk-a#b c" {
		t.Errorf("HASHVAL = %q, want 'sk-a#b c' (quoted keeps # and space)", got)
	}
	if got := os.Getenv("BAREURL"); got != "https://x.example.com/v1" {
		t.Errorf("BAREURL = %q, want the full URL ('#'-less, no false comment strip)", got)
	}
	if got := os.Getenv("RALREADY"); got != "fromenv" {
		t.Errorf("RALREADY = %q, want fromenv (real env must override file)", got)
	}
}

func TestParseValue(t *testing.T) {
	cases := map[string]string{
		"sk-x":                  "sk-x",
		"sk-x   # trailing":     "sk-x",
		`"quoted # keep"`:       "quoted # keep",
		`'single q'`:            "single q",
		"  spaced  # c":         "spaced",
		"# whole comment":       "",
		"":                      "",
		"https://a.com/v1#frag": "https://a.com/v1#frag", // '#' without preceding space is kept
		`"  inner spaces  "`:    "  inner spaces  ",
	}
	for in, want := range cases {
		if got := parseValue(in); got != want {
			t.Errorf("parseValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadEnvFile_Missing(t *testing.T) {
	if err := LoadEnvFile(filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
