package service

import "testing"

func TestPlacePrefixAndLabel(t *testing.T) {
	t.Parallel()
	if got := placePrefix("看台 A"); got != "看台-A" {
		t.Fatalf("placePrefix = %q", got)
	}
	if got := formatPlaceLabel("看台", 17); got != "看台-00017" {
		t.Fatalf("formatPlaceLabel = %q", got)
	}
	if got := placePrefix("   "); got != "区" {
		t.Fatalf("empty prefix = %q", got)
	}
}
