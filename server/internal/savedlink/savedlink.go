// Package savedlink handles links saved from the share sheet: the .url
// files they are stored as, and the previews (title, author, thumbnail)
// that a fixed list of oEmbed providers give for them. Homedrive never
// connects to a host taken from the user's input: the address only ever
// travels as a query parameter to a provider on the list.
package savedlink

import (
	"bufio"
	"bytes"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Ext is the extension of saved links: Windows internet shortcuts, which
// still open on a PC after an export.
const Ext = ".url"

// MaxURL is the longest address a link may have.
const MaxURL = 2048

// MaxFile is the largest .url file that is read for its address.
const MaxFile = 64 << 10

var ErrInvalidURL = errors.New("invalid link address")

// Parse checks an address for a saved link: http or https, a host, no user
// info, at most MaxURL characters.
func Parse(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxURL || !utf8.ValidString(raw) || strings.ContainsFunc(raw, unicode.IsControl) {
		return nil, ErrInvalidURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" ||
		u.User != nil || u.Opaque != "" {
		return nil, ErrInvalidURL
	}
	u.Host = strings.ToLower(u.Host)
	if len(u.String()) > MaxURL {
		return nil, ErrInvalidURL
	}
	return u, nil
}

// Find returns the first http or https address in shared text, the way
// Android apps put a link inside the text instead of the url field, and the
// text without it.
func Find(text string) (*url.URL, string) {
	for _, field := range strings.Fields(text) {
		lower := strings.ToLower(field)
		i := strings.Index(lower, "https://")
		if j := strings.Index(lower, "http://"); i < 0 || (j >= 0 && j < i) {
			i = j
		}
		if i < 0 {
			continue
		}
		candidate := strings.TrimRight(field[i:], `.,;:!?)]}>"'`)
		if u, err := Parse(candidate); err == nil {
			rest := strings.Replace(text, candidate, "", 1)
			rest = strings.ReplaceAll(rest, "  ", " ")
			return u, strings.TrimSpace(rest)
		}
	}
	return nil, text
}

// Shortcut is the contents of a .url file for an address.
func Shortcut(u *url.URL) []byte {
	return []byte("[InternetShortcut]\r\nURL=" + u.String() + "\r\n")
}

// Read finds the address in a .url file: the URL= line of its
// [InternetShortcut] section.
func Read(b []byte) (*url.URL, error) {
	if len(b) > MaxFile {
		return nil, ErrInvalidURL
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	sc := bufio.NewScanner(bytes.NewReader(b))
	section := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && section == "[internetshortcut]" && strings.EqualFold(strings.TrimSpace(key), "URL") {
			return Parse(value)
		}
	}
	return nil, ErrInvalidURL
}

// FileName makes the name of a saved link from its title: no slashes or
// control characters, short enough for the name rules with the extension.
func FileName(title string) string {
	title = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return '-'
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	const max = 255 - len(Ext)
	for len(title) > max {
		_, size := utf8.DecodeLastRuneInString(title)
		title = title[:len(title)-size]
	}
	title = strings.TrimRight(title, " .")
	if title == "" {
		title = "Link"
	}
	return title + Ext
}

// IsLink reports whether a file name is a saved link's.
func IsLink(name string) bool {
	return len(name) > len(Ext) && strings.EqualFold(name[len(name)-len(Ext):], Ext)
}

// Site is how a card names where a link goes: the provider's name, else
// the host without "www.".
func Site(u *url.URL) string {
	if p := providerOf(u); p != nil {
		return p.name
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// Title is the title a link gets when nothing better is known: its host.
func Title(u *url.URL) string { return strings.TrimPrefix(u.Hostname(), "www.") }
