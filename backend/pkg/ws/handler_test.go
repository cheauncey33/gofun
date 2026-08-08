package ws

import (
	"net/http/httptest"
	"testing"
)

func TestOriginAllowed(t *testing.T) {
	allowed := map[string]struct{}{"http://127.0.0.1:5173": {}}

	sameOrigin := httptest.NewRequest("GET", "http://api.example.test/ws", nil)
	sameOrigin.Header.Set("Origin", "http://api.example.test")
	if !originAllowed(sameOrigin, allowed) {
		t.Fatal("same origin should be accepted")
	}

	devOrigin := httptest.NewRequest("GET", "http://127.0.0.1:18080/ws", nil)
	devOrigin.Header.Set("Origin", "http://127.0.0.1:5173")
	if !originAllowed(devOrigin, allowed) {
		t.Fatal("configured development origin should be accepted")
	}

	untrusted := httptest.NewRequest("GET", "http://api.example.test/ws", nil)
	untrusted.Header.Set("Origin", "https://evil.example")
	if originAllowed(untrusted, allowed) {
		t.Fatal("untrusted origin should be rejected")
	}
}
