// Formula engine of the spreadsheet editor. Pure TypeScript, no imports, no
// DOM, no eval / Function. It is kept identical to the Go package
// server/internal/sheet (grammar, evaluation and number display); both run
// the shared vectors in server/internal/sheet/testdata/formulas.json.
//
//   formula := "=" expr
//   expr    := term (("+"|"-") term)*
//   term    := unary (("*"|"/") unary)*
//   unary   := ("-"|"+")* primary
//   primary := number | cell | func "(" arg ("," arg)* ")" | "(" expr ")"
//   arg     := expr | cell ":" cell          (ranges only as arguments)
//   cell    := "$"? A..AZ "$"? 1..1000        (any case)
//   func    := SUM | AVERAGE | MIN | MAX | COUNT
//   number  := digits ["." digits] | "." digits
//
// Whitespace (space, tab, CR, LF) may separate tokens. At most 500
// characters including "=", at most 32 nested brackets / calls.

export const MAX_ROWS = 1000;
export const MAX_COLS = 52;
export const MAX_CELL_CHARS = 2000; // characters of one cell input (the server refuses more)
export const ERR = "#ERR";

const MAX_FORMULA_CHARS = 500;
const MAX_DEPTH = 32;
const MAX_RANGE_WORK = 10_000_000; // referenced cells summed over all formulas
const N_CELLS = MAX_ROWS * MAX_COLS;
const FUNCS = new Map([["SUM", 1], ["AVERAGE", 2], ["MIN", 3], ["MAX", 4], ["COUNT", 5]]);

// k: "" end, "n" number, "c" cell, "f" function, or the punctuation itself;
// v: the number, the cell index or the function id.
type Tok = { k: string; v: number };
// op: "n" number, "c" cell, "r" range, "u" sign(s), "+", "-", "*", "/", "f" call.
type Node = { op: string; neg: boolean; v: number; a: number; b: number; kids: Node[] };
type Formula = { root: Node; refs: number[]; work: number }; // refs: pairs of corners
// k: 0 empty, 1 number, 2 text, 3 error.
type Val = { k: number; n: number; s: string };

const isDigit = (c: number) => c >= 48 && c <= 57;
const isLetter = (c: number) => (c | 0x20) >= 97 && (c | 0x20) <= 122;
const isSpace = (c: number) => c === 32 || c === 9 || c === 13 || c === 10;
const area = (a: number, b: number) =>
  (Math.floor(b / MAX_COLS) - Math.floor(a / MAX_COLS) + 1) * ((b % MAX_COLS) - (a % MAX_COLS) + 1);

/** 0 -> "A", 25 -> "Z", 26 -> "AA", 51 -> "AZ". */
export function colName(i: number): string {
  if (!Number.isInteger(i) || i < 0) return "";
  let s = "";
  for (let c = i + 1; c > 0; c = Math.floor((c - 1) / 26)) s = String.fromCharCode(65 + ((c - 1) % 26)) + s;
  return s;
}

/** Cell key for a 0-based position: (6, 1) -> "B7". */
export function cellKey(row: number, col: number): string {
  return colName(col) + String(row + 1);
}

// 1-3 upper-case letters -> 1-based column, 0 if invalid.
function colNumber(l: string): number {
  if (l.length === 0 || l.length > 3) return 0;
  let n = 0;
  for (let i = 0; i < l.length; i++) {
    const c = l.charCodeAt(i);
    if (c < 65 || c > 90) return 0;
    n = n * 26 + c - 64;
  }
  return n;
}

// Row number without leading zeros, 0 if invalid.
function digitsValue(d: string): number {
  if (d === "" || d[0] === "0" || d.length > 7) return 0;
  let n = 0;
  for (let i = 0; i < d.length; i++) {
    const c = d.charCodeAt(i);
    if (!isDigit(c)) return 0;
    n = n * 10 + c - 48;
  }
  return n;
}

/** "B7" -> { row: 6, col: 1 } (0-based, any case, no "$"); null if invalid or outside the grid. */
export function parseRef(ref: string): { row: number; col: number } | null {
  if (typeof ref !== "string" || ref.length > 12) return null;
  let j = 0;
  while (j < ref.length && isLetter(ref.charCodeAt(j))) j++;
  const col = colNumber(ref.slice(0, j).toUpperCase());
  const row = digitsValue(ref.slice(j));
  if (col < 1 || col > MAX_COLS || row < 1 || row > MAX_ROWS) return null;
  return { row: row - 1, col: col - 1 };
}

function tokenize(s: string): Tok[] | null {
  const toks: Tok[] = [];
  let i = 0;
  while (i < s.length) {
    const c = s.charCodeAt(i);
    if (isSpace(c)) {
      i++;
    } else if (isDigit(c) || c === 46) {
      let j = i;
      while (j < s.length && isDigit(s.charCodeAt(j))) j++;
      if (j < s.length && s.charCodeAt(j) === 46) {
        const k = ++j;
        while (j < s.length && isDigit(s.charCodeAt(j))) j++;
        if (j === k) return null;
      }
      const v = Number(s.slice(i, j));
      if (!Number.isFinite(v)) return null;
      toks.push({ k: "n", v });
      i = j;
    } else if (isLetter(c) || c === 36) {
      const absC = c === 36;
      let j = absC ? i + 1 : i;
      const k = j;
      while (j < s.length && isLetter(s.charCodeAt(j))) j++;
      const letters = s.slice(k, j).toUpperCase();
      if (letters === "") return null;
      if (!absC && (j === s.length || (s.charCodeAt(j) !== 36 && !isDigit(s.charCodeAt(j))))) {
        let m = j;
        while (m < s.length && isSpace(s.charCodeAt(m))) m++;
        const fn = FUNCS.get(letters);
        if (fn === undefined || m === s.length || s.charCodeAt(m) !== 40) return null;
        toks.push({ k: "f", v: fn });
        i = j;
        continue;
      }
      if (j < s.length && s.charCodeAt(j) === 36) j++;
      const d = j;
      while (j < s.length && isDigit(s.charCodeAt(j)) && j - d < 8) j++;
      const col = colNumber(letters);
      const row = digitsValue(s.slice(d, j));
      if (col < 1 || col > MAX_COLS || row < 1 || row > MAX_ROWS) return null;
      toks.push({ k: "c", v: (row - 1) * MAX_COLS + col - 1 });
      i = j;
    } else if ("+-*/(),:".indexOf(s[i]) >= 0) {
      toks.push({ k: s[i], v: 0 });
      i++;
    } else {
      return null;
    }
  }
  toks.push({ k: "", v: 0 });
  return toks;
}

const SYNTAX = new Error("unsupported formula");

function compile(raw: string): Formula | null {
  if (typeof raw !== "string" || raw.length > MAX_FORMULA_CHARS || raw.length < 2 || raw[0] !== "=") return null;
  const tk = tokenize(raw.slice(1));
  if (tk === null) return null;
  const toks: Tok[] = tk;
  const refs: number[] = [];
  let i = 0;
  let work = 0;
  const fail = (): never => {
    throw SYNTAX;
  };
  const peek = () => toks[i].k;
  const next = () => toks[i < toks.length - 1 ? i++ : i];
  const mk = (op: string, kids: Node[], v = 0, a = 0, b = 0): Node => ({ op, neg: false, v, a, b, kids });
  const ref = (a: number, b: number) => {
    refs.push(a, b);
    work += area(a, b);
  };
  function expr(d: number): Node {
    let l = term(d);
    while (peek() === "+" || peek() === "-") {
      const op = next().k;
      l = mk(op, [l, term(d)]);
    }
    return l;
  }
  function term(d: number): Node {
    let l = unary(d);
    while (peek() === "*" || peek() === "/") {
      const op = next().k;
      l = mk(op, [l, unary(d)]);
    }
    return l;
  }
  function unary(d: number): Node {
    let signs = 0;
    let neg = false;
    while (peek() === "+" || peek() === "-") {
      if (next().k === "-") neg = !neg;
      signs++;
    }
    const n = primary(d);
    if (signs === 0) return n;
    const u = mk("u", [n]);
    u.neg = neg;
    return u;
  }
  function primary(d: number): Node {
    const t = next();
    switch (t.k) {
      case "n":
        return mk("n", [], t.v);
      case "c":
        ref(t.v, t.v);
        return mk("c", [], 0, t.v);
      case "(": {
        if (d >= MAX_DEPTH) fail();
        const n = expr(d + 1);
        if (next().k !== ")") fail();
        return n;
      }
      case "f": {
        if (d >= MAX_DEPTH || next().k !== "(") fail();
        const n = mk("f", [], t.v);
        for (;;) {
          n.kids.push(arg(d + 1));
          const k = next().k;
          if (k === ")") return n;
          if (k !== ",") fail();
        }
      }
    }
    return fail();
  }
  function arg(d: number): Node {
    if (peek() === "c" && toks[i + 1].k === ":") {
      const a = next().v;
      next();
      const t = next();
      if (t.k !== "c") fail();
      const b = t.v;
      const r0 = Math.min(Math.floor(a / MAX_COLS), Math.floor(b / MAX_COLS));
      const r1 = Math.max(Math.floor(a / MAX_COLS), Math.floor(b / MAX_COLS));
      const c0 = Math.min(a % MAX_COLS, b % MAX_COLS);
      const c1 = Math.max(a % MAX_COLS, b % MAX_COLS);
      const n = mk("r", [], 0, r0 * MAX_COLS + c0, r1 * MAX_COLS + c1);
      ref(n.a, n.b);
      return n;
    }
    return expr(d);
  }
  try {
    const root = expr(0);
    return peek() === "" ? { root, refs, work } : null;
  } catch (e) {
    if (e === SYNTAX) return null;
    throw e;
  }
}

/** Grammar check of a formula input ("=..."). */
export function validate(raw: string): boolean {
  return compile(raw) !== null;
}

// A plain number as typed: optional ASCII whitespace and sign, digits with an
// optional fraction and exponent ("1e3", ".5", "5."); finite only.
function numLiteral(raw: string): number | null {
  let a = 0;
  let b = raw.length;
  while (a < b && isSpace(raw.charCodeAt(a))) a++;
  while (b > a && isSpace(raw.charCodeAt(b - 1))) b--;
  const s = raw.slice(a, b);
  let i = 0;
  let d = 0;
  if (s[i] === "+" || s[i] === "-") i++;
  for (; i < s.length && isDigit(s.charCodeAt(i)); i++) d++;
  if (s[i] === ".") for (i++; i < s.length && isDigit(s.charCodeAt(i)); i++) d++;
  if (d === 0) return null;
  if (s[i] === "e" || s[i] === "E") {
    i++;
    if (s[i] === "+" || s[i] === "-") i++;
    let e = 0;
    for (; i < s.length && isDigit(s.charCodeAt(i)); i++) e++;
    if (e === 0) return null;
  }
  if (i !== s.length) return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

// Inputs the server refuses: too long, control characters, broken UTF-16.
function badInput(s: string): boolean {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c >= 0xd800 && c <= 0xdbff) {
      const lo = s.charCodeAt(i + 1);
      if (!(lo >= 0xdc00 && lo <= 0xdfff)) return true;
      i++;
    } else if ((c >= 0xdc00 && c <= 0xdfff) || (c < 0x20 && c !== 9 && c !== 10 && c !== 13) || c === 0x7f || c === 0xfffe || c === 0xffff) {
      return true;
    }
    if (++n > MAX_CELL_CHARS) return true;
  }
  return false;
}

/**
 * Number display like Excel's "General" format (same as Go FormatNumber):
 * the shortest round-trip digits rounded half-up to 15 significant digits,
 * trailing zeros dropped; decimal exponents -9..14 in plain notation, others
 * as d.dddE+XX / d.dddE-XX. -0 shows "0", non-finite values "#ERR".
 */
export function formatNumber(n: number): string {
  if (!Number.isFinite(n)) return ERR;
  if (n === 0) return "0";
  const s = Math.abs(n).toExponential();
  const e = s.indexOf("e");
  let exp = Number(s.slice(e + 1));
  const d: number[] = [];
  for (let i = 0; i < e; i++) if (s[i] !== ".") d.push(s.charCodeAt(i) - 48);
  if (d.length > 15) {
    const up = d[15] >= 5;
    d.length = 15;
    if (up) {
      let j = 14;
      for (; j >= 0 && d[j] === 9; j--) d[j] = 0;
      if (j < 0) {
        d.unshift(1);
        d.length = 15;
        exp++;
      } else {
        d[j]++;
      }
    }
  }
  while (d.length > 1 && d[d.length - 1] === 0) d.pop();
  const digits = d.join("");
  let out: string;
  if (exp > 14 || exp < -9) {
    out = digits[0] + (digits.length > 1 ? "." + digits.slice(1) : "") + "E" + (exp < 0 ? "-" : "+") + String(Math.abs(exp)).padStart(2, "0");
  } else if (exp < 0) {
    out = "0." + "0".repeat(-exp - 1) + digits;
  } else if (digits.length <= exp + 1) {
    out = digits + "0".repeat(exp + 1 - digits.length);
  } else {
    out = digits.slice(0, exp + 1) + "." + digits.slice(exp + 1);
  }
  return n < 0 ? "-" + out : out;
}

const ERRV: Val = { k: 3, n: 0, s: "" };
const numv = (x: number): Val => (Number.isFinite(x) ? { k: 1, n: x, s: "" } : ERRV);
// Arithmetic operand: empty -> 0, text -> error.
const toNum = (v: Val): Val => (v.k === 0 ? { k: 1, n: 0, s: "" } : v.k === 2 ? ERRV : v);

/**
 * Computes the shown value of every non-empty cell. raws maps a cell key
 * ("B7", any case) to the input: a number, text ("'" forces text), or a
 * "=formula". Errors, invalid formulas or keys, inputs the server would
 * refuse and circular references show "#ERR"; errors propagate to every
 * dependent. Results are keyed like the input.
 */
export function evaluate(raws: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  const kind = new Uint8Array(N_CELLS);
  const nums = new Float64Array(N_CELLS);
  const texts: string[] = [];
  const forms: (Formula | undefined)[] = [];
  const keys: [string, number][] = [];
  let work = 0;
  for (const key of Object.keys(raws)) {
    const raw = raws[key];
    const p = parseRef(key);
    if (typeof raw !== "string" || p === null) {
      if (raw !== "") out[key] = ERR;
      continue;
    }
    const idx = p.row * MAX_COLS + p.col;
    keys.push([key, idx]);
    const old = forms[idx];
    if (old) work -= old.work;
    forms[idx] = undefined;
    kind[idx] = 0;
    if (raw === "") continue;
    if (badInput(raw)) {
      kind[idx] = 3;
    } else if (raw[0] === "=") {
      const f = compile(raw);
      if (f === null) kind[idx] = 3;
      else {
        forms[idx] = f;
        work += f.work;
      }
    } else if (raw[0] === "'") {
      if (raw.length > 1) {
        kind[idx] = 2;
        texts[idx] = raw.slice(1);
      }
    } else {
      const n = numLiteral(raw);
      if (n !== null) {
        kind[idx] = 1;
        nums[idx] = n;
      } else {
        kind[idx] = 2;
        texts[idx] = raw;
      }
    }
  }

  const ev = (n: Node): Val => {
    switch (n.op) {
      case "n":
        return { k: 1, n: n.v, s: "" };
      case "c":
        return { k: kind[n.a], n: nums[n.a], s: texts[n.a] ?? "" };
      case "u": {
        const x = toNum(ev(n.kids[0]));
        return x.k === 1 && n.neg ? { k: 1, n: -x.n, s: "" } : x;
      }
      case "f":
        return call(n);
    }
    const l = toNum(ev(n.kids[0]));
    if (l.k === 3) return l;
    const r = toNum(ev(n.kids[1]));
    if (r.k === 3) return r;
    switch (n.op) {
      case "+":
        return numv(l.n + r.n);
      case "-":
        return numv(l.n - r.n);
      case "*":
        return numv(l.n * r.n);
    }
    return r.n === 0 ? ERRV : numv(l.n / r.n);
  };

  // SUM/AVERAGE/MIN/MAX/COUNT: cell and range arguments skip empty and text
  // cells; any error propagates.
  const call = (n: Node): Val => {
    let sum = 0;
    let lo = 0;
    let hi = 0;
    let cnt = 0;
    const add = (x: number) => {
      if (cnt === 0 || x < lo) lo = x;
      if (cnt === 0 || x > hi) hi = x;
      sum += x;
      cnt++;
    };
    for (const a of n.kids) {
      if (a.op === "c" || a.op === "r") {
        const z = a.op === "r" ? a.b : a.a;
        for (let r = Math.floor(a.a / MAX_COLS); r <= Math.floor(z / MAX_COLS); r++) {
          for (let c = a.a % MAX_COLS; c <= z % MAX_COLS; c++) {
            const i = r * MAX_COLS + c;
            if (kind[i] === 1) add(nums[i]);
            else if (kind[i] === 3) return ERRV;
          }
        }
        continue;
      }
      const v = toNum(ev(a));
      if (v.k === 3) return v;
      add(v.n);
    }
    switch (n.v) {
      case 1:
        return numv(sum);
      case 2:
        return cnt === 0 ? ERRV : numv(sum / cnt);
      case 3:
        return numv(lo);
      case 4:
        return numv(hi);
    }
    return numv(cnt);
  };

  if (work > MAX_RANGE_WORK) {
    for (let i = 0; i < N_CELLS; i++) if (forms[i]) kind[i] = 3;
  } else {
    // Dependencies first, with an explicit stack (no recursion, so a chain
    // through every cell is fine). Meeting a cell that is still in progress
    // means a cycle: that cell becomes an error, which then propagates.
    const state = new Uint8Array(N_CELLS); // 0 new, 1 in progress, 2 done
    const stack: { cell: number; ref: number; pos: number; cyc: boolean }[] = [];
    for (let start = 0; start < N_CELLS; start++) {
      if (!forms[start] || state[start] !== 0) continue;
      state[start] = 1;
      stack.push({ cell: start, ref: 0, pos: 0, cyc: false });
      frames: while (stack.length > 0) {
        const fr = stack[stack.length - 1];
        const f = forms[fr.cell] as Formula;
        for (; fr.ref < f.refs.length; fr.ref += 2, fr.pos = 0) {
          const a = f.refs[fr.ref];
          const z = f.refs[fr.ref + 1];
          const w = (z % MAX_COLS) - (a % MAX_COLS) + 1;
          for (const n = area(a, z); fr.pos < n; ) {
            const i = a + Math.floor(fr.pos / w) * MAX_COLS + (fr.pos % w);
            fr.pos++;
            if (!forms[i]) continue;
            if (state[i] === 1) {
              fr.cyc = true;
            } else if (state[i] === 0) {
              state[i] = 1;
              stack.push({ cell: i, ref: 0, pos: 0, cyc: false });
              continue frames;
            }
          }
        }
        let v = fr.cyc ? ERRV : ev(f.root);
        if (v.k === 0) v = { k: 1, n: 0, s: "" };
        kind[fr.cell] = v.k;
        nums[fr.cell] = v.n;
        texts[fr.cell] = v.s;
        state[fr.cell] = 2;
        stack.pop();
      }
    }
  }

  for (const [key, i] of keys) {
    if (kind[i] === 1) out[key] = formatNumber(nums[i]);
    else if (kind[i] === 2) out[key] = texts[i];
    else if (kind[i] === 3) out[key] = ERR;
  }
  return out;
}
