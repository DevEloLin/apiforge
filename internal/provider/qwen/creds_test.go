package qwen

import "testing"

func TestDeriveEndpoint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", defaultBase},
		{"portal.qwen.ai", "https://portal.qwen.ai/v1"},
		{"https://portal.qwen.ai", "https://portal.qwen.ai/v1"},
		{"https://portal.qwen.ai/v1", "https://portal.qwen.ai/v1"},
		{"https://portal.qwen.ai/v1/", "https://portal.qwen.ai/v1"},
	}
	for _, c := range cases {
		if got := deriveEndpoint(c.in); got != c.want {
			t.Errorf("deriveEndpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
