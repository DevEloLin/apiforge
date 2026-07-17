package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandCredDirs(t *testing.T) {
	dir := t.TempDir()
	// A creds dir with two account files (+ a non-json that must be ignored).
	os.WriteFile(filepath.Join(dir, "b.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(dir, "a.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("x"), 0o600)
	file := filepath.Join(dir, "b.json")

	got := expandCredDirs([]string{dir, file, "/no/such/path.json"})

	// dir → sorted a.json,b.json ; explicit file kept ; missing kept ; deduped.
	want := []string{
		filepath.Join(dir, "a.json"),
		filepath.Join(dir, "b.json"),
		"/no/such/path.json",
	}
	if len(got) != len(want) {
		t.Fatalf("expandCredDirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expandCredDirs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoad_DirectoryMultiAccount(t *testing.T) {
	dir := t.TempDir()
	creds := filepath.Join(dir, "codex")
	os.MkdirAll(creds, 0o700)
	os.WriteFile(filepath.Join(creds, "acct1.json"), []byte(`{"tokens":{"access_token":"t1"}}`), 0o600)
	os.WriteFile(filepath.Join(creds, "acct2.json"), []byte(`{"tokens":{"access_token":"t2"}}`), 0o600)

	t.Setenv("CODEX_AUTHS", creds) // point at the DIRECTORY
	cfg := Load()

	got := cfg.Providers["codex"].CredentialPaths
	if len(got) != 2 {
		t.Fatalf("codex credential paths = %v, want 2 (one per file in the dir)", got)
	}
}
