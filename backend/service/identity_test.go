package service

import "testing"

func TestIdentityMaskAndRoundTrip(t *testing.T) {
	secret := []byte("test-ticket-qr-secret")
	id := "110101199003078890"
	if maskIDNumber(id) != "110***********8890" {
		t.Fatalf("mask: %s", maskIDNumber(id))
	}
	key := stableIdentityKey(secret, id)
	if key != stableIdentityKey(secret, "110101199003078890") {
		t.Fatal("stable key should ignore case/space identically after normalize")
	}
	cipher, err := encryptIdentity(secret, id)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptIdentity(secret, cipher)
	if err != nil || plain != id {
		t.Fatalf("roundtrip: %q %v", plain, err)
	}
}
