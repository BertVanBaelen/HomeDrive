package savedlink_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
	"github.com/BertVanBaelen/HomeDrive/server/internal/savedlink"
)

// FuzzRead checks that a .url file only ever yields an http or https
// address within the limits, which reads back the same.
func FuzzRead(f *testing.F) {
	for _, s := range []string{
		"[InternetShortcut]\r\nURL=https://example.com/\r\n",
		"[InternetShortcut]\nURL=javascript:alert(1)",
		"[InternetShortcut]\nURL=https://user:pw@example.com/",
		"[InternetShortcut]\nURL=HTTPS://EXAMPLE.com:8443/a?b#c",
		"[internetshortcut]\nurl = http://[::1]/",
		"URL=https://example.com/",
		"\xef\xbb\xbf[InternetShortcut]\nURL=https://éxample.com/",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		u, err := savedlink.Read(in)
		if err != nil {
			return
		}
		s := u.String()
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || len(s) > savedlink.MaxURL {
			t.Fatalf("Read accepted %q", s)
		}
		again, err := savedlink.Read(savedlink.Shortcut(u))
		if err != nil || again.String() != s {
			t.Fatalf("not stable: %q -> %v, %v", s, again, err)
		}
	})
}

// FuzzFileName checks that a link's name from any title is a valid file
// name ending in .url.
func FuzzFileName(f *testing.F) {
	for _, s := range []string{"", "AC/DC", "a\\b", "..", " x ", "\x00", "\xff", strings.Repeat("é", 200)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, title string) {
		if !utf8.ValidString(title) {
			return
		}
		name := savedlink.FileName(title)
		if valid, err := files.ValidateName(name); err != nil || valid != name || !savedlink.IsLink(name) {
			t.Fatalf("FileName(%q) = %q: %v", title, name, err)
		}
	})
}
