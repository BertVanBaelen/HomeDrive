import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'

// grid.ts imports './engine' without an extension, as the app does.
registerHooks({ resolve: (s, c, next) => next(s.startsWith('.') && !/\.\w+$/.test(s) ? s + '.ts' : s, c) })
const g = await import('../src/sheet/grid.ts')

const size = { rows: 20, cols: 8 }

test('move', () => {
  assert.deepEqual(g.move({ r: 0, c: 0 }, 'ArrowUp', size), { r: 0, c: 0 })
  assert.deepEqual(g.move({ r: 2, c: 2 }, 'Enter', size), { r: 3, c: 2 })
  assert.deepEqual(g.move({ r: 2, c: 2 }, 'Tab', size, false, true), { r: 2, c: 1 })
  assert.deepEqual(g.move({ r: 2, c: 2 }, 'ArrowDown', size, true), { r: 19, c: 2 })
  assert.deepEqual(g.move({ r: 2, c: 2 }, 'End', size, true), { r: 19, c: 7 })
  assert.deepEqual(g.move({ r: 2, c: 2 }, 'Home', size), { r: 2, c: 0 })
  assert.equal(g.move({ r: 2, c: 2 }, 'x', size), null)
})

test('tsv round trip', () => {
  const rows = [['a', 'b\tc', ''], ['"q"', 'line\nbreak', '1']]
  assert.deepEqual(g.parseTsv(g.toTsv(rows)), rows)
  assert.deepEqual(g.parseTsv('1\t2\r\n3\t4\r\n'), [['1', '2'], ['3', '4']])
  assert.deepEqual(g.parseTsv('a\rb'), [['a'], ['b']])
})

test('paste clips, grows and cleans', () => {
  const p = g.paste('=A1+\t=SUM(A1:A2)\n' + 'x'.repeat(2500) + '\tok\x07\t3', { r: 999, c: 50 }, size)
  assert.deepEqual(p.cells, { AY1000: "'=A1+", AZ1000: '=SUM(A1:A2)' })
  assert.deepEqual([p.rows, p.cols, p.end], [1000, 52, { r: 999, c: 51 }])
  const q = g.paste('x'.repeat(2500) + '\tok\x07', { r: 0, c: 0 }, size)
  assert.deepEqual([q.cells.A1.length, q.cells.B1, q.rows, q.cols], [2000, 'ok', 20, 8])
})

test('update, editOf and show', () => {
  const s = g.load({ rows: 20, cols: 8, cells: { A1: { raw: '2', v: '2.00' }, A2: { raw: '=A1*2', v: '4' } } })
  assert.deepEqual(s.shown.A1, { v: '2.00', look: 'num' })
  assert.equal(g.update(s, { A1: '2', C1: '' }, 20, 8, false), null)
  const t = g.update(s, { A1: '3' }, 21, 8, false)!
  assert.deepEqual([t.shown.A1.v, t.shown.A2.v, t.shown.A2.look], ['3', '6', 'num'])
  const u = g.update(t, { A2: '', B1: '=1/0' }, 21, 8, true)!
  assert.deepEqual(g.editOf(u), { rows: 21, cols: 8, cells: { A1: '3', A2: '', B1: '=1/0' } })
  assert.equal(u.shown.B1.look, 'err')
  assert.deepEqual(g.editOf(g.update(u, { C3: 'x' }, 21, 8, false)!).cells, { C3: 'x' })
  assert.deepEqual(g.cleared(u.raws, { r0: 0, c0: 0, r1: 0, c1: 0 }), { A1: '' })
})

test('csv copy', () => {
  const s = g.values({ name: 'S', rows: 2, cols: 3, cells: { A1: '=cmd', B1: '-5', C1: 'a,"b"', A2: '@x', B2: '-x' } })
  assert.equal(g.toCsv(s), '\uFEFF\'=cmd,-5,"a,""b"""\r\n\'@x,\'-x,\r\n')
})
