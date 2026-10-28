package backup

import (
	"strings"
	"testing"
)

// FuzzSafeName checks that exported names are usable on Windows, exFAT and
// SMB shares whatever the name in Homedrive was.
func FuzzSafeName(f *testing.F) {
	for _, s := range []string{"Tax: 2026?.pdf", "CON", "nul.txt", "trailing. ", "", `a/b\c`, "\x01x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := safeName(in)
		if out == "" || strings.ContainsAny(out, `<>:"/\|?*`) || strings.HasSuffix(out, ".") || strings.HasSuffix(out, " ") {
			t.Fatalf("safeName(%q) = %q", in, out)
		}
		for _, r := range out {
			if r < 0x20 {
				t.Fatalf("safeName(%q) = %q has a control character", in, out)
			}
		}
		if again := safeName(out); again != out {
			t.Fatalf("not stable: %q -> %q -> %q", in, out, again)
		}
	})
}
