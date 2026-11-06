package sheet

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"path"
	"strconv"
	"strings"
)

// Hostile-file limits for the zip container. Every part is read by our own
// linear streaming parsers; excelize never parses an uploaded workbook (its
// readers have quadratic/amplifying paths on hostile input and spill big
// parts to temp files).
const (
	maxUnzip   = 64 << 20 // sum of declared uncompressed sizes
	maxPart    = 16 << 20 // any single part
	maxParts   = 4096     // zip entries
	maxStrings = 1 << 20  // shared strings
	maxRels    = 10000
)

type pkg struct {
	zr    *zip.Reader
	files map[string]*zip.File // key: partKey(name)
}

func unsupported(reason string) error { return &UnsupportedError{Reason: reason} }

func partKey(name string) string { return strings.ToLower(strings.ReplaceAll(name, "\\", "/")) }

func openPkg(data []byte) (*pkg, error) {
	if len(data) < 4 || string(data[:4]) != "PK\x03\x04" {
		return nil, unsupported("invalid")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) > maxParts {
		return nil, unsupported("invalid")
	}
	p := &pkg{zr: zr, files: make(map[string]*zip.File, len(zr.File))}
	var total uint64
	for _, f := range zr.File {
		k := partKey(f.Name)
		if _, dup := p.files[k]; dup {
			return nil, unsupported("invalid")
		}
		if f.UncompressedSize64 >= maxPart {
			return nil, unsupported("too_big")
		}
		if total += f.UncompressedSize64; total > maxUnzip {
			return nil, unsupported("too_big")
		}
		// Every part must be copyable by Apply, not only the ones we parse.
		if off, err := f.DataOffset(); err != nil || off < 0 || uint64(off)+f.CompressedSize64 > uint64(len(data)) {
			return nil, unsupported("invalid")
		}
		p.files[k] = f
	}
	return p, nil
}

func (p *pkg) has(name string) bool { return name != "" && p.files[partKey(name)] != nil }

// read returns a part; archive/zip enforces the declared size and CRC.
func (p *pkg) read(name string) ([]byte, error) {
	f := p.files[partKey(name)]
	if f == nil {
		return nil, unsupported("invalid")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, unsupported("invalid")
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxPart))
	if err != nil {
		return nil, unsupported("invalid")
	}
	return b, nil
}

// xscan is a strict, non-recursive XML token reader that tracks byte
// offsets (so parts can be patched in place) and checks ctx regularly.
type xscan struct {
	d   *xml.Decoder
	ctx context.Context
	n   int
	off int // offset of the current token's first byte
}

func newScan(ctx context.Context, b []byte) *xscan {
	return &xscan{d: xml.NewDecoder(bytes.NewReader(b)), ctx: ctx}
}

func (s *xscan) next() (xml.Token, error) {
	if s.n++; s.n&0xfff == 0 {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
	}
	s.off = int(s.d.InputOffset())
	t, err := s.d.Token()
	if err == io.EOF {
		return nil, io.EOF
	}
	if err != nil {
		return nil, unsupported("invalid")
	}
	return t, nil
}

func (s *xscan) end() int { return int(s.d.InputOffset()) }

// attr returns an un-namespaced attribute.
func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}

func hasAttr(se xml.StartElement, local string) bool {
	for _, a := range se.Attr {
		if a.Name.Local == local && a.Name.Space == "" {
			return true
		}
	}
	return false
}

// prefixOf returns the namespace prefix ("x:") of the element starting at b.
func prefixOf(b []byte) string {
	for i := 1; i < len(b) && i < 64; i++ {
		switch b[i] {
		case ':':
			return string(b[1 : i+1])
		case ' ', '\t', '\r', '\n', '>', '/':
			return ""
		}
	}
	return ""
}

type rel struct {
	id, typ, target string
	external        bool
}

func (p *pkg) rels(ctx context.Context, part string) ([]rel, error) {
	dir, file := path.Split(part)
	name := dir + "_rels/" + file + ".rels"
	if !p.has(name) {
		return nil, nil
	}
	b, err := p.read(name)
	if err != nil {
		return nil, err
	}
	var out []rel
	s := newScan(ctx, b)
	for {
		tok, err := s.next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			if len(out) >= maxRels {
				return nil, unsupported("invalid")
			}
			out = append(out, rel{id: attr(se, "Id"), typ: attr(se, "Type"), target: attr(se, "Target"),
				external: strings.EqualFold(attr(se, "TargetMode"), "External")})
		}
	}
}

// resolve turns a relationship target into a part name.
func resolve(dir, target string) string {
	t := strings.ReplaceAll(target, "\\", "/")
	if strings.HasPrefix(t, "/") {
		return strings.TrimPrefix(path.Clean(t), "/")
	}
	return path.Join(dir, t)
}

type wbSheet struct {
	name, path string
	worksheet  bool
}

type workbook struct {
	path, relsPath             string
	data                       []byte
	sheets                     []wbSheet
	date1904, external, vba    bool
	sst, styles, calcChain     string
	calcStart, calcEnd, calcAt int // calcPr start tag (calcStart -1 if absent), insertion point
	prefix                     string
}

func (p *pkg) workbook(ctx context.Context) (*workbook, error) {
	root, err := p.rels(ctx, "")
	if err != nil {
		return nil, err
	}
	wb := &workbook{calcStart: -1, sst: "xl/sharedStrings.xml", styles: "xl/styles.xml"}
	for _, r := range root {
		if strings.HasSuffix(r.typ, "/officeDocument") && !r.external {
			wb.path = resolve("", r.target)
			break
		}
	}
	if !p.has(wb.path) {
		return nil, unsupported("invalid")
	}
	dir, file := path.Split(wb.path)
	wb.relsPath = dir + "_rels/" + file + ".rels"
	rels, err := p.rels(ctx, wb.path)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]rel, len(rels))
	for _, r := range rels {
		byID[r.id] = r
		t, target := r.typ, resolve(path.Dir(wb.path), r.target)
		switch {
		case strings.HasSuffix(t, "/externalLink"):
			wb.external = true
		case strings.HasSuffix(t, "/vbaProject"):
			wb.vba = true
		case r.external:
		case strings.HasSuffix(t, "/sharedStrings"):
			wb.sst = target
		case strings.HasSuffix(t, "/styles"):
			wb.styles = target
		case strings.HasSuffix(t, "/calcChain"):
			wb.calcChain = target
		}
	}
	for k := range p.files {
		if strings.HasPrefix(k, "xl/externallinks/") {
			wb.external = true
		}
		if k == "xl/vbaproject.bin" {
			wb.vba = true
		}
	}
	if wb.data, err = p.read(wb.path); err != nil {
		return nil, err
	}
	s := newScan(ctx, wb.data)
	depth := 0
	for {
		tok, err := s.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "workbook":
				if depth == 1 {
					wb.prefix = prefixOf(wb.data[s.off:])
				}
			case "workbookPr":
				v := attr(t, "date1904")
				wb.date1904 = v == "1" || v == "true"
			case "sheet":
				if depth != 3 {
					break
				}
				if len(wb.sheets) >= maxParts {
					return nil, unsupported("invalid")
				}
				sh := wbSheet{name: attr(t, "name")}
				for _, a := range t.Attr {
					if a.Name.Local == "id" && a.Name.Space != "" {
						if r, ok := byID[a.Value]; ok && !r.external {
							sh.path = resolve(path.Dir(wb.path), r.target)
							sh.worksheet = strings.HasSuffix(r.typ, "/worksheet")
						}
					}
				}
				wb.sheets = append(wb.sheets, sh)
			case "externalReference":
				wb.external = true
			case "calcPr":
				if depth == 2 {
					wb.calcStart, wb.calcEnd = s.off, s.end()
				}
			}
		case xml.EndElement:
			if depth == 2 {
				switch t.Name.Local {
				case "sheets", "functionGroups", "externalReferences", "definedNames":
					wb.calcAt = s.end()
				}
			}
			depth--
		}
	}
	if wb.calcAt == 0 {
		return nil, unsupported("invalid")
	}
	return wb, nil
}

// unescape decodes the OOXML _xHHHH_ escapes in linear time.
func unescape(s string) string {
	if !strings.Contains(s, "_x") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '_' && i+6 < len(s) && s[i+1] == 'x' && s[i+6] == '_' {
			if r, ok := hex4(s[i+2 : i+6]); ok {
				b.WriteRune(r)
				i += 6
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func hex4(h string) (rune, bool) {
	var r rune
	for i := 0; i < len(h); i++ {
		c := h[i]
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c|0x20 >= 'a' && c|0x20 <= 'f':
			r = r<<4 | rune(c|0x20-'a'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

func (p *pkg) sharedStrings(ctx context.Context, name string) ([]string, error) {
	if !p.has(name) {
		return nil, nil
	}
	b, err := p.read(name)
	if err != nil {
		return nil, err
	}
	var out []string
	var cur strings.Builder
	inSI, inT, rph := false, false, 0
	s := newScan(ctx, b)
	for {
		tok, err := s.next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "rPh":
				rph++
			case "t":
				inT = inSI && rph == 0
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "rPh":
				rph--
			case "si":
				if len(out) >= maxStrings {
					return nil, unsupported("too_big")
				}
				inSI = false
				out = append(out, unescape(cur.String()))
			}
		}
	}
}

// xcell is one <c> element of a worksheet with its byte offsets.
type xcell struct {
	idx, row, col      int // idx = (row-1)*MaxCols + col-1 (row, col 1-based)
	start, tagEnd, end int
	hasR, f, out       bool // out: beyond MaxRows x MaxCols
	s                  int
	t, v, is           string
	fText, fType, fSi  string
}

type xrow struct {
	r                          int
	hasR, custom               bool
	s                          int
	start, tagEnd, end, c0, c1 int // cells[c0:c1] belong to this row
}

type xcol struct{ min, max, s int }

type wsheet struct {
	data                     []byte
	prefix, sdPrefix         string
	dimStart, dimEnd, dimAt  int
	dimRows, dimCols         int
	sdStart, sdTagEnd, sdEnd int
	sdEndTag                 int // start of </sheetData>
	rows                     []xrow
	cells                    []xcell
	cols                     []xcol
	merged, needSST          bool
}

// scanSheet reads a worksheet. strict (editable view) rejects anything
// outside MaxRows x MaxCols; otherwise rows/columns beyond are dropped.
// Rows and cells must be in ascending order (as every writer does), which
// also bounds the work.
func scanSheet(ctx context.Context, b []byte, strict bool) (*wsheet, error) {
	ws := &wsheet{data: b, dimStart: -1, dimAt: -1, sdStart: -1}
	s := newScan(ctx, b)
	var stack []string
	var cur *xcell
	var fB, vB, isB strings.Builder
	var text *strings.Builder
	lastRow, lastCol, rph := 0, 0, 0
	for {
		tok, err := s.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth, name, parent := len(stack), t.Name.Local, ""
			if depth > 0 {
				parent = stack[depth-1]
			}
			stack = append(stack, name)
			switch {
			case depth == 0:
				if name != "worksheet" {
					return nil, unsupported("invalid")
				}
				ws.prefix = prefixOf(b[s.off:])
			case depth == 1:
				switch name {
				case "dimension":
					ws.dimStart = s.off
					ref := attr(t, "ref")
					if c, r, ok := parseCellName(ref[strings.IndexByte(ref, ':')+1:]); ok && c <= MaxCols && r <= MaxRows {
						ws.dimRows, ws.dimCols = r, c
					}
				case "sheetViews", "sheetFormatPr", "cols", "sheetData":
					if ws.dimAt < 0 {
						ws.dimAt = s.off
					}
					if name == "sheetData" {
						ws.sdStart, ws.sdTagEnd = s.off, s.end()
						ws.sdPrefix = prefixOf(b[s.off:])
					}
				}
			case depth == 2 && name == "col" && parent == "cols":
				lo, _ := strconv.Atoi(attr(t, "min"))
				hi, _ := strconv.Atoi(attr(t, "max"))
				st, _ := strconv.Atoi(attr(t, "style"))
				if len(ws.cols) < 1024 && st > 0 {
					ws.cols = append(ws.cols, xcol{lo, hi, st})
				}
			case name == "mergeCell" && parent == "mergeCells":
				ws.merged = true
			case depth == 2 && name == "row" && parent == "sheetData":
				r := lastRow + 1
				if hasAttr(t, "r") {
					n, err := strconv.Atoi(attr(t, "r"))
					if err != nil || n < 1 || n > 1048576 {
						return nil, unsupported("invalid")
					}
					r = n
				}
				if r <= lastRow {
					return nil, unsupported("invalid")
				}
				if r > MaxRows && !strict {
					return ws, nil
				}
				lastRow, lastCol = r, 0
				if r <= MaxRows {
					st, _ := strconv.Atoi(attr(t, "s"))
					cf := attr(t, "customFormat")
					ws.rows = append(ws.rows, xrow{r: r, hasR: hasAttr(t, "r"), custom: cf == "1" || cf == "true", s: st,
						start: s.off, tagEnd: s.end(), c0: len(ws.cells), c1: len(ws.cells)})
				}
			case depth == 3 && name == "c" && parent == "row" && lastRow > 0:
				col := lastCol + 1
				if hasAttr(t, "r") {
					c, r, ok := parseCellName(attr(t, "r"))
					if !ok || r != lastRow {
						return nil, unsupported("invalid")
					}
					col = c
				}
				if col <= lastCol {
					return nil, unsupported("invalid")
				}
				lastCol = col
				st, _ := strconv.Atoi(attr(t, "s"))
				cur = &xcell{row: lastRow, col: col, start: s.off, tagEnd: s.end(), hasR: hasAttr(t, "r"), s: st, t: attr(t, "t"),
					out: lastRow > MaxRows || col > MaxCols}
				if !cur.out {
					cur.idx = (lastRow-1)*MaxCols + col - 1
				}
				fB.Reset()
				vB.Reset()
				isB.Reset()
				rph = 0
			case cur == nil:
			case name == "f" && parent == "c":
				cur.f, cur.fType, cur.fSi, text = true, attr(t, "t"), attr(t, "si"), &fB
			case name == "v" && parent == "c":
				text = &vB
			case name == "rPh":
				rph++
			case name == "t" && rph == 0 && (parent == "is" || parent == "r"):
				text = &isB
			}
		case xml.CharData:
			if text != nil {
				text.Write(t)
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			depth, name := len(stack), t.Name.Local
			switch {
			case depth == 1 && name == "dimension":
				ws.dimEnd = s.end()
			case depth == 1 && name == "sheetData":
				ws.sdEndTag, ws.sdEnd = s.off, s.end()
			case depth == 2 && name == "row" && stack[1] == "sheetData":
				if lastRow <= MaxRows {
					ws.rows[len(ws.rows)-1].end = s.end()
				}
			case depth == 3 && name == "c" && cur != nil:
				cur.end = s.end()
				cur.fText, cur.v = fB.String(), vB.String()
				cur.is = unescape(isB.String())
				// Cells outside the grid are tolerated (and kept as they
				// are) only when they hold nothing, e.g. formatting.
				if cur.out {
					if strict && (cur.f || cur.v != "" || cur.is != "") {
						return nil, unsupported("too_large_grid")
					}
					cur, text = nil, nil
					break
				}
				if cur.t == "s" && !cur.f {
					ws.needSST = true
				}
				ws.cells = append(ws.cells, *cur)
				ws.rows[len(ws.rows)-1].c1 = len(ws.cells)
				cur, text = nil, nil
			case name == "rPh" && cur != nil:
				rph--
			case name == "f" || name == "v" || name == "t":
				text = nil
			}
		}
	}
	return ws, nil
}

// literal is the value of a non-formula cell (or a formula's cached value).
func (c *xcell) literal(sst []string) value {
	switch c.t {
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(c.v))
		if err != nil || i < 0 || i >= len(sst) {
			return textv(c.v)
		}
		return textv(sst[i])
	case "inlineStr":
		return textv(c.is)
	case "str":
		return textv(unescape(c.v))
	case "d":
		return textv(c.v)
	case "b":
		if strings.TrimSpace(c.v) == "1" {
			return textv("TRUE")
		}
		return textv("FALSE")
	case "e":
		if c.v == "" || len(c.v) > 16 {
			return errv(errValue)
		}
		return errv(c.v)
	}
	if c.v == "" {
		return value{}
	}
	if f, ok := numLiteral(c.v); ok {
		return value{k: kNum, n: f}
	}
	return textv(c.v)
}

// textv makes a text value; empty text counts as an empty cell.
func textv(s string) value {
	if s == "" {
		return value{}
	}
	return value{k: kText, s: s}
}

// colStyle is the default style of a new cell in column col (1-based).
func (ws *wsheet) colStyle(col int) int {
	for _, c := range ws.cols {
		if col >= c.min && col <= c.max {
			return c.s
		}
	}
	return 0
}
