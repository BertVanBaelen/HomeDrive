package sheet

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

type vector struct {
	Name   string            `json:"name"`
	Valid  *bool             `json:"valid"`
	Cells  map[string]string `json:"cells"`
	Expect map[string]string `json:"expect"`
}

func loadVectors(t testing.TB) []vector {
	b, err := os.ReadFile("testdata/formulas.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []vector
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}
	return vs
}

func blank(t testing.TB) []byte {
	b, err := Blank()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func norm(v string) string {
	if strings.HasPrefix(v, "#") {
		return "#ERR"
	}
	return v
}

func TestVectors(t *testing.T) {
	ctx := context.Background()
	base := blank(t)
	for _, v := range loadVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			out, err := Apply(ctx, base, Edit{Rows: MaxRows, Cols: MaxCols, Cells: v.Cells})
			if v.Valid != nil && !*v.Valid {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Apply: want ErrInvalid, got %v", err)
				}
				if Validate(v.Cells["A1"]) == nil {
					t.Fatalf("Validate accepted %q", v.Cells["A1"])
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			g, err := Read(ctx, out)
			if err != nil {
				t.Fatal(err)
			}
			for k, want := range v.Expect {
				if got := norm(g.Cells[k].V); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func TestBlank(t *testing.T) {
	ctx := context.Background()
	b := blank(t)
	g, err := Read(ctx, b)
	if err != nil || g.Rows != MinRows || g.Cols != MinCols || len(g.Cells) != 0 {
		t.Fatalf("Read(Blank) = %+v, %v", g, err)
	}
	s, err := ReadOnly(ctx, b)
	if err != nil || len(s) != 1 || s[0].Name != "Sheet1" || len(s[0].Cells) != 0 {
		t.Fatalf("ReadOnly(Blank) = %+v, %v", s, err)
	}
	out, err := Apply(ctx, b, Edit{Rows: 30, Cols: 10})
	if err != nil {
		t.Fatal(err)
	}
	if g, err = Read(ctx, out); err != nil || g.Rows != 30 || g.Cols != 10 {
		t.Fatalf("grid size not kept: %+v, %v", g, err)
	}
}

func TestApplyRead(t *testing.T) {
	ctx := context.Background()
	cells := map[string]string{
		"A1": "1", "A2": "2.5", "A3": "=SUM(A1:A2)", "b1": "a<b&c\"d'", "B2": "_x0041_ & \x09tab\nline\r\n",
		"B3": "'=1+1", "B4": "'12", "B5": "  ", "C1": "=a1/0", "C2": "=B1", "C3": "= max( $a$1 , A2 )*2",
		"D1": "1e3", "D2": "-0", "D3": "日本語 ✓", "E1": "=C2+1", "E2": "TRUE",
	}
	out, err := Apply(ctx, blank(t), Edit{Rows: 40, Cols: 9, Cells: cells})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Read(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Cell{
		"A1": {"1", "1"}, "A2": {"2.5", "2.5"}, "A3": {"=SUM(A1:A2)", "3.5"}, "B1": {"a<b&c\"d'", "a<b&c\"d'"},
		"B2": {"_x0041_ & \ttab\nline\r\n", "_x0041_ & \ttab\nline\r\n"}, "B3": {"'=1+1", "=1+1"}, "B4": {"'12", "12"},
		"B5": {"  ", "  "}, "C1": {"=A1/0", "#DIV/0!"}, "C2": {"=B1", "a<b&c\"d'"}, "C3": {"=MAX($A$1,A2)*2", "5"},
		"D1": {"1000", "1000"}, "D2": {"0", "0"}, "D3": {"日本語 ✓", "日本語 ✓"}, "E1": {"=C2+1", "#VALUE!"}, "E2": {"TRUE", "TRUE"},
	}
	if g.Rows != 40 || g.Cols != 9 || len(g.Cells) != len(want) {
		t.Fatalf("grid %dx%d with %d cells: %+v", g.Rows, g.Cols, len(g.Cells), g.Cells)
	}
	for k, w := range want {
		if g.Cells[k] != w {
			t.Errorf("%s = %+v, want %+v", k, g.Cells[k], w)
		}
	}
	// Raw typed back gives the same cell.
	again := map[string]string{}
	for k, c := range g.Cells {
		again[k] = c.Raw
	}
	out2, err := Apply(ctx, out, Edit{Rows: 40, Cols: 9, Cells: again})
	if err != nil {
		t.Fatal(err)
	}
	if g2, err := Read(ctx, out2); err != nil || len(g2.Cells) != len(g.Cells) {
		t.Fatalf("re-apply: %v", err)
	} else {
		for k, c := range g.Cells {
			if g2.Cells[k] != c {
				t.Errorf("re-apply %s = %+v, want %+v", k, g2.Cells[k], c)
			}
		}
	}
	// Clearing and changing cells updates dependent formulas.
	out3, err := Apply(ctx, out, Edit{Rows: 40, Cols: 9, Cells: map[string]string{"A1": "", "A2": "4", "C1": ""}})
	if err != nil {
		t.Fatal(err)
	}
	g3, err := Read(ctx, out3)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g3.Cells["A1"]; ok || g3.Cells["A3"].V != "4" || g3.Cells["C3"].V != "8" || g3.Cells["C1"].Raw != "" {
		t.Fatalf("after clearing: %+v", g3.Cells)
	}

	// Cached values are in the file for any reader, and Excel recalculates on open.
	f, err := excelize.OpenReader(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for cell, v := range map[string]string{"A3": "3.5", "C1": "#DIV/0!", "C2": "a<b&c\"d'", "C3": "5", "B3": "=1+1", "B2": "_x0041_ & \ttab\nline\r\n"} {
		if got, err := f.GetCellValue("Sheet1", cell); err != nil || got != v {
			t.Errorf("excelize %s = %q, %v; want %q", cell, got, err, v)
		}
	}
	if fo, _ := f.GetCellFormula("Sheet1", "C3"); fo != "MAX($A$1,A2)*2" {
		t.Errorf("formula C3 = %q", fo)
	}
	if fo, _ := f.GetCellFormula("Sheet1", "B3"); fo != "" {
		t.Errorf("text became a formula: %q", fo)
	}
	if wb := partOf(t, out, "xl/workbook.xml"); !strings.Contains(wb, `fullCalcOnLoad="1"`) {
		t.Errorf("no fullCalcOnLoad: %s", wb)
	}
}

func TestStylesSurvive(t *testing.T) {
	ctx := context.Background()
	f := excelize.NewFile()
	must(t, f.SetColWidth("Sheet1", "B", "B", 33))
	pct, err := f.NewStyle(&excelize.Style{NumFmt: 10, Font: &excelize.Font{Bold: true}})
	must(t, err)
	two, err := f.NewStyle(&excelize.Style{NumFmt: 2})
	must(t, err)
	must(t, f.SetCellValue("Sheet1", "B2", 0.5))
	must(t, f.SetCellStyle("Sheet1", "B2", "B2", pct))
	must(t, f.SetCellStyle("Sheet1", "C3", "C3", two))
	must(t, f.SetCellValue("Sheet1", "A1", "keep"))
	must(t, f.SetRowHeight("Sheet1", 2, 40))
	buf, err := f.WriteToBuffer()
	must(t, err)
	g, err := Read(ctx, buf.Bytes())
	must(t, err)
	if g.Cells["B2"] != (Cell{"0.5", "50.00%"}) {
		t.Fatalf("B2 = %+v", g.Cells["B2"])
	}
	out, err := Apply(ctx, buf.Bytes(), Edit{Rows: 20, Cols: 8, Cells: map[string]string{"B2": "=1/3", "C3": "=B2*3", "D4": "x"}})
	must(t, err)
	g, err = Read(ctx, out)
	must(t, err)
	if g.Cells["B2"].V != "33.33%" || g.Cells["C3"].V != "1.00" || g.Cells["A1"].V != "keep" {
		t.Fatalf("after apply: %+v", g.Cells)
	}
	r, err := excelize.OpenReader(bytes.NewReader(out))
	must(t, err)
	defer r.Close()
	if w, _ := r.GetColWidth("Sheet1", "B"); w != 33 {
		t.Errorf("column width = %v", w)
	}
	if h, _ := r.GetRowHeight("Sheet1", 2); h != 40 {
		t.Errorf("row height = %v", h)
	}
	if s, _ := r.GetCellStyle("Sheet1", "B2"); s != pct {
		t.Errorf("B2 style = %d, want %d", s, pct)
	}
	if s, _ := r.GetCellStyle("Sheet1", "C3"); s != two {
		t.Errorf("C3 style = %d, want %d", s, two)
	}
	if v, _ := r.GetCellValue("Sheet1", "B2"); v != "33.33%" {
		t.Errorf("cached B2 = %q", v)
	}
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// rezip rewrites a zip: parts maps a name to new content ("" deletes).
func rezip(t testing.TB, base []byte, parts map[string]string) []byte {
	zr, err := zip.NewReader(bytes.NewReader(base), int64(len(base)))
	must(t, err)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	done := map[string]bool{}
	write := func(name, content string) {
		w, err := zw.Create(name)
		must(t, err)
		_, err = io.WriteString(w, content)
		must(t, err)
	}
	for _, f := range zr.File {
		if c, ok := parts[f.Name]; ok {
			done[f.Name] = true
			if c != "" {
				write(f.Name, c)
			}
			continue
		}
		must(t, zw.Copy(f))
	}
	for name, c := range parts {
		if !done[name] && c != "" {
			write(name, c)
		}
	}
	must(t, zw.Close())
	return out.Bytes()
}

func partOf(t testing.TB, data []byte, name string) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	must(t, err)
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			must(t, err)
			b, err := io.ReadAll(rc)
			must(t, err)
			return string(b)
		}
	}
	t.Fatalf("no part %s", name)
	return ""
}

const wsHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`

func withSheet(t testing.TB, xml string) []byte {
	return rezip(t, blank(t), map[string]string{"xl/worksheets/sheet1.xml": xml})
}

func workbookWith(t testing.TB, build func(f *excelize.File)) []byte {
	f := excelize.NewFile()
	build(f)
	b, err := f.WriteToBuffer()
	must(t, err)
	return b.Bytes()
}

func TestUnsupported(t *testing.T) {
	ctx := context.Background()
	b := blank(t)
	cases := map[string][]byte{
		"too_big": append([]byte("PK\x03\x04"), make([]byte, MaxFileBytes)...),
		"invalid": []byte("definitely not a workbook"),
		"formula": workbookWith(t, func(f *excelize.File) { must(t, f.SetCellFormula("Sheet1", "A1", `HYPERLINK("http://x","y")`)) }),
		"merged_cells": workbookWith(t, func(f *excelize.File) {
			must(t, f.MergeCell("Sheet1", "A1", "B2"))
		}),
		"multiple_sheets": workbookWith(t, func(f *excelize.File) { _, err := f.NewSheet("Two"); must(t, err) }),
		"too_large_grid":  workbookWith(t, func(f *excelize.File) { must(t, f.SetCellValue("Sheet1", "BA1", 1)) }),
	}
	extra := map[string][]byte{
		"formula/sheet ref":    workbookWith(t, func(f *excelize.File) { must(t, f.SetCellFormula("Sheet1", "A1", "Sheet1!B1")) }),
		"formula/external":     rezip(t, b, map[string]string{"xl/externalLinks/externalLink1.xml": "<externalLink/>"}),
		"formula/array":        withSheet(t, wsHead+`<sheetData><row r="1"><c r="A1"><f t="array" ref="A1">1+1</f></c></row></sheetData></worksheet>`),
		"formula/webservice":   withSheet(t, wsHead+`<sheetData><row r="1"><c r="A1" t="str"><f>_xlfn.WEBSERVICE("http://x")</f><v>1</v></c></row></sheetData></worksheet>`),
		"invalid/vba":          rezip(t, b, map[string]string{"xl/vbaProject.bin": "x"}),
		"invalid/truncated":    b[:len(b)/2],
		"invalid/ole":          append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 4096)...),
		"invalid/empty":        nil,
		"invalid/rows order":   withSheet(t, wsHead+`<sheetData><row r="2"/><row r="1"/></sheetData></worksheet>`),
		"invalid/not xml":      withSheet(t, wsHead+`<sheetData><row r="1"><c r="A1"><v>1</v></row></sheetData></worksheet>`),
		"invalid/entity":       withSheet(t, `<!DOCTYPE x [<!ENTITY a "aaaa">]>`+wsHead+`<sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>&a;</t></is></c></row></sheetData></worksheet>`),
		"too_large_grid/row":   withSheet(t, wsHead+`<sheetData><row r="1001"><c r="A1001"><v>1</v></c></row></sheetData></worksheet>`),
		"too_big/part":         rezip(t, b, map[string]string{"xl/worksheets/sheet1.xml": wsHead + strings.Repeat(" ", maxPart) + "<sheetData/></worksheet>"}),
		"merged_cells/foreign": withSheet(t, wsHead+`<sheetData/><mergeCells count="1"><mergeCell ref="A1:A2"/></mergeCells></worksheet>`),
	}
	for k, v := range extra {
		cases[k] = v
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			reason, _, _ := strings.Cut(name, "/")
			_, err := Read(ctx, data)
			var ue *UnsupportedError
			if !errors.As(err, &ue) || ue.Reason != reason {
				t.Fatalf("Read: %v, want %s", err, reason)
			}
			if _, err := Apply(ctx, data, Edit{Rows: 1, Cols: 1, Cells: map[string]string{"A1": "1"}}); !errors.As(err, &ue) || ue.Reason != reason {
				t.Fatalf("Apply: %v, want %s", err, reason)
			}
		})
	}
	// Empty, formatted rows and cells beyond the grid are kept but allowed.
	ok := withSheet(t, wsHead+`<sheetData><row r="1"><c r="A1"><v>1</v></c><c r="BA1" s="0"/></row><row r="5000" ht="30" customHeight="1"/></sheetData></worksheet>`)
	out, err := Apply(ctx, ok, Edit{Rows: 1000, Cols: 8, Cells: map[string]string{"B1": "2", "A1000": "=A1+B1"}})
	must(t, err)
	if g, err := Read(ctx, out); err != nil || g.Cells["A1000"].V != "3" {
		t.Fatalf("read back: %+v %v", g.Cells, err)
	}
	if s := partOf(t, out, "xl/worksheets/sheet1.xml"); !strings.Contains(s, `<row r="5000" ht="30" customHeight="1"/>`) || !strings.Contains(s, `<c r="BA1" s="0"/>`) {
		t.Fatalf("rows beyond the grid lost: %s", s)
	}
}

func TestZipBombs(t *testing.T) {
	ctx := context.Background()
	var big bytes.Buffer
	zw := zip.NewWriter(&big)
	for i := 0; i < 5; i++ {
		w, err := zw.Create("xl/media/bomb" + string(rune('0'+i)) + ".bin")
		must(t, err)
		_, err = w.Write(make([]byte, 15<<20))
		must(t, err)
	}
	must(t, zw.Close())
	if big.Len() > MaxFileBytes {
		t.Fatalf("bomb is %d bytes", big.Len())
	}
	var ue *UnsupportedError
	if _, err := Read(ctx, big.Bytes()); !errors.As(err, &ue) || ue.Reason != "too_big" {
		t.Fatalf("Read: %v", err)
	}
	if _, err := ReadOnly(ctx, big.Bytes()); !errors.As(err, &ue) || ue.Reason != "too_big" {
		t.Fatalf("ReadOnly: %v", err)
	}
}

func TestPrefixedAndImplicit(t *testing.T) {
	ctx := context.Background()
	// x: prefix, rows and cells without r, spans, self-closing rows, shared formulas.
	sheet := `<?xml version="1.0" encoding="UTF-8"?><x:worksheet xmlns:x="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<x:sheetData><x:row spans="1:2"><x:c><x:v>1</x:v></x:c><x:c><x:f t="shared" ref="B1:B3" si="0">A1*2</x:f><x:v>9</x:v></x:c></x:row>` +
		`<x:row><x:c><x:v>2</x:v></x:c><x:c><x:f t="shared" si="0"/></x:c></x:row>` +
		`<x:row r="3"><x:c r="A3"><x:v>3</x:v></x:c><x:c r="B3"><x:f t="shared" si="0"/></x:c></x:row><x:row r="5" s="0"/></x:sheetData></x:worksheet>`
	data := withSheet(t, sheet)
	g, err := Read(ctx, data)
	must(t, err)
	if g.Cells["B2"] != (Cell{"=A2*2", "4"}) || g.Cells["B3"] != (Cell{"=A3*2", "6"}) || g.Cells["B1"].V != "2" {
		t.Fatalf("shared formulas: %+v", g.Cells)
	}
	out, err := Apply(ctx, data, Edit{Rows: 20, Cols: 8, Cells: map[string]string{"A2": "10", "C1": "x", "C5": "=SUM(B1:B3)", "A4": "7"}})
	must(t, err)
	g, err = Read(ctx, out)
	must(t, err)
	want := map[string]string{"A1": "1", "B1": "2", "A2": "10", "B2": "20", "B3": "6", "C1": "x", "C5": "28", "A4": "7"}
	for k, v := range want {
		if g.Cells[k].V != v {
			t.Errorf("%s = %q, want %q", k, g.Cells[k].V, v)
		}
	}
	f, err := excelize.OpenReader(bytes.NewReader(out))
	must(t, err)
	defer f.Close()
	for k, v := range want {
		if got, _ := f.GetCellValue("Sheet1", k); got != v {
			t.Errorf("excelize %s = %q, want %q", k, got, v)
		}
	}
}

func TestReadOnly(t *testing.T) {
	ctx := context.Background()
	data := workbookWith(t, func(f *excelize.File) {
		must(t, f.SetCellValue("Sheet1", "A1", "hello"))
		must(t, f.MergeCell("Sheet1", "A1", "B1"))
		_, err := f.NewSheet("Data")
		must(t, err)
		must(t, f.SetCellValue("Data", "C2", 0.25))
		pct, err := f.NewStyle(&excelize.Style{NumFmt: 9})
		must(t, err)
		must(t, f.SetCellStyle("Data", "C2", "C2", pct))
		must(t, f.SetCellValue("Data", "AZ1000", "end"))
		must(t, f.SetCellValue("Data", "BA1", "cut"))
		must(t, f.SetCellValue("Data", "A1001", "cut"))
	})
	s, err := ReadOnly(ctx, data)
	must(t, err)
	if len(s) != 2 || s[0].Name != "Sheet1" || s[0].Cells["A1"] != "hello" || s[1].Name != "Data" || s[1].Cells["C2"] != "25%" ||
		s[1].Cells["AZ1000"] != "end" || len(s[1].Cells) != 2 || s[1].Rows != MaxRows || s[1].Cols != MaxCols {
		t.Fatalf("ReadOnly = %+v", s)
	}
}

func TestEditInvalid(t *testing.T) {
	ctx := context.Background()
	b := blank(t)
	long := strings.Repeat("x", MaxCellChars+1)
	many := map[string]string{}
	for i := 0; i < 2200; i++ {
		many[cellKey(i)] = strings.Repeat("y", MaxCellChars)
	}
	heavy := map[string]string{}
	for i := 0; i < 200; i++ {
		heavy[cellKey(i)] = "=SUM(A2:AZ1000)"
	}
	cases := map[string]Edit{
		"rows 0":         {Rows: 0, Cols: 1},
		"rows 1001":      {Rows: 1001, Cols: 1},
		"cols 53":        {Rows: 1, Cols: 53},
		"key outside":    {Rows: 2, Cols: 2, Cells: map[string]string{"C1": "1"}},
		"key 1001":       {Rows: 1000, Cols: 52, Cells: map[string]string{"A1001": "1"}},
		"key BA":         {Rows: 1000, Cols: 52, Cells: map[string]string{"BA1": "1"}},
		"key junk":       {Rows: 1000, Cols: 52, Cells: map[string]string{"<x>": "1"}},
		"key dollar":     {Rows: 1000, Cols: 52, Cells: map[string]string{"$A$1": "1"}},
		"key duplicate":  {Rows: 1000, Cols: 52, Cells: map[string]string{"a1": "1", "A1": "2"}},
		"too long":       {Rows: 1000, Cols: 52, Cells: map[string]string{"A1": long}},
		"control":        {Rows: 1000, Cols: 52, Cells: map[string]string{"A1": "a\x00b"}},
		"bad utf8":       {Rows: 1000, Cols: 52, Cells: map[string]string{"A1": "a\xffb"}},
		"formula":        {Rows: 1000, Cols: 52, Cells: map[string]string{"A1": `=HYPERLINK("x")`}},
		"too much data":  {Rows: 1000, Cols: 52, Cells: many},
		"too complex":    {Rows: 1000, Cols: 52, Cells: heavy},
		"lone equals":    {Rows: 1000, Cols: 52, Cells: map[string]string{"A1": "="}},
		"formula length": {Rows: 1000, Cols: 52, Cells: map[string]string{"A1": "=1" + strings.Repeat("+1", 250)}},
	}
	for name, e := range cases {
		if _, err := Apply(ctx, b, e); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		} else if strings.Contains(err.Error(), "HYPERLINK") || strings.Contains(err.Error(), "xxx") {
			t.Errorf("%s: error echoes input: %v", name, err)
		}
	}
}

func TestValidateCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"= sum( $a$1 : b2 , .5 )": "SUM($A$1:B2,0.5)",
		"=-+-a1*(2)":              "-+-A1*(2)",
		"=AVERAGE (A$3:$b1)":      "AVERAGE(A$3:$B1)",
	} {
		f, err := compile(in, 0, 0)
		if err != nil || f.text != want {
			t.Errorf("%q -> %v %v, want %q", in, f, err, want)
		}
	}
	for _, bad := range []string{"", "1+1", "=", "=1+", "=SUM(A1:B)", "=1,000", "=$$A1", "=AAA1", "=TRUE", "=PI()", "=SUM(1;2)", "=A2+ü", "=(1", "=1)", "=1.2.3", "=-"} {
		if !errors.Is(Validate(bad), ErrInvalid) {
			t.Errorf("Validate(%q) accepted", bad)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	for in, want := range map[float64]string{
		0: "0", 1: "1", -1.5: "-1.5", 0.1 + 0.2: "0.3", 1e15: "1E+15", 123456789012345: "123456789012345",
		1e-9: "0.000000001", 1.5e-10: "1.5E-10", 1e100: "1E+100", -2.5e-300: "-2.5E-300", 1e308: "1E+308",
		0.000123456789012345678: "0.000123456789012346", 99999999999999.999: "100000000000000", 5e-324: "5E-324",
	} {
		if got := FormatNumber(in); got != want {
			t.Errorf("FormatNumber(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestSemaphore(t *testing.T) {
	b := blank(t)
	sem <- struct{}{}
	sem <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := Read(ctx, b); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third op: %v", err)
	}
	done := make(chan error)
	go func() { _, err := Read(context.Background(), b); done <- err }()
	select {
	case <-done:
		t.Fatal("third op did not wait")
	case <-time.After(50 * time.Millisecond):
	}
	<-sem
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-sem
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if _, err := Apply(cctx, b, Edit{Rows: 1, Cols: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func FuzzValidate(f *testing.F) {
	for _, v := range loadVectors(f) {
		for _, raw := range v.Cells {
			if strings.HasPrefix(raw, "=") {
				f.Add(raw)
			}
		}
	}
	f.Fuzz(func(t *testing.T, s string) {
		fo, err := compile(s, 0, 0)
		if (err == nil) != (Validate(s) == nil) {
			t.Fatal("compile and Validate disagree")
		}
		if err != nil {
			return
		}
		again, err := compile("="+fo.text, 0, 0)
		if err != nil || again.text != fo.text {
			t.Fatalf("canonical form %q does not round-trip", fo.text)
		}
		bk := newBook()
		bk.set(0, value{k: kNum, n: 3}, nil)
		bk.set(1, value{k: kText, s: "x"}, nil)
		bk.set(MaxCols, value{k: kErr, s: errDiv0}, nil)
		bk.set(5*MaxCols+2, value{}, fo)
		bk.set(nCells-1, value{}, fo)
		if bk.work <= maxRangeWork {
			if err := bk.evalAll(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func seedFiles(f *testing.F) [][]byte {
	b, err := Blank()
	if err != nil {
		f.Fatal(err)
	}
	out, err := Apply(context.Background(), b, Edit{Rows: 3, Cols: 3, Cells: map[string]string{"A1": "1", "A2": "=A1*2", "B1": "x", "C3": "=SUM(A1:A2)"}})
	if err != nil {
		f.Fatal(err)
	}
	return [][]byte{b, out}
}

func FuzzRead(f *testing.F) {
	for _, s := range seedFiles(f) {
		f.Add(s)
	}
	catchPanics = false
	f.Fuzz(func(t *testing.T, data []byte) { fuzzOne(t, data) })
}

// FuzzSheetXML fuzzes the worksheet part inside a valid package, which
// reaches the XML scanner and the writer far more often than raw bytes.
func FuzzSheetXML(f *testing.F) {
	f.Add(wsHead + `<dimension ref="A1:C3"/><sheetData><row r="1" spans="1:3"><c r="A1"><v>1</v></c><c r="B1" t="s"><v>0</v></c></row><row r="2"><c r="A2"><f>A1*2</f><v>2</v></c><c><f t="shared" ref="B2:B3" si="0">A1+1</f></c></row><row r="3"><c r="B3"><f t="shared" si="0"/></c></row></sheetData></worksheet>`)
	f.Add(wsHead + `<sheetData><row><c t="inlineStr"><is><t>_x0041_</t></is></c></row><row r="4" s="1" customFormat="1"/></sheetData><mergeCells/></worksheet>`)
	base := blank(f)
	catchPanics = false
	f.Fuzz(func(t *testing.T, sheet string) {
		fuzzOne(t, rezip(t, base, map[string]string{"xl/worksheets/sheet1.xml": sheet}))
	})
}

// fuzzOne: nothing panics, and every file Read accepts can be edited and
// read again.
func fuzzOne(t *testing.T, data []byte) {
	ctx := context.Background()
	_, _ = ReadOnly(ctx, data)
	g, err := Read(ctx, data)
	if err != nil {
		var ue *UnsupportedError
		if !errors.As(err, &ue) {
			t.Fatalf("Read: unexpected error %v", err)
		}
		return
	}
	out, err := Apply(ctx, data, Edit{Rows: g.Rows, Cols: g.Cols, Cells: map[string]string{"A1": "=B2+1", "C7": "text", "B2": "2"}})
	if err != nil {
		t.Fatalf("Apply after Read: %v", err)
	}
	g2, err := Read(ctx, out)
	if err != nil {
		t.Fatalf("Read after Apply: %v", err)
	}
	if g2.Cells["A1"].V != "3" || g2.Cells["C7"].V != "text" {
		t.Fatalf("edit lost: %+v", g2.Cells)
	}
}
