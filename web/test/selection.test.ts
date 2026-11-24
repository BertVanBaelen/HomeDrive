import assert from 'node:assert/strict'
import { test } from 'node:test'
import { selectionToggle } from '../src/selection.ts'

const items = ['a', 'b', 'c', 'd', 'e'].map((id) => ({ id }))
const ids = (s: Set<string>) => [...s].sort().join('')

test('a tap adds or removes one item', () => {
  let s = selectionToggle(new Set(), items, null, 'b', false)
  assert.equal(ids(s), 'b')
  s = selectionToggle(s, items, 'b', 'd', false)
  assert.equal(ids(s), 'bd')
  s = selectionToggle(s, items, 'd', 'b', false)
  assert.equal(ids(s), 'd')
})

test('Shift selects from the anchor, in either direction, keeping what was selected', () => {
  assert.equal(ids(selectionToggle(new Set(['a']), items, 'b', 'd', true)), 'abcd')
  assert.equal(ids(selectionToggle(new Set(), items, 'e', 'c', true)), 'cde')
})

test('Shift without an anchor, or with one that went away, is a tap', () => {
  assert.equal(ids(selectionToggle(new Set(), items, null, 'c', true)), 'c')
  assert.equal(ids(selectionToggle(new Set(['c']), items, 'gone', 'c', true)), '')
})

test('the selection given is not changed', () => {
  const before = new Set(['a'])
  selectionToggle(before, items, 'a', 'c', true)
  assert.equal(ids(before), 'a')
})

test('the selection never exceeds what a batch or zip request accepts', () => {
  const many = Array.from({ length: 550 }, (_, i) => ({ id: String(i).padStart(3, '0') }))
  let selected = new Set<string>()
  for (const item of many) selected = selectionToggle(selected, many, null, item.id, false)
  assert.equal(selected.size, 500)
  assert.ok(!selected.has('549'))
  assert.ok(selected.has('499'))
})
