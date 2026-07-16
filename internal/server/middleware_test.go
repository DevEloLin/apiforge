package server

import "testing"

func TestAnyConstantTimeEqual(t *testing.T) {
	keys := []string{"sk-alpha", "sk-bravo"}
	if !anyConstantTimeEqual(keys, "sk-bravo") {
		t.Fatal("valid key rejected")
	}
	if anyConstantTimeEqual(keys, "sk-wrong") {
		t.Fatal("invalid key accepted")
	}
	if anyConstantTimeEqual(keys, "") {
		t.Fatal("empty key accepted")
	}
	if anyConstantTimeEqual(nil, "anything") {
		t.Fatal("empty key set must not match")
	}
}
