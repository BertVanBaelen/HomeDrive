// Package sheet reads and edits small, single-sheet .xlsx workbooks safely.
//
// Uploaded workbooks are hostile input. They are never opened with excelize;
// a strict streaming reader (xlsx.go) checks the zip and XML with hard caps,
// and edits are spliced into the original parts (write.go) so styles, column
// widths, number formats and everything else are kept byte for byte.
// Formulas are limited to a tiny grammar (formula.go) and computed here
// (eval.go); the computed values are stored as the cached values and the
// workbook is marked to fully recalculate when Excel opens it.
//
// Limits: files up to MaxFileBytes, at most 64 MB unzipped (16 MB per part),
// an editable grid of MaxRows x MaxCols (empty styled cells beyond it are
// kept but not shown), formulas up to MaxFormulaChars and MaxDepth nesting,
// cell input up to MaxCellChars. A blank or small sheet is shown as at least
// MinRows x MinCols. At most two operations run at the same time.
package sheet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

const (
	MaxFileBytes = 5 << 20
	Timeout      = 20 * time.Second // waiting for a slot and the work itself
	MaxRows      = 1000
	MaxCols      = 52

	MinRows      = 20      // grid shown for a blank or small sheet
	MinCols      = 8       //
	MaxCellChars = 2000    // characters (runes) of one cell input
	MaxEditBytes = 4 << 20 // all cell inputs of one Edit together
	MaxSheets    = 20      // sheets shown by ReadOnly
	maxNameChars = 100     // sheet names shown by ReadOnly
	maxGridBytes = 8 << 20 // Raw+V returned by Read
	maxViewBytes = 16 << 20
	maxViewCells = 200_000
)

// Cell is one cell of the editable view. Raw is what the user would type:
// a number, text (prefixed with "'" when it would otherwise be read as a
// number or formula) or "=FORMULA" in canonical form; V is the value shown.
type Cell struct {
	Raw string `json:"raw"`
	V   string `json:"v"`
}

// Grid is the editable view; only non-empty cells are listed (keys like
// "B7"). Rows/Cols is the used range, at least MinRows x MinCols.
type Grid struct {
	Rows  int             `json:"rows"`
	Cols  int             `json:"cols"`
	Cells map[string]Cell `json:"cells"`
}

// Sheet is the read-only view of one sheet: shown values only, truncated to
// MaxRows x MaxCols and MaxCellChars per value.
type Sheet struct {
	Name  string            `json:"name"`
	Rows  int               `json:"rows"`
	Cols  int               `json:"cols"`
	Cells map[string]string `json:"cells"`
}

// Edit lists changed cells (raw input per key; "" clears the cell) and the
// grid size the user sees.
type Edit struct {
	Rows  int               `json:"rows"`
	Cols  int               `json:"cols"`
	Cells map[string]string `json:"cells"`
}

// UnsupportedError means a workbook cannot be edited safely. Reason is one
// of "too_big", "multiple_sheets", "merged_cells", "formula",
// "too_large_grid", "invalid".
type UnsupportedError struct{ Reason string }

func (e *UnsupportedError) Error() string { return "sheet: unsupported workbook: " + e.Reason }

// ErrInvalid is wrapped by every error about a bad Edit or formula.
var ErrInvalid = errors.New("sheet: invalid edit")

func invalid(msg string) error { return fmt.Errorf("%w: %s", ErrInvalid, msg) }

var sem = make(chan struct{}, 2)

// catchPanics is switched off by the fuzz tests so that panics surface.
var catchPanics = true

// guard checks the size, waits for one of two slots and turns panics into
// an "invalid" error.
func guard(ctx context.Context, size int, fn func() error) (err error) {
	if size > MaxFileBytes {
		return unsupported("too_big")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-sem }()
	defer func() {
		if catchPanics {
			if recover() != nil {
				err = unsupported("invalid")
			}
		}
	}()
	return fn()
}

// Validate checks a formula ("=...") against the supported grammar.
func Validate(formula string) error {
	if _, err := compile(formula, 0, 0); err != nil {
		return invalid("unsupported formula")
	}
	return nil
}

// Blank returns a new, empty one-sheet workbook ("Sheet1").
func Blank() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	b, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// doc is a loaded editable workbook.
type doc struct {
	p   *pkg
	wb  *workbook
	sh  wbSheet
	ws  *wsheet
	sst []string
	nf  *numFormatter
	bk  *book
	at  []int32 // cell index -> 1 + index in ws.cells (0: no element)
}

func load(ctx context.Context, data []byte) (*doc, error) {
	p, err := openPkg(data)
	if err != nil {
		return nil, err
	}
	wb, err := p.workbook(ctx)
	switch {
	case err != nil:
		return nil, err
	case wb.external:
		return nil, unsupported("formula")
	case wb.vba:
		return nil, unsupported("invalid")
	case len(wb.sheets) > 1:
		return nil, unsupported("multiple_sheets")
	case len(wb.sheets) == 0 || !wb.sheets[0].worksheet || !p.has(wb.sheets[0].path):
		return nil, unsupported("invalid")
	}
	d := &doc{p: p, wb: wb, sh: wb.sheets[0], bk: newBook(), at: make([]int32, nCells)}
	b, err := p.read(d.sh.path)
	if err != nil {
		return nil, err
	}
	if d.ws, err = scanSheet(ctx, b, true); err != nil {
		return nil, err
	}
	if d.ws.sdStart < 0 {
		return nil, unsupported("invalid")
	}
	if d.ws.merged {
		return nil, unsupported("merged_cells")
	}
	if d.ws.needSST {
		if d.sst, err = p.sharedStrings(ctx, wb.sst); err != nil {
			return nil, err
		}
	}
	if d.nf, err = p.numFormatter(ctx, wb.styles, wb.date1904); err != nil {
		return nil, err
	}
	masters := map[string]*xcell{}
	for i := range d.ws.cells {
		c := &d.ws.cells[i]
		d.at[c.idx] = int32(i + 1)
		if c.f && c.fType == "shared" && c.fText != "" && masters[c.fSi] == nil {
			masters[c.fSi] = c
		}
	}
	for i := range d.ws.cells {
		c := &d.ws.cells[i]
		if !c.f {
			d.bk.set(c.idx, c.literal(d.sst), nil)
			continue
		}
		text, dr, dc := c.fText, 0, 0
		switch c.fType {
		case "", "normal":
		case "shared":
			if m := masters[c.fSi]; text == "" && m != nil {
				text, dr, dc = m.fText, c.row-m.row, c.col-m.col
			}
		default:
			return nil, unsupported("formula")
		}
		f, err := compile("="+text, dr, dc)
		if err != nil {
			return nil, unsupported("formula")
		}
		d.bk.set(c.idx, value{}, f)
	}
	if d.bk.work > maxRangeWork {
		return nil, unsupported("formula")
	}
	return d, d.bk.evalAll(ctx)
}

// show formats a value for display in a cell with style s.
func (d *doc) show(v value, s int) string {
	if v.k == kNum {
		return d.nf.format(v.n, s)
	}
	return v.display()
}

// rawText is how a text value is typed back: with a "'" prefix when it
// would otherwise be taken as a number or formula.
func rawText(s string) string {
	if _, isNum := numLiteral(s); isNum || s[0] == '=' || s[0] == '\'' {
		return "'" + s
	}
	return s
}

// Read returns the editable view of a workbook, or an *UnsupportedError.
func Read(ctx context.Context, data []byte) (Grid, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var g Grid
	err := guard(ctx, len(data), func() error {
		d, err := load(ctx, data)
		if err != nil {
			return err
		}
		g = Grid{Rows: max(d.ws.dimRows, MinRows), Cols: max(d.ws.dimCols, MinCols), Cells: map[string]Cell{}}
		size := 0
		for i := range d.ws.cells {
			c := &d.ws.cells[i]
			g.Rows, g.Cols = max(g.Rows, c.row), max(g.Cols, c.col)
			v := d.bk.vals[c.idx]
			var cell Cell
			switch {
			case d.bk.forms[c.idx] != nil:
				cell = Cell{Raw: "=" + d.bk.forms[c.idx].text, V: d.show(v, c.s)}
			case v.k == kNum:
				cell = Cell{Raw: canonNum(v.n), V: d.show(v, c.s)}
			case v.k == kText:
				cell = Cell{Raw: rawText(v.s), V: v.s}
			case v.k == kErr:
				cell = Cell{Raw: v.s, V: v.s}
			default:
				continue
			}
			if size += len(cell.Raw) + len(cell.V); size > maxGridBytes {
				return unsupported("too_big")
			}
			g.Cells[cellKey(c.idx)] = cell
		}
		return nil
	})
	if err != nil {
		return Grid{}, err
	}
	return g, nil
}

// ReadOnly returns the shown (cached) values of up to MaxSheets sheets.
// Sheets that cannot be read are returned empty.
func ReadOnly(ctx context.Context, data []byte) ([]Sheet, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var out []Sheet
	err := guard(ctx, len(data), func() error {
		p, err := openPkg(data)
		if err != nil {
			return err
		}
		wb, err := p.workbook(ctx)
		if err != nil {
			return err
		}
		if len(wb.sheets) == 0 {
			return unsupported("invalid")
		}
		nf, err := p.numFormatter(ctx, wb.styles, wb.date1904)
		if err != nil {
			nf = nil
		}
		var sst []string
		sstDone, size, count := false, 0, 0
		for _, sh := range wb.sheets[:min(len(wb.sheets), MaxSheets)] {
			if err := ctx.Err(); err != nil {
				return err
			}
			view := Sheet{Name: truncate(sh.name, maxNameChars), Rows: MinRows, Cols: MinCols, Cells: map[string]string{}}
			out = append(out, view)
			if !sh.worksheet || !p.has(sh.path) {
				continue
			}
			b, err := p.read(sh.path)
			if err != nil {
				continue
			}
			ws, err := scanSheet(ctx, b, false)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				continue
			}
			if ws.needSST && !sstDone {
				sstDone = true
				sst, _ = p.sharedStrings(ctx, wb.sst)
			}
			view.Rows, view.Cols = max(view.Rows, ws.dimRows), max(view.Cols, ws.dimCols)
			d := &doc{nf: nf}
			for i := range ws.cells {
				c := &ws.cells[i]
				v := c.literal(sst)
				if v.k == kEmpty {
					continue
				}
				s := truncate(d.show(v, c.s), MaxCellChars)
				if size += len(s); size > maxViewBytes || count >= maxViewCells {
					break
				}
				count++
				view.Rows, view.Cols = max(view.Rows, c.row), max(view.Cols, c.col)
				view.Cells[cellKey(c.idx)] = s
			}
			out[len(out)-1] = view
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

type edit struct {
	idx int
	v   value
	f   *formula
}

// checkEdit validates an Edit and parses every input.
func checkEdit(e Edit) ([]edit, error) {
	if e.Rows < 1 || e.Rows > MaxRows || e.Cols < 1 || e.Cols > MaxCols {
		return nil, invalid("grid size out of range")
	}
	if len(e.Cells) > nCells {
		return nil, invalid("too many cells")
	}
	seen := make(map[int]bool, len(e.Cells))
	out := make([]edit, 0, len(e.Cells))
	total := 0
	for k, raw := range e.Cells {
		idx, ok := parseKey(k)
		if !ok || idx/MaxCols >= e.Rows || idx%MaxCols >= e.Cols {
			return nil, invalid("bad cell reference")
		}
		if seen[idx] {
			return nil, invalid("duplicate cell " + cellKey(idx))
		}
		seen[idx] = true
		if total += len(raw); total > MaxEditBytes {
			return nil, invalid("too much data")
		}
		if !utf8.ValidString(raw) || utf8.RuneCountInString(raw) > MaxCellChars {
			return nil, invalid("value too long or not text in " + cellKey(idx))
		}
		for _, r := range raw {
			if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0x7f || r == 0xFFFE || r == 0xFFFF {
				return nil, invalid("control character in " + cellKey(idx))
			}
		}
		ed := edit{idx: idx}
		switch {
		case raw == "":
		case raw[0] == '=':
			f, err := compile(raw, 0, 0)
			if err != nil {
				return nil, invalid("unsupported formula in " + cellKey(idx))
			}
			ed.f = f
		case raw[0] == '\'':
			ed.v = textv(raw[1:])
		default:
			if n, ok := numLiteral(raw); ok {
				ed.v = value{k: kNum, n: n}
			} else {
				ed.v = textv(raw)
			}
		}
		out = append(out, ed)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].idx < out[j].idx })
	return out, nil
}

// Apply writes the changed cells into the original workbook and returns
// the new file. Formulas are checked against the grammar, stored and
// computed here; the results are written as cached values and the workbook
// is marked for a full recalculation when opened in Excel.
func Apply(ctx context.Context, data []byte, e Edit) ([]byte, error) {
	edits, err := checkEdit(e)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var out []byte
	err = guard(ctx, len(data), func() error {
		d, err := load(ctx, data)
		if err != nil {
			return err
		}
		for _, ed := range edits {
			d.bk.set(ed.idx, ed.v, ed.f)
		}
		if d.bk.work > maxRangeWork {
			return invalid("formulas too complex")
		}
		if err := d.bk.evalAll(ctx); err != nil {
			return err
		}
		out, err = d.write(ctx, e, edits)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
