package sheet

import (
	"context"
	"encoding/xml"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Number display for cells with a non-General number format. styles.xml is
// parsed by our own scanner; only the (short) format code of a style is
// handed to excelize, which formats the value in a private scratch
// workbook. Anything unusual falls back to General (FormatNumber).
const (
	maxXfs        = 65536
	maxNumFmts    = 1000
	maxFmtStyles  = 256     // distinct formatted styles per call
	maxFmtCalls   = 200_000 // formatted values per call
	maxFmtCodeLen = 255     // as Excel
)

type numFormatter struct {
	xfs      []int          // cellXfs index -> numFmtId
	codes    map[int]string // custom number formats
	date1904 bool
	f        *excelize.File
	ids      map[int]int // cellXfs index -> scratch style id (-1: General)
	last     int
	calls    int
}

func (p *pkg) numFormatter(ctx context.Context, name string, date1904 bool) (*numFormatter, error) {
	nf := &numFormatter{codes: map[int]string{}, date1904: date1904, ids: map[int]int{}, last: -1}
	if !p.has(name) {
		return nf, nil
	}
	b, err := p.read(name)
	if err != nil {
		return nil, err
	}
	s := newScan(ctx, b)
	var stack []string
	for {
		tok, err := s.next()
		if err == io.EOF {
			return nf, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, t.Name.Local)
			switch {
			case len(stack) != 3:
			case parent == "cellXfs" && t.Name.Local == "xf" && len(nf.xfs) < maxXfs:
				id, _ := strconv.Atoi(attr(t, "numFmtId"))
				nf.xfs = append(nf.xfs, id)
			case parent == "numFmts" && t.Name.Local == "numFmt" && len(nf.codes) < maxNumFmts:
				id, err := strconv.Atoi(attr(t, "numFmtId"))
				if code := attr(t, "formatCode"); err == nil && len(code) <= maxFmtCodeLen {
					nf.codes[id] = code
				}
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
}

// format shows x as the cell style s displays it.
func (nf *numFormatter) format(x float64, s int) string {
	if nf == nil || s <= 0 || s >= len(nf.xfs) || nf.xfs[s] == 0 || math.Abs(x) >= 1e15 || nf.calls >= maxFmtCalls {
		return FormatNumber(x)
	}
	nf.calls++
	sid, ok := nf.ids[s]
	if !ok {
		sid = nf.style(nf.xfs[s])
		nf.ids[s] = sid
	}
	if sid < 0 {
		return FormatNumber(x)
	}
	if v, ok := nf.apply(sid, canonNum(x)); ok {
		return v
	}
	return FormatNumber(x)
}

// style registers a scratch style for numFmtId id (-1 if not supported).
func (nf *numFormatter) style(id int) (sid int) {
	defer func() {
		if recover() != nil {
			sid = -1
		}
	}()
	code, custom := nf.codes[id]
	if len(nf.ids) >= maxFmtStyles || (!custom && id >= 164) || strings.EqualFold(code, "General") || (custom && code == "") {
		return -1
	}
	if nf.f == nil {
		nf.f = excelize.NewFile()
		if nf.date1904 {
			t := true
			if nf.f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &t}) != nil {
				return -1
			}
		}
	}
	st := &excelize.Style{NumFmt: id}
	if custom {
		st = &excelize.Style{CustomNumFmt: &code}
	}
	sid, err := nf.f.NewStyle(st)
	if err != nil {
		return -1
	}
	return sid
}

func (nf *numFormatter) apply(sid int, v string) (out string, ok bool) {
	defer func() {
		if recover() != nil {
			out, ok = "", false
		}
	}()
	if sid != nf.last {
		if nf.f.SetCellStyle("Sheet1", "A1", "A1", sid) != nil {
			return "", false
		}
		nf.last = sid
	}
	if nf.f.SetCellDefault("Sheet1", "A1", v) != nil {
		return "", false
	}
	out, err := nf.f.GetCellValue("Sheet1", "A1")
	return out, err == nil && out != ""
}
