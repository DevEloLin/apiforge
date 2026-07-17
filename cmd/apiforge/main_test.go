package main

import "testing"

func TestParseMemLimit(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"64MiB", 64 << 20},
		{"1GiB", 1 << 30},
		{"512KiB", 512 << 10},
		{"1024B", 1024},
		{"4096", 4096},
		{"1.5GiB", int64(1.5 * (1 << 30))},
	}
	for _, c := range cases {
		got, err := parseMemLimit(c.in)
		if err != nil {
			t.Errorf("parseMemLimit(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseMemLimit(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	if _, err := parseMemLimit("garbage"); err == nil {
		t.Error("parseMemLimit(garbage) = nil error, want error")
	}
}
