package files

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// FuzzValidateName checks that every accepted name is safe to store and
// show, and that validation is stable.
func FuzzValidateName(f *testing.F) {
	for _, s := range []string{"report.pdf", " a ", "..", "a/b", `a\b`, "x\x00y", "\xff", "é.txt", strings.Repeat("a", 300)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out, err := ValidateName(in)
		if err != nil {
			return
		}
		if out == "" || out == "." || out == ".." || len(out) > 255 || !utf8.ValidString(out) ||
			strings.ContainsAny(out, `/\`) || strings.ContainsFunc(out, unicode.IsControl) ||
			strings.TrimSpace(out) != out {
			t.Fatalf("ValidateName(%q) accepted %q", in, out)
		}
		if again, err := ValidateName(out); err != nil || again != out {
			t.Fatalf("not stable: %q -> %q -> %q, %v", in, out, again, err)
		}
	})
}

// FuzzZipSegment checks that a zip entry name step can never leave the
// folder the zip is unpacked into.
func FuzzZipSegment(f *testing.F) {
	for _, s := range []string{"photo.jpg", "", ".", "..", "a/b", `..\..\x`, "x\x00y", "\xff", "C:", " .. "} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := zipSegment(in)
		if out == "" || out == "." || out == ".." || !utf8.ValidString(out) ||
			strings.ContainsAny(out, `/\`) || strings.ContainsFunc(out, unicode.IsControl) {
			t.Fatalf("zipSegment(%q) = %q", in, out)
		}
		if in != out && utf8.ValidString(in) && !strings.ContainsAny(in, `/\`) &&
			!strings.ContainsFunc(in, unicode.IsControl) && in != "" && in != "." && in != ".." &&
			!strings.ContainsRune(in, utf8.RuneError) {
			t.Fatalf("zipSegment(%q) changed a safe name to %q", in, out)
		}
	})
}

// FuzzNaturalCompare checks that sorting by name is a consistent order.
func FuzzNaturalCompare(f *testing.F) {
	f.Add("IMG 2", "img 10")
	f.Add("a01", "a1")
	f.Add("", "x")
	f.Fuzz(func(t *testing.T, a, b string) {
		ab, ba := naturalCompare(a, b), naturalCompare(b, a)
		if sign(ab) != -sign(ba) {
			t.Fatalf("compare(%q, %q) = %d but compare(%q, %q) = %d", a, b, ab, b, a, ba)
		}
		if naturalCompare(a, a) != 0 {
			t.Fatalf("compare(%q, itself) != 0", a)
		}
	})
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// FuzzUploadChunks sends chunks at arbitrary offsets and lengths and checks
// that an upload never holds more than announced, only accepts the next
// offset, and keeps exactly the bytes it acknowledged.
func FuzzUploadChunks(f *testing.F) {
	s, uid, root, _ := newService(f)
	f.Add(int64(10), int64(0), int64(5), int64(5), int64(5))
	f.Add(int64(3), int64(0), int64(9), int64(1), int64(1))
	f.Add(int64(0), int64(1), int64(0), int64(-1), int64(2))
	f.Fuzz(func(t *testing.T, size, off1, len1, off2, len2 int64) {
		if size < 0 || size > 1<<12 {
			return
		}
		ctx := context.Background()
		up, err := s.CreateUpload(ctx, uid, root, "fuzz.bin", size)
		if err != nil {
			t.Fatal(err)
		}
		defer s.CancelUpload(ctx, uid, up.ID)
		var want []byte
		for _, c := range [][2]int64{{off1, len1}, {off2, len2}} {
			off, n := c[0], c[1]
			if n < 0 || n > 1<<13 {
				continue
			}
			chunk := bytes.Repeat([]byte{byte(len(want))}, int(n))
			received, err := s.WriteChunk(ctx, uid, up.ID, off, n, bytes.NewReader(chunk))
			if err == nil {
				if off != int64(len(want)) || off+n > size {
					t.Fatalf("accepted chunk at %d+%d with %d received, size %d", off, n, len(want), size)
				}
				want = append(want, chunk...)
			}
			if received != int64(len(want)) {
				t.Fatalf("received %d, want %d", received, len(want))
			}
		}
		st, err := s.UploadStatus(ctx, uid, up.ID)
		if err != nil || st.Received != int64(len(want)) || st.Received > size {
			t.Fatalf("status %+v, %v; want %d", st, err, len(want))
		}
		if st.Received == size {
			n, err := s.CompleteUpload(ctx, uid, up.ID)
			if err != nil {
				t.Fatal(err)
			}
			got := readAll(t, s, uid, n.ID)
			if !bytes.Equal(got, want) {
				t.Fatalf("stored %v, want %v", got, want)
			}
			s.Trash(ctx, uid, n.ID)
			s.DeleteForever(ctx, uid, n.ID)
		}
	})
}

func readAll(t *testing.T, s *Service, uid, id string) []byte {
	t.Helper()
	f, _, err := s.Open(context.Background(), uid, id)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var b bytes.Buffer
	b.ReadFrom(f)
	return b.Bytes()
}
