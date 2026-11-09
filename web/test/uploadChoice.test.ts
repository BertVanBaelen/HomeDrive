import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Choices } from '../src/uploadChoice.ts'

const items = [
  { key: 1, batch: 1, state: 'asking' },
  { key: 2, batch: 1, state: 'asking' },
  { key: 3, batch: 1, state: 'uploading' },
  { key: 4, batch: 2, state: 'asking' },
]
const keys = (l: { key: number }[]) => l.map((it) => it.key)

test('a choice applies to the upload asked about only', () => {
  const c = new Choices()
  assert.deepEqual(keys(c.answer(items, 2, 'replace', false)), [2])
  assert.equal(c.remembered(1), undefined)
})

test('do this for all settles the waiting batch and is remembered for it', () => {
  const c = new Choices()
  assert.deepEqual(keys(c.answer(items, 2, 'skip', true)), [1, 2])
  assert.equal(c.remembered(1), 'skip')
  assert.equal(c.remembered(2), undefined)
})

test('only an upload waiting for a choice can be answered', () => {
  const c = new Choices()
  assert.deepEqual(c.answer(items, 3, 'keep', true), [])
  assert.deepEqual(c.answer(items, 9, 'keep', true), [])
  assert.equal(c.remembered(1), undefined)
})
