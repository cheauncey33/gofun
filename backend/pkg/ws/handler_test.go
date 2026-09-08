package ws

import (
	"net/http/httptest"
	"testing"
)

func TestParseWSAuthMessage(t *testing.T) {
	token, err := parseWSAuthMessage([]byte(`{"type":"auth","token":"abc.def"}`))
	if err != nil || token != "abc.def" {
		t.Fatalf("got token %q err %v", token, err)
	}
	if _, err := parseWSAuthMessage([]byte(`{"type":"ping"}`)); err == nil {
		t.Fatal("non-auth message should fail")
	}
	if _, err := parseWSAuthMessage([]byte(`{"type":"auth","token":"  "}`)); err == nil {
		t.Fatal("empty token should fail")
	}
}

func TestAccessTokenFromHeader(t *testing.T) {
	if got := accessTokenFromHeader("Bearer abc"); got != "abc" {
		t.Fatalf("bearer: got %q", got)
	}
	if got := accessTokenFromHeader("raw-jwt"); got != "raw-jwt" {
		t.Fatalf("raw: got %q", got)
	}
	if got := accessTokenFromHeader(""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

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
