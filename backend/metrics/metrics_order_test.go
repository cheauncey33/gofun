package metrics

import "testing"

func TestNormalizeOrderSource(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"normal":     "normal",
		"rush_sale":  "rush_sale",
		"waitlist":   "waitlist",
		"":           "unknown",
		"other":      "unknown",
	}
	for in, want := range cases {
		if got := NormalizeOrderSource(in); got != want {
			t.Fatalf("NormalizeOrderSource(%q)=%q want %q", in, got, want)
		}
	}
}
