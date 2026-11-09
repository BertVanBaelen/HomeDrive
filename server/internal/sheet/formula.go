package sheet

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// The formula grammar (kept identical to web/src/sheet/engine.ts):
//
//	formula := "=" expr
//	expr    := term (("+"|"-") term)*
//	term    := unary (("*"|"/") unary)*
//	unary   := ("-"|"+")* primary
//	primary := number | cell | func "(" arg ("," arg)* ")" | "(" expr ")"
//	arg     := expr | cell ":" cell
//	cell    := "$"? A..AZ "$"? 1..1000
//	func    := SUM | AVERAGE | MIN | MAX | COUNT
//	number  := digits ["." digits] | "." digits
//
// Whitespace (space, tab, CR, LF) is allowed between tokens. Anything else
// (strings, other operators, functions, names, sheet or external
// references, non-ASCII) is rejected, so a stored formula can never reach
// outside this tiny language. Formulas are stored in a canonical form:
// upper case, no whitespace.

const (
	MaxFormulaChars = 500 // whole raw input including the leading "="
	MaxDepth        = 32  // nesting of brackets and function calls
	maxRangeWork    = 10_000_000
)

const (
	fnSum = iota + 1
	fnAverage
	fnMin
	fnMax
	fnCount
)

var funcs = map[string]int{"SUM": fnSum, "AVERAGE": fnAverage, "MIN": fnMin, "MAX": fnMax, "COUNT": fnCount}

var errSyntax = errors.New("unsupported formula")

// node is a parsed expression. op: 'n' number, 'c' cell, 'r' range (only
// as a function argument), 'u' unary sign(s), '+', '-', '*', '/', 'f' call.
type node struct {
	op   byte
	neg  bool
	fn   int
	num  float64
	a, b int // cell index (a) or range corners (a top-left, b bottom-right)
	kids []*node
}

type formula struct {
	root *node
	text string   // canonical text without "="
	refs [][2]int // every referenced rectangle (a single cell has a == b)
	work int      // total number of referenced cells
}

type token struct {
	k    byte // 0 end, 'n', 'c', 'f', or the operator/punctuation itself
	text string
	num  float64
	cell int
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }
func isSpace(c byte) bool  { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// tokenize splits s (without "="); dRow/dCol shift relative references
// (used to expand shared formulas read from a file).
func tokenize(s string, dRow, dCol int) ([]token, error) {
	var toks []token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case isSpace(c):
			i++
		case isDigit(c) || c == '.':
			j := i
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			if j < len(s) && s[j] == '.' {
				j++
				k := j
				for j < len(s) && isDigit(s[j]) {
					j++
				}
				if j == k {
					return nil, errSyntax
				}
			}
			text := s[i:j]
			if text[0] == '.' {
				text = "0" + text
			}
			f, err := strconv.ParseFloat(text, 64)
			if math.IsInf(f, 0) || (err != nil && !errors.Is(err, strconv.ErrRange)) {
				return nil, errSyntax
			}
			toks = append(toks, token{k: 'n', text: text, num: f})
			i = j
		case isLetter(c) || c == '$':
			j := i
			absC := s[j] == '$'
			if absC {
				j++
			}
			k := j
			for j < len(s) && isLetter(s[j]) {
				j++
			}
			letters := strings.ToUpper(s[k:j])
			if letters == "" {
				return nil, errSyntax
			}
			if !absC && (j == len(s) || (s[j] != '$' && !isDigit(s[j]))) {
				m := j
				for m < len(s) && isSpace(s[m]) {
					m++
				}
				fn, ok := funcs[letters]
				if !ok || m == len(s) || s[m] != '(' {
					return nil, errSyntax
				}
				toks = append(toks, token{k: 'f', text: letters, cell: fn})
				i = j
				continue
			}
			absR := j < len(s) && s[j] == '$'
			if absR {
				j++
			}
			k = j
			for j < len(s) && isDigit(s[j]) && j-k < 8 {
				j++
			}
			col, row := colNumber(letters), digitsValue(s[k:j])
			if col < 1 || row < 1 {
				return nil, errSyntax
			}
			if !absC {
				col += dCol
			}
			if !absR {
				row += dRow
			}
			if col < 1 || col > MaxCols || row < 1 || row > MaxRows {
				return nil, errSyntax
			}
			text := colName(col-1) + strconv.Itoa(row)
			if absR {
				text = colName(col-1) + "$" + strconv.Itoa(row)
			}
			if absC {
				text = "$" + text
			}
			toks = append(toks, token{k: 'c', text: text, cell: (row-1)*MaxCols + col - 1})
			i = j
		case strings.IndexByte("+-*/(),:", c) >= 0:
			toks = append(toks, token{k: c, text: s[i : i+1]})
			i++
		default:
			return nil, errSyntax
		}
	}
	return append(toks, token{}), nil
}

// colNumber converts 1-3 upper-case letters to a 1-based column, 0 if invalid.
func colNumber(l string) int {
	if len(l) == 0 || len(l) > 3 {
		return 0
	}
	n := 0
	for i := 0; i < len(l); i++ {
		if l[i] < 'A' || l[i] > 'Z' {
			return 0
		}
		n = n*26 + int(l[i]-'A') + 1
	}
	return n
}

// digitsValue parses a row number without leading zeros, 0 if invalid.
func digitsValue(d string) int {
	if d == "" || d[0] == '0' || len(d) > 7 {
		return 0
	}
	n := 0
	for i := 0; i < len(d); i++ {
		if !isDigit(d[i]) {
			return 0
		}
		n = n*10 + int(d[i]-'0')
	}
	return n
}

type parser struct {
	toks []token
	i    int
	refs [][2]int
	work int
}

func (p *parser) peek() byte       { return p.toks[p.i].k }
func (p *parser) next() token      { t := p.toks[p.i]; p.i++; return t }
func (p *parser) want(k byte) bool { return p.next().k == k }
func (p *parser) ref(a, b int)     { p.refs = append(p.refs, [2]int{a, b}); p.work += area(a, b) }
func area(a, b int) int            { return (b/MaxCols - a/MaxCols + 1) * (b%MaxCols - a%MaxCols + 1) }

func (p *parser) expr(d int) (*node, error) {
	l, err := p.term(d)
	for err == nil && (p.peek() == '+' || p.peek() == '-') {
		op := p.next().k
		var r *node
		if r, err = p.term(d); err == nil {
			l = &node{op: op, kids: []*node{l, r}}
		}
	}
	return l, err
}

func (p *parser) term(d int) (*node, error) {
	l, err := p.unary(d)
	for err == nil && (p.peek() == '*' || p.peek() == '/') {
		op := p.next().k
		var r *node
		if r, err = p.unary(d); err == nil {
			l = &node{op: op, kids: []*node{l, r}}
		}
	}
	return l, err
}

func (p *parser) unary(d int) (*node, error) {
	signs, neg := 0, false
	for p.peek() == '+' || p.peek() == '-' {
		if p.next().k == '-' {
			neg = !neg
		}
		signs++
	}
	n, err := p.primary(d)
	if err != nil || signs == 0 {
		return n, err
	}
	return &node{op: 'u', neg: neg, kids: []*node{n}}, nil
}

func (p *parser) primary(d int) (*node, error) {
	t := p.next()
	switch t.k {
	case 'n':
		return &node{op: 'n', num: t.num}, nil
	case 'c':
		p.ref(t.cell, t.cell)
		return &node{op: 'c', a: t.cell}, nil
	case '(':
		if d >= MaxDepth {
			return nil, errSyntax
		}
		n, err := p.expr(d + 1)
		if err != nil || !p.want(')') {
			return nil, errSyntax
		}
		return n, nil
	case 'f':
		if d >= MaxDepth || !p.want('(') {
			return nil, errSyntax
		}
		n := &node{op: 'f', fn: t.cell}
		for {
			a, err := p.arg(d + 1)
			if err != nil {
				return nil, err
			}
			n.kids = append(n.kids, a)
			if k := p.next().k; k == ')' {
				return n, nil
			} else if k != ',' {
				return nil, errSyntax
			}
		}
	}
	return nil, errSyntax
}

func (p *parser) arg(d int) (*node, error) {
	if p.peek() == 'c' && p.toks[p.i+1].k == ':' {
		a := p.next().cell
		p.next()
		t := p.next()
		if t.k != 'c' {
			return nil, errSyntax
		}
		b := t.cell
		r0, r1 := min(a/MaxCols, b/MaxCols), max(a/MaxCols, b/MaxCols)
		c0, c1 := min(a%MaxCols, b%MaxCols), max(a%MaxCols, b%MaxCols)
		n := &node{op: 'r', a: r0*MaxCols + c0, b: r1*MaxCols + c1}
		p.ref(n.a, n.b)
		return n, nil
	}
	return p.expr(d)
}

// compile parses raw ("=..."); dRow/dCol shift relative references.
func compile(raw string, dRow, dCol int) (*formula, error) {
	if len(raw) > MaxFormulaChars || len(raw) < 2 || raw[0] != '=' {
		return nil, errSyntax
	}
	toks, err := tokenize(raw[1:], dRow, dCol)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	root, err := p.expr(0)
	if err != nil || p.peek() != 0 {
		return nil, errSyntax
	}
	var sb strings.Builder
	for _, t := range toks {
		sb.WriteString(t.text)
	}
	return &formula{root: root, text: sb.String(), refs: p.refs, work: p.work}, nil
}

// colName converts a 0-based column index to letters: 0 -> "A", 51 -> "AZ".
func colName(c int) string {
	var b [8]byte
	i := len(b)
	for c++; c > 0 && i > 0; c = (c - 1) / 26 {
		i--
		b[i] = byte('A' + (c-1)%26)
	}
	return string(b[i:])
}

func cellKey(idx int) string { return colName(idx%MaxCols) + strconv.Itoa(idx/MaxCols+1) }

// parseCellName parses an A1 reference as found in a file (any column up
// to XFD, any row up to 1048576, any case, no "$"), 1-based.
func parseCellName(s string) (col, row int, ok bool) {
	j := 0
	for j < len(s) && isLetter(s[j]) {
		j++
	}
	col, row = colNumber(strings.ToUpper(s[:j])), digitsValue(s[j:])
	return col, row, col >= 1 && col <= 16384 && row >= 1 && row <= 1048576
}

// parseKey parses an Edit key like "B7" into a cell index inside the limits.
func parseKey(s string) (int, bool) {
	c, r, ok := parseCellName(s)
	if !ok || c > MaxCols || r > MaxRows {
		return 0, false
	}
	return (r-1)*MaxCols + c - 1, true
}

// numLiteral reports whether s (trimmed of ASCII whitespace) is a plain
// number: [+-]digits[.digits][e[+-]digits] (also ".5", "5."), finite.
func numLiteral(s string) (float64, bool) {
	s = strings.Trim(s, " \t\r\n")
	i, d := 0, 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	for ; i < len(s) && isDigit(s[i]); i++ {
		d++
	}
	if i < len(s) && s[i] == '.' {
		for i++; i < len(s) && isDigit(s[i]); i++ {
			d++
		}
	}
	if d == 0 {
		return 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		e := 0
		for ; i < len(s) && isDigit(s[i]); i++ {
			e++
		}
		if e == 0 {
			return 0, false
		}
	}
	if i != len(s) {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if math.IsInf(f, 0) || (err != nil && !errors.Is(err, strconv.ErrRange)) {
		return 0, false
	}
	return f, true
}

// FormatNumber renders a number like Excel's "General" format (and exactly
// like formatNumber in web/src/sheet/engine.ts): the shortest round-trip
// decimal digits are rounded half-up to 15 significant digits, trailing
// zeros dropped; decimal exponents -9..14 are written in plain notation,
// anything else as d.dddE+XX / d.dddE-XX (at least two exponent digits).
// -0 shows as "0"; non-finite values (never stored) show as "#NUM!".
func FormatNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return errNum
	}
	if f == 0 {
		return "0"
	}
	s := strconv.FormatFloat(math.Abs(f), 'e', -1, 64)
	e := strings.IndexByte(s, 'e')
	exp, _ := strconv.Atoi(s[e+1:])
	digits := []byte(strings.Replace(s[:e], ".", "", 1))
	if len(digits) > 15 {
		up := digits[15] >= '5'
		digits = digits[:15]
		if up {
			j := 14
			for ; j >= 0 && digits[j] == '9'; j-- {
				digits[j] = '0'
			}
			if j < 0 {
				digits = append([]byte{'1'}, digits[:14]...)
				exp++
			} else {
				digits[j]++
			}
		}
	}
	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}
	var out string
	switch {
	case exp > 14 || exp < -9:
		out = string(digits[:1])
		if len(digits) > 1 {
			out += "." + string(digits[1:])
		}
		sign, x := "+", exp
		if x < 0 {
			sign, x = "-", -x
		}
		xs := strconv.Itoa(x)
		if len(xs) < 2 {
			xs = "0" + xs
		}
		out += "E" + sign + xs
	case exp < 0:
		out = "0." + strings.Repeat("0", -exp-1) + string(digits)
	case len(digits) <= exp+1:
		out = string(digits) + strings.Repeat("0", exp+1-len(digits))
	default:
		out = string(digits[:exp+1]) + "." + string(digits[exp+1:])
	}
	if f < 0 {
		out = "-" + out
	}
	return out
}

// canonNum writes a number for the file (xsd:double), round-trip exact.
func canonNum(f float64) string {
	if f == 0 {
		return "0"
	}
	if a := math.Abs(f); a >= 1e-6 && a < 1e15 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'E', -1, 64)
}
