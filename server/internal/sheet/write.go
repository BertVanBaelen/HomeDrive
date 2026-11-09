package sheet

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// write splices the edited and all formula cells into the original sheet
// part, updates the dimension, sets fullCalcOnLoad, drops the calc chain and
// repacks the zip, copying every other part unchanged.
func (d *doc) write(ctx context.Context, e Edit, edits []edit) ([]byte, error) {
	ws, data := d.ws, d.ws.data
	regen := make([]bool, nCells)
	for _, ed := range edits {
		regen[ed.idx] = true
	}
	rows, cols := e.Rows, e.Cols
	var list []int
	for i := range regen {
		if d.bk.forms[i] != nil {
			regen[i] = true
		}
		if regen[i] {
			list = append(list, i)
		}
		if d.bk.forms[i] != nil || d.bk.vals[i].k != kEmpty {
			rows, cols = max(rows, i/MaxCols+1), max(cols, i%MaxCols+1)
		}
	}

	var body bytes.Buffer
	pos, k := ws.sdTagEnd, 0
	for ri := range ws.rows {
		row := &ws.rows[ri]
		body.Write(data[pos:row.start])
		for k < len(list) && list[k]/MaxCols+1 < row.r {
			k = d.newRow(&body, list, k)
		}
		j := k
		for j < len(list) && list[j]/MaxCols+1 == row.r {
			j++
		}
		d.copyRow(&body, row, list[k:j])
		k, pos = j, row.end
	}
	for k < len(list) {
		k = d.newRow(&body, list, k)
	}
	sd := splice{ws.sdTagEnd, ws.sdEndTag, nil}
	if ws.sdTagEnd == ws.sdEnd { // <sheetData/>
		sd = splice{ws.sdStart, ws.sdEnd, nil}
		body.WriteString("</" + ws.sdPrefix + "sheetData>")
		sd.repl = append([]byte("<"+ws.sdPrefix+"sheetData>"), body.Bytes()...)
	} else {
		body.Write(data[pos:ws.sdEndTag])
		sd.repl = body.Bytes()
	}
	ref := "A1"
	if rows > 1 || cols > 1 {
		ref += ":" + colName(cols-1) + strconv.Itoa(rows)
	}
	dim := splice{ws.dimAt, ws.dimAt, []byte("<" + ws.prefix + `dimension ref="` + ref + `"/>`)}
	if ws.dimStart >= 0 {
		dim.start, dim.end = ws.dimStart, ws.dimEnd
	}
	sheetXML, err := splices(data, dim, sd)
	if err != nil {
		return nil, err
	}
	repl := map[string][]byte{partKey(d.sh.path): sheetXML}

	wb := d.wb
	calc := splice{wb.calcAt, wb.calcAt, []byte("<" + wb.prefix + `calcPr fullCalcOnLoad="1"/>`)}
	if wb.calcStart >= 0 {
		calc = splice{wb.calcStart, wb.calcEnd, []byte(patchTag(wb.data[wb.calcStart:wb.calcEnd], ` fullCalcOnLoad="1"`, false, "fullCalcOnLoad"))}
	}
	if repl[partKey(wb.path)], err = splices(wb.data, calc); err != nil {
		return nil, err
	}

	drop := map[string]bool{}
	if wb.calcChain != "" {
		drop[partKey(wb.calcChain)] = true
		if ct := "[Content_Types].xml"; d.p.has(ct) {
			b, err := d.p.read(ct)
			if err == nil {
				b, err = removeElems(ctx, b, func(se xml.StartElement) bool {
					return se.Name.Local == "Override" && strings.EqualFold(attr(se, "PartName"), "/"+wb.calcChain)
				})
			}
			if err != nil {
				return nil, err
			}
			repl[partKey(ct)] = b
		}
		if d.p.has(wb.relsPath) {
			b, err := d.p.read(wb.relsPath)
			if err == nil {
				b, err = removeElems(ctx, b, func(se xml.StartElement) bool {
					return se.Name.Local == "Relationship" && strings.HasSuffix(attr(se, "Type"), "/calcChain")
				})
			}
			if err != nil {
				return nil, err
			}
			repl[partKey(wb.relsPath)] = b
		}
	}
	return d.repack(ctx, repl, drop)
}

// newRow writes a new <row> for the cells list[k:] of one row.
func (d *doc) newRow(b *bytes.Buffer, list []int, k int) int {
	r := list[k] / MaxCols
	var cells strings.Builder
	for ; k < len(list) && list[k]/MaxCols == r; k++ {
		cells.WriteString(d.genCell(list[k], d.ws.colStyle(list[k]%MaxCols+1)))
	}
	if cells.Len() > 0 {
		p := d.ws.sdPrefix
		b.WriteString("<" + p + `row r="` + strconv.Itoa(r+1) + `">` + cells.String() + "</" + p + "row>")
	}
	return k
}

// copyRow copies an existing row, regenerating the cells in list (one row,
// ascending) and making every row/cell reference explicit.
func (d *doc) copyRow(b *bytes.Buffer, row *xrow, list []int) {
	data := d.ws.data
	tag, self := data[row.start:row.tagEnd], row.tagEnd == row.end
	rAttr := ` r="` + strconv.Itoa(row.r) + `"`
	if len(list) == 0 {
		if row.hasR {
			b.Write(data[row.start:row.end])
		} else {
			b.WriteString(patchTag(tag, rAttr, false, "r"))
			b.Write(data[row.tagEnd:row.end])
		}
		return
	}
	b.WriteString(patchTag(tag, rAttr, true, "r", "spans"))
	style := func(idx int) int {
		if row.custom && row.s > 0 {
			return row.s
		}
		return d.ws.colStyle(idx%MaxCols + 1)
	}
	pos, j := row.tagEnd, 0
	for ci := row.c0; ci < row.c1; ci++ {
		c := &d.ws.cells[ci]
		b.Write(data[pos:c.start])
		for ; j < len(list) && list[j] < c.idx; j++ {
			b.WriteString(d.genCell(list[j], style(list[j])))
		}
		switch {
		case j < len(list) && list[j] == c.idx:
			b.WriteString(d.genCell(c.idx, c.s))
			j++
		case c.hasR:
			b.Write(data[c.start:c.end])
		default:
			b.WriteString(patchTag(data[c.start:c.tagEnd], ` r="`+cellKey(c.idx)+`"`, false, "r"))
			b.Write(data[c.tagEnd:c.end])
		}
		pos = c.end
	}
	for ; j < len(list); j++ {
		b.WriteString(d.genCell(list[j], style(list[j])))
	}
	if self {
		b.WriteString("</" + prefixOf(tag) + "row>")
	} else {
		b.Write(data[pos:row.end])
	}
}

// genCell writes cell idx from the computed book with style s ("" when the
// cell is empty and unstyled).
func (d *doc) genCell(idx, s int) string {
	p := d.ws.sdPrefix
	head := "<" + p + `c r="` + cellKey(idx) + `"`
	if s > 0 {
		head += ` s="` + strconv.Itoa(s) + `"`
	}
	v := d.bk.vals[idx]
	if f := d.bk.forms[idx]; f != nil {
		t, val := "", "0"
		switch v.k {
		case kNum:
			val = canonNum(v.n)
		case kText:
			t, val = ` t="str"`, escText(v.s)
		case kErr:
			t, val = ` t="e"`, escText(v.s)
		}
		return head + t + "><" + p + "f>" + escText(f.text) + "</" + p + "f><" + p + "v>" + val + "</" + p + "v></" + p + "c>"
	}
	switch v.k {
	case kNum:
		return head + "><" + p + "v>" + canonNum(v.n) + "</" + p + "v></" + p + "c>"
	case kText:
		return head + ` t="inlineStr"><` + p + "is><" + p + `t xml:space="preserve">` + escText(v.s) + "</" + p + "t></" + p + "is></" + p + "c>"
	case kErr:
		return head + ` t="e"><` + p + "v>" + escText(v.s) + "</" + p + "v></" + p + "c>"
	}
	if s > 0 {
		return head + "/>"
	}
	return ""
}

// escText escapes text for XML and for OOXML's _xHHHH_ convention
// (characters XML cannot hold, and a literal "_x0041_").
func escText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '_' && i+6 < len(s) && s[i+1] == 'x' && s[i+6] == '_' && validHex(s[i+2:i+6]):
			b.WriteString("_x005F_")
		case r == utf8.RuneError && n == 1:
			b.WriteRune(utf8.RuneError)
		case (r < 0x20 && r != '\t' && r != '\n') || r == 0xFFFE || r == 0xFFFF:
			b.WriteString("_x" + strings.ToUpper(strconv.FormatInt(int64(r)|0x10000, 16)[1:]) + "_")
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

func validHex(h string) bool { _, ok := hex4(h); return ok }

// patchTag rewrites a raw start tag (already checked by the XML decoder):
// it drops the named attributes, appends add and, if open, turns "<a/>"
// into "<a>".
func patchTag(tag []byte, add string, open bool, drop ...string) string {
	i := 1
	for i < len(tag) && !isSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' {
		i++
	}
	var b strings.Builder
	b.Write(tag[:i])
	for {
		for i < len(tag) && isSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || tag[i] == '/' || tag[i] == '>' {
			break
		}
		k := i
		for i < len(tag) && tag[i] != '=' && !isSpace(tag[i]) {
			i++
		}
		name := string(tag[k:i])
		for i < len(tag) && tag[i] != '"' && tag[i] != '\'' {
			i++
		}
		if i < len(tag) {
			q := tag[i]
			for i++; i < len(tag) && tag[i] != q; i++ {
			}
			i = min(i+1, len(tag))
		}
		if !slices.Contains(drop, name) {
			b.WriteByte(' ')
			b.Write(tag[k:i])
		}
	}
	b.WriteString(add)
	if open {
		b.WriteByte('>')
	} else {
		b.Write(tag[min(i, len(tag)):])
	}
	return b.String()
}

type splice struct {
	start, end int
	repl       []byte
}

// splices applies replacements; they must be in order and not overlap
// (a sheet with its parts in an unusual order is refused).
func splices(data []byte, sp ...splice) ([]byte, error) {
	var b bytes.Buffer
	pos := 0
	for _, s := range sp {
		if s.start < pos || s.end < s.start || s.end > len(data) {
			return nil, unsupported("invalid")
		}
		b.Write(data[pos:s.start])
		b.Write(s.repl)
		pos = s.end
	}
	b.Write(data[pos:])
	return b.Bytes(), nil
}

// removeElems removes every element (with its content) matching match.
func removeElems(ctx context.Context, data []byte, match func(xml.StartElement) bool) ([]byte, error) {
	s := newScan(ctx, data)
	var b bytes.Buffer
	pos, depth, rmDepth, rmStart := 0, 0, -1, 0
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
			if rmDepth < 0 && match(t) {
				rmDepth, rmStart = depth, s.off
			}
		case xml.EndElement:
			if depth == rmDepth {
				b.Write(data[pos:rmStart])
				pos, rmDepth = s.end(), -1
			}
			depth--
		}
	}
	b.Write(data[pos:])
	return b.Bytes(), nil
}

// repack writes the zip again: replaced parts are checked and compressed,
// dropped parts left out, all others copied without recompression.
func (d *doc) repack(ctx context.Context, repl map[string][]byte, drop map[string]bool) ([]byte, error) {
	total := uint64(0)
	for _, f := range d.p.zr.File {
		k := partKey(f.Name)
		if b, ok := repl[k]; ok {
			if len(b) >= maxPart {
				return nil, invalid("result too large")
			}
			if err := wellFormed(ctx, b); err != nil {
				return nil, err
			}
			total += uint64(len(b))
		} else if !drop[k] {
			total += f.UncompressedSize64
		}
	}
	if total > maxUnzip {
		return nil, invalid("result too large")
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range d.p.zr.File {
		k := partKey(f.Name)
		if b, ok := repl[k]; ok {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: f.Modified})
			if err == nil {
				_, err = w.Write(b)
			}
			if err != nil {
				return nil, err
			}
			continue
		}
		if drop[k] {
			continue
		}
		if err := zw.Copy(f); err != nil {
			return nil, unsupported("invalid")
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if out.Len() > MaxFileBytes {
		return nil, invalid("result too large")
	}
	return out.Bytes(), nil
}

// wellFormed checks that b is a complete XML document.
func wellFormed(ctx context.Context, b []byte) error {
	s := newScan(ctx, b)
	for {
		if _, err := s.next(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
