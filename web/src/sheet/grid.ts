// Pure logic of the spreadsheet editor (editor/SheetEditor.tsx): moving
// around, clipboard text, what cells show, the edit saved and the CSV of
// Save as a copy (a browser can't write .xlsx).
import type { SheetEdit, SheetGrid, SheetView } from '../api'
import { cellKey, evaluate, MAX_CELL_CHARS, MAX_COLS, MAX_ROWS, parseRef, validate } from './engine'

export type Pos = { r: number; c: number }
export type Size = { rows: number; cols: number }
// Sel is a range: a is the active cell, b the opposite corner.
export type Sel = { a: Pos; b: Pos }
export type Rect = { r0: number; c0: number; r1: number; c1: number }
export type Look = '' | 'num' | 'err'
export type Shown = { v: string; look: Look }

// Snapshot is a sheet as edited, and the payload of the editor's session:
// the inputs, what they show, and the cells changed since the last save.
// loaded is the grid as read, base what the browser computes from it.
export interface Snapshot extends Size {
  raws: Record<string, string>
  shown: Record<string, Shown>
  keys: string[]
  loaded: SheetGrid['cells']
  base: Record<string, string>
}

const clamp = (n: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, n))
const clampPos = (p: Pos, s: Size): Pos => ({ r: clamp(p.r, 0, s.rows - 1), c: clamp(p.c, 0, s.cols - 1) })
export const clampSel = (sel: Sel, s: Size): Sel => ({ a: clampPos(sel.a, s), b: clampPos(sel.b, s) })
export const rectOf = ({ a, b }: Sel): Rect =>
  ({ r0: Math.min(a.r, b.r), c0: Math.min(a.c, b.c), r1: Math.max(a.r, b.r), c1: Math.max(a.c, b.c) })

const PAGE = 20
const STEPS: Record<string, [number, number]> = {
  ArrowUp: [-1, 0], ArrowDown: [1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1], PageUp: [-PAGE, 0], PageDown: [PAGE, 0],
  Home: [0, -MAX_COLS], End: [0, MAX_COLS], Enter: [1, 0], Tab: [0, 1],
}

// move gives where key takes the cell p (null: not a key that moves).
// Shift reverses Enter and Tab; jump (Ctrl) goes to the grid's edge, with
// Home / End to its first / last cell.
export function move(p: Pos, key: string, s: Size, jump = false, shift = false): Pos | null {
  const d = STEPS[key]
  if (!d) return null
  let [dr, dc] = shift && (key === 'Enter' || key === 'Tab') ? [-d[0], -d[1]] : d
  if (jump) [dr, dc] = key === 'Home' || key === 'End' ? [dc * MAX_ROWS, dc] : [dr * MAX_ROWS, dc * MAX_COLS]
  return clampPos({ r: p.r + dr, c: p.c + dc }, s)
}

// table gives f of each cell of a range, by row.
export function table({ r0, c0, r1, c1 }: Rect, f: (key: string) => string): string[][] {
  return Array.from({ length: r1 - r0 + 1 }, (_, i) => Array.from({ length: c1 - c0 + 1 }, (_, j) => f(cellKey(r0 + i, c0 + j))))
}

// cleared clears the cells of raws within a range.
export function cleared(raws: Record<string, string>, { r0, c0, r1, c1 }: Rect): Record<string, string> {
  const out: Record<string, string> = {}
  for (const k in raws) {
    const p = parseRef(k)
    if (p && p.row >= r0 && p.row <= r1 && p.col >= c0 && p.col <= c1) out[k] = ''
  }
  return out
}

const BAD_CHARS = /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f\ufffe\uffff]|[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g

// clean drops what the server refuses in a cell (control characters but
// tab and line breaks, broken UTF-16) and caps its length.
export function clean(s: string): string {
  s = s.replace(BAD_CHARS, '')
  return s.length > MAX_CELL_CHARS ? [...s].slice(0, MAX_CELL_CHARS).join('') : s
}

export const badFormula = (raw: string) => raw[0] === '=' && !validate(raw)

// Clipboard text is tab separated; a value with a tab, a line break or a
// leading quote is quoted, like spreadsheet apps do.
const FIELD = /"((?:[^"]|"")*)"(?=[\t\r\n]|$)|[^\t\r\n]*/y

export function parseTsv(text: string): string[][] {
  text = text.replace(/\r?\n$/, '')
  const rows: string[][] = [[]]
  for (let i = 0; ; ) {
    FIELD.lastIndex = i
    const m = FIELD.exec(text)!
    rows[rows.length - 1].push(m[1] === undefined ? m[0] : m[1].replace(/""/g, '"'))
    i = FIELD.lastIndex
    if (i >= text.length) return rows
    if (text[i] !== '\t') {
      if (text[i] === '\r' && text[i + 1] === '\n') i++
      rows.push([])
    }
    i++
  }
}

export const toTsv = (rows: string[][]) =>
  rows.map((r) => r.map((v) => (/[\t\r\n]|^"/.test(v) ? `"${v.replace(/"/g, '""')}"` : v)).join('\t')).join('\n')

// paste reads clipboard text into the cells from at on, within the grid's
// limits (the grid grows up to them). Values are cleaned; a formula that
// isn't valid is kept as text.
export function paste(text: string, at: Pos, s: Size) {
  const lines = parseTsv(text).slice(0, MAX_ROWS - at.r)
  const cells: Record<string, string> = {}
  let w = 1
  lines.forEach((line, i) =>
    line.slice(0, MAX_COLS - at.c).forEach((v, j) => {
      v = clean(v)
      cells[cellKey(at.r + i, at.c + j)] = badFormula(v) ? clean("'" + v) : v
      w = Math.max(w, j + 1)
    }),
  )
  const end = { r: at.r + lines.length - 1, c: at.c + w - 1 }
  return { cells, rows: Math.max(s.rows, end.r + 1), cols: Math.max(s.cols, end.c + 1), end }
}

const ERRORS = /^#(ERR|NULL!|DIV\/0!|VALUE!|REF!|NAME\?|NUM!|N\/A)$/
const NUM = /^-?\d+(\.\d+)?(E[+-]\d+)?$/ // as the engine shows numbers
const SHOWN_NUM = /^-?[\d.,]*\d(E[+-]\d+)?%?$/ // also with the file's formats

export const lookOf = (v: string, num = SHOWN_NUM.test(v)): Look => (ERRORS.test(v) ? 'err' : num ? 'num' : '')

// show computes what cells show: the server's value (with the file's number
// formats) while a cell and its result are as loaded, else the browser's.
export function show(raws: Record<string, string>, loaded: SheetGrid['cells'], base: Record<string, string>) {
  const out: Record<string, Shown> = {}
  for (const [k, v] of Object.entries(evaluate(raws))) {
    const l = loaded[k]
    out[k] = { v: l && l.raw === raws[k] && base[k] === v ? l.v : v, look: lookOf(v, raws[k][0] !== "'" && NUM.test(v)) }
  }
  return out
}

const fit = (s: Size): Size => ({ rows: clamp(s.rows, 1, MAX_ROWS), cols: clamp(s.cols, 1, MAX_COLS) })

export function load(g: SheetGrid): Snapshot {
  const loaded = g.cells ?? {}
  const raws = Object.fromEntries(Object.entries(loaded).map(([k, c]) => [k, c.raw]))
  const base = evaluate(raws)
  return { ...fit(g), raws, shown: show(raws, loaded, base), keys: [], loaded, base }
}

// values is a read-only sheet's snapshot.
export function values(sh: NonNullable<SheetView['sheets']>[number]): Snapshot {
  const shown: Record<string, Shown> = {}
  for (const [k, v] of Object.entries(sh.cells ?? {})) shown[k] = { v, look: lookOf(v) }
  return { ...fit(sh), raws: sh.cells ?? {}, shown, keys: [], loaded: {}, base: {} }
}

// update sets cells (inputs by key, "" clears) and the grid size; null when
// nothing changes. keep: the session still has changes it hasn't saved, so
// the cells changed before are sent again with these.
export function update(p: Snapshot, cells: Record<string, string>, rows: number, cols: number, keep: boolean): Snapshot | null {
  const raws = { ...p.raws }
  const keys = new Set(keep ? p.keys : [])
  let same = rows === p.rows && cols === p.cols
  for (const [k, v] of Object.entries(cells)) {
    if ((raws[k] ?? '') === v) continue
    same = false
    keys.add(k)
    if (v) raws[k] = v
    else delete raws[k]
  }
  return same ? null : { ...p, rows, cols, raws, keys: [...keys], shown: show(raws, p.loaded, p.base) }
}

export const editOf = (p: Snapshot): SheetEdit =>
  ({ rows: p.rows, cols: p.cols, cells: Object.fromEntries(p.keys.map((k) => [k, p.raws[k] ?? ''])) })

// A CSV value starting like a formula gets a quote, so that opening the copy
// runs nothing (CSV injection); plain negative numbers stay numbers.
function csvField(v: string): string {
  if (/^[=+\-@\t\r]/.test(v) && !SHOWN_NUM.test(v)) v = "'" + v
  return /[",\r\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v
}

// toCsv gives the values shown in the used range, for Save as a copy.
export function toCsv(p: Snapshot): string {
  let h = 0
  let w = 0
  for (const k in p.shown) {
    const at = parseRef(k)
    if (at) [h, w] = [Math.max(h, at.row + 1), Math.max(w, at.col + 1)]
  }
  const rows = table({ r0: 0, c0: 0, r1: h - 1, c1: w - 1 }, (k) => csvField(p.shown[k]?.v ?? ''))
  return '\uFEFF' + rows.map((r) => r.join(',') + '\r\n').join('')
}
