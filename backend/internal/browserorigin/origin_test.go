package browserorigin

import (
	"strings"
	"testing"
)

func TestCanonicalizeAcceptsExactFirstPartyOrigins(t *testing.T) {
	t.Parallel()
	origins, err := ParseList(
		"https://ascendany.example,ascendany-app://bundle,capacitor://localhost," +
			"https://localhost,http://127.0.0.1:5173,http://[::1]:5173",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "ascendany-app://bundle,capacitor://localhost,http://127.0.0.1:5173," +
		"http://[::1]:5173,https://ascendany.example,https://localhost"
	if strings.Join(origins, ",") != want {
		t.Fatalf("origins=%q want=%q", origins, want)
	}
}

func TestCanonicalizeRejectsAmbiguousOrUnsafeOrigins(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"",
		"https://ascendany.example,",
		"https://ascendany.example, https://localhost",
		"https://ascendany.example,https://ascendany.example",
		"http://ascendany.example",
		"https://ascendany.example/",
		"https://ascendany.example/path",
		"https://user@ascendany.example",
		"https://升任.例子",
		"https://ASCENDANY.example",
		"https://ascendany.example:443",
		"http://127.0.0.1:05173",
		"ascendany-app://other",
		"capacitor://device",
		"file://localhost",
	} {
		if _, err := ParseList(raw); err == nil {
			t.Fatalf("ParseList(%q) unexpectedly succeeded", raw)
		}
	}
}
