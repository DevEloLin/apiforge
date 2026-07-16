package ssrf

import "testing"

func TestAssertPublicURL(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/x", "https://10.0.0.5", "http://192.168.1.1",
		"https://169.254.169.254/latest/meta-data", "http://localhost:8899",
		"https://foo.localhost", "http://[::1]", "https://0.0.0.0",
		"ftp://example.com", "https://172.16.0.1",
	}
	for _, u := range blocked {
		if err := AssertPublicURL(u, "t"); err == nil {
			t.Errorf("AssertPublicURL(%q) = nil, want blocked", u)
		}
	}
	allowed := []string{"https://api.deepseek.com", "https://api.openai.com/v1"}
	for _, u := range allowed {
		if err := AssertPublicURL(u, "t"); err != nil {
			t.Errorf("AssertPublicURL(%q) = %v, want allowed", u, err)
		}
	}
}
