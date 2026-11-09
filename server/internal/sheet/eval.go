package sheet

import (
	"context"
	"math"
)

type kind uint8

const (
	kEmpty kind = iota
	kNum
	kText
	kErr
)

const (
	errDiv0  = "#DIV/0!"
	errValue = "#VALUE!"
	errNum   = "#NUM!"
	errCycle = "#REF!" // circular reference
)

type value struct {
	k kind
	n float64
	s string // text, or the Excel error code
}

func errv(code string) value { return value{k: kErr, s: code} }

func numv(x float64) value {
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return errv(errNum)
	}
	return value{k: kNum, n: x}
}

// num converts for arithmetic: empty -> 0, text -> #VALUE!.
func num(v value) value {
	switch v.k {
	case kEmpty:
		return value{k: kNum}
	case kText:
		return errv(errValue)
	}
	return v
}

// display is the value as shown in the General format.
func (v value) display() string {
	switch v.k {
	case kNum:
		return FormatNumber(v.n)
	case kText, kErr:
		return v.s
	}
	return ""
}

const nCells = MaxRows * MaxCols

// book is a dense MaxRows x MaxCols model: literal values and formulas.
type book struct {
	vals  []value
	forms []*formula
	work  int // sum of formula work, bounded by maxRangeWork
}

func newBook() *book { return &book{vals: make([]value, nCells), forms: make([]*formula, nCells)} }

func (b *book) set(i int, v value, f *formula) {
	if old := b.forms[i]; old != nil {
		b.work -= old.work
	}
	b.vals[i], b.forms[i] = v, f
	if f != nil {
		b.work += f.work
	}
}

// evalAll computes every formula cell. Dependencies are visited with an
// explicit stack (no recursion, so a 52000-long chain is fine); a reference
// to a cell that is still being computed marks a cycle. Every cell of a
// cycle and everything depending on it ends up as an error (errors always
// propagate). Work is bounded by maxRangeWork (checked by the callers).
func (b *book) evalAll(ctx context.Context) error {
	type frame struct {
		cell, ref, pos int
		cyc            bool
	}
	state := make([]uint8, nCells) // 0 new, 1 in progress, 2 done
	var stack []frame
	steps := 0
	for start := range b.forms {
		if b.forms[start] == nil || state[start] != 0 {
			continue
		}
		state[start] = 1
		stack = append(stack[:0], frame{cell: start})
	next:
		for len(stack) > 0 {
			fr := &stack[len(stack)-1]
			refs := b.forms[fr.cell].refs
			for ; fr.ref < len(refs); fr.ref, fr.pos = fr.ref+1, 0 {
				a, z := refs[fr.ref][0], refs[fr.ref][1]
				w := z%MaxCols - a%MaxCols + 1
				for n := area(a, z); fr.pos < n; {
					i := a + (fr.pos/w)*MaxCols + fr.pos%w
					fr.pos++
					if steps++; steps&0xffff == 0 && ctx.Err() != nil {
						return ctx.Err()
					}
					if b.forms[i] == nil {
						continue
					}
					switch state[i] {
					case 1:
						fr.cyc = true
					case 0:
						state[i] = 1
						stack = append(stack, frame{cell: i})
						continue next
					}
				}
			}
			v := errv(errCycle)
			if !fr.cyc {
				v = b.eval(b.forms[fr.cell].root)
				if v.k == kEmpty {
					v = value{k: kNum}
				}
			}
			b.vals[fr.cell] = v
			state[fr.cell] = 2
			stack = stack[:len(stack)-1]
		}
	}
	return nil
}

func (b *book) eval(n *node) value {
	switch n.op {
	case 'n':
		return value{k: kNum, n: n.num}
	case 'c':
		return b.vals[n.a]
	case 'u':
		x := num(b.eval(n.kids[0]))
		if x.k == kNum && n.neg {
			x.n = -x.n
		}
		return x
	case 'f':
		return b.call(n)
	}
	l := num(b.eval(n.kids[0]))
	if l.k == kErr {
		return l
	}
	r := num(b.eval(n.kids[1]))
	if r.k == kErr {
		return r
	}
	switch n.op {
	case '+':
		return numv(l.n + r.n)
	case '-':
		return numv(l.n - r.n)
	case '*':
		return numv(l.n * r.n)
	}
	if r.n == 0 {
		return errv(errDiv0)
	}
	return numv(l.n / r.n)
}

// call evaluates SUM/AVERAGE/MIN/MAX/COUNT. Cell and range arguments skip
// empty and text cells; any error (in a cell or an argument) propagates.
func (b *book) call(n *node) value {
	var sum, lo, hi float64
	cnt := 0
	add := func(x float64) {
		if cnt == 0 || x < lo {
			lo = x
		}
		if cnt == 0 || x > hi {
			hi = x
		}
		sum += x
		cnt++
	}
	for _, a := range n.kids {
		if a.op == 'c' || a.op == 'r' {
			z := a.a
			if a.op == 'r' {
				z = a.b
			}
			for r := a.a / MaxCols; r <= z/MaxCols; r++ {
				for c := a.a % MaxCols; c <= z%MaxCols; c++ {
					switch v := b.vals[r*MaxCols+c]; v.k {
					case kNum:
						add(v.n)
					case kErr:
						return v
					}
				}
			}
			continue
		}
		v := num(b.eval(a))
		if v.k == kErr {
			return v
		}
		add(v.n)
	}
	switch n.fn {
	case fnSum:
		return numv(sum)
	case fnAverage:
		if cnt == 0 {
			return errv(errDiv0)
		}
		return numv(sum / float64(cnt))
	case fnMin:
		return numv(lo)
	case fnMax:
		return numv(hi)
	}
	return numv(float64(cnt))
}
