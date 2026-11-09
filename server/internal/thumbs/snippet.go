package thumbs

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	sheetpkg "github.com/BertVanBaelen/HomeDrive/server/internal/sheet"
)

// Snippet is the start of a document, laid out by the web app as a page,
// a code block, a sheet or a list. It is made in Go: no tool is involved.
type Snippet struct {
	// Kind is "text", "code", "sheet" or "archive".
	Kind string `json:"kind"`
	// Lines are a text's or code's first lines.
	Lines []string `json:"lines,omitempty"`
	// Rows are a sheet's first rows, as Excel shows the cells.
	Rows [][]string `json:"rows,omitempty"`
	// Entries are an archive's first items at its top: folders end in /.
	Entries []string `json:"entries,omitempty"`
	// Total is how many items are at the archive's top.
	Total int `json:"total,omitempty"`
}

const (
	snippetBytes = 2 << 10 // read of a text, code or CSV file
	snippetLines = 12
	lineChars    = 160
	sheetRows    = 6
	sheetCols    = 4
	cellChars    = 40
	zipEntries   = 8
	nameChars    = 80
	// maxZipDirectory is the most of a zip's central directory read.
	maxZipDirectory = 4 << 20
)

var errNotText = errors.New("binary contents")

func (s *Service) buildSnippet(kind string, f *os.File, versionID string) error {
	var sn Snippet
	var err error
	switch kind {
	case "text", "code":
		sn, err = textSnippet(kind, f)
	case "sheet":
		sn, err = csvSnippet(f)
	case "xlsx":
		sn, err = xlsxSnippet(f)
	case "archive":
		sn, err = zipSnippet(f)
	default:
		err = ErrNone
	}
	if err != nil {
		return err
	}
	b, err := json.Marshal(sn)
	if err != nil {
		return err
	}
	return s.cache.write(versionID, snippetSuffix, b)
}

// head reads the first snippetBytes of a file as text, without a
// byte-order mark and without a character cut in half. more is set when
// the file goes on.
func head(r io.Reader) (text string, more bool, err error) {
	b := make([]byte, snippetBytes+1)
	n, err := io.ReadFull(r, b)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	more = n > snippetBytes
	b = bytes.TrimPrefix(b[:min(n, snippetBytes)], []byte("\xef\xbb\xbf"))
	if bytes.IndexByte(b, 0) >= 0 {
		return "", false, errNotText
	}
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
		if len(b) < snippetBytes-utf8.UTFMax {
			// Invalid in the middle, not just cut short.
			return strings.ToValidUTF8(string(b), "�"), more, nil
		}
	}
	return string(b), more, nil
}

func textSnippet(kind string, r io.Reader) (Snippet, error) {
	text, more, err := head(r)
	if err != nil {
		return Snippet{}, err
	}
	lines := strings.Split(text, "\n")
	if more && len(lines) > 1 {
		// The last line was cut by the read.
		lines = lines[:len(lines)-1]
	}
	sn := Snippet{Kind: kind, Lines: []string{}}
	for _, l := range lines[:min(len(lines), snippetLines)] {
		sn.Lines = append(sn.Lines, cleanLine(l, lineChars))
	}
	for len(sn.Lines) > 0 && strings.TrimSpace(sn.Lines[len(sn.Lines)-1]) == "" {
		sn.Lines = sn.Lines[:len(sn.Lines)-1]
	}
	return sn, nil
}

// cleanLine makes a line safe to lay out: tabs as spaces, no control
// characters, at most max characters.
func cleanLine(s string, max int) string {
	s = strings.TrimRight(s, "\r")
	s = strings.ReplaceAll(s, "\t", "    ")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func csvSnippet(r io.Reader) (Snippet, error) {
	text, more, err := head(r)
	if err != nil {
		return Snippet{}, err
	}
	if more {
		if i := strings.LastIndexByte(text, '\n'); i >= 0 {
			text = text[:i]
		}
	}
	cr := csv.NewReader(strings.NewReader(text))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = false
	// Excel in much of Europe writes semicolons.
	first, _, _ := strings.Cut(text, "\n")
	if strings.Count(first, ";") > strings.Count(first, ",") {
		cr.Comma = ';'
	} else if strings.Count(first, "\t") > strings.Count(first, ",") {
		cr.Comma = '\t'
	}
	sn := Snippet{Kind: "sheet", Rows: [][]string{}}
	for len(sn.Rows) < sheetRows {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if len(sn.Rows) > 0 {
				break
			}
			return Snippet{}, err
		}
		sn.Rows = append(sn.Rows, row(rec))
	}
	return sn, nil
}

func row(cells []string) []string {
	out := make([]string, 0, sheetCols)
	for _, c := range cells[:min(len(cells), sheetCols)] {
		out = append(out, cleanLine(c, cellChars))
	}
	return out
}

// xlsxSnippet reads the first sheet's top left corner as Excel shows it:
// formatted, with formulas as their results cached in the file. It uses the
// same capped reader as the editor; larger workbooks get no snippet.
func xlsxSnippet(f *os.File) (Snippet, error) {
	data, err := io.ReadAll(io.LimitReader(f, sheetpkg.MaxFileBytes+1))
	if err != nil {
		return Snippet{}, err
	}
	sheets, err := sheetpkg.ReadOnly(context.Background(), data)
	if err != nil {
		return Snippet{}, err
	}
	sn := Snippet{Kind: "sheet", Rows: [][]string{}}
	for r := 1; r <= sheetRows; r++ {
		var cells []string
		for c := 0; c < sheetCols; c++ {
			cells = append(cells, sheets[0].Cells[string(rune('A'+c))+strconv.Itoa(r)])
		}
		for len(cells) > 0 && cells[len(cells)-1] == "" {
			cells = cells[:len(cells)-1]
		}
		sn.Rows = append(sn.Rows, row(cells))
	}
	// Trailing empty rows say nothing.
	for len(sn.Rows) > 0 && len(strings.Join(sn.Rows[len(sn.Rows)-1], "")) == 0 {
		sn.Rows = sn.Rows[:len(sn.Rows)-1]
	}
	return sn, nil
}

// zipSnippet lists what is at the top of a zip archive from its central
// directory only; nothing is extracted.
func zipSnippet(f *os.File) (Snippet, error) {
	info, err := f.Stat()
	if err != nil {
		return Snippet{}, err
	}
	size, err := zipDirectorySize(f, info.Size())
	if err != nil {
		return Snippet{}, err
	}
	if size < 0 || size > maxZipDirectory {
		return Snippet{}, errors.New("zip directory too large")
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return Snippet{}, err
	}
	sn := Snippet{Kind: "archive", Entries: []string{}}
	seen := map[string]bool{}
	for _, zf := range zr.File {
		name := strings.TrimPrefix(strings.ReplaceAll(zf.Name, `\`, "/"), "/")
		top, _, nested := strings.Cut(name, "/")
		if top == "" || top == "." || top == ".." || top == "__MACOSX" {
			continue
		}
		top = cleanLine(strings.ToValidUTF8(top, "�"), nameChars)
		if nested || zf.FileInfo().IsDir() {
			top += "/"
		}
		if seen[top] {
			continue
		}
		seen[top] = true
		sn.Total++
		if len(sn.Entries) < zipEntries {
			sn.Entries = append(sn.Entries, top)
		}
	}
	return sn, nil
}

// zipDirectorySize reads the size of a zip's central directory from its
// end records, before archive/zip reads the directory itself.
func zipDirectorySize(r io.ReaderAt, size int64) (int64, error) {
	const eocdLen, maxComment = 22, 0xffff
	tail := min(size, eocdLen+maxComment)
	b := make([]byte, tail)
	if _, err := r.ReadAt(b, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	i := bytes.LastIndex(b, []byte("PK\x05\x06"))
	if i < 0 || len(b)-i < eocdLen {
		return 0, errors.New("not a zip archive")
	}
	dirSize := int64(binary.LittleEndian.Uint32(b[i+12:]))
	if dirSize != 0xffffffff {
		return dirSize, nil
	}
	// Zip64: the locator just before points at the larger end record.
	if i < 20 || string(b[i-20:i-16]) != "PK\x06\x07" {
		return 0, errors.New("broken zip64 archive")
	}
	at := int64(binary.LittleEndian.Uint64(b[i-20+8:]))
	rec := make([]byte, 56)
	if at < 0 || at > size-56 {
		return 0, errors.New("broken zip64 archive")
	}
	if _, err := r.ReadAt(rec, at); err != nil {
		return 0, err
	}
	if string(rec[:4]) != "PK\x06\x06" {
		return 0, errors.New("broken zip64 archive")
	}
	return int64(binary.LittleEndian.Uint64(rec[40:])), nil
}
