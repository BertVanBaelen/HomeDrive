import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clampPan, clampZoom, fit, zoomAtPoint } from '../src/zoom.ts'

test('zoom is clamped and stays finite', () => {
  assert.equal(clampZoom(-1), 1)
  assert.equal(clampZoom(10), 3)
  assert.equal(clampZoom(Number.NaN), 1)
  assert.equal(clampZoom(Number.POSITIVE_INFINITY), 1)
})

test('the point zoomed at stays where it is', () => {
  const at = { x: 120, y: -40 }
  // The picture point under `at` is (at - offset) / scale, before and after.
  const under = (v: { scale: number; x: number; y: number }) => ({ x: (at.x - v.x) / v.scale, y: (at.y - v.y) / v.scale })
  let v = fit
  for (const s of [2, 2.5, 1.5, 3, 1.2]) {
    const before = under(v)
    v = zoomAtPoint(v, s, at)
    const after = under(v)
    assert.ok(Math.abs(before.x - after.x) < 1e-9 && Math.abs(before.y - after.y) < 1e-9, `drift at ${s}`)
  }
})

test('zooming in and out at the same point comes back to fit', () => {
  const at = { x: 75, y: 30 }
  const back = zoomAtPoint(zoomAtPoint(fit, 2.4, at), 1, at)
  assert.deepEqual(clampPan(back, { w: 400, h: 300 }, { w: 400, h: 600 }), fit)
})

test('a zoomed picture covers the stage where it is larger, and stays centred where it is not', () => {
  // A wide photo: fills the stage's width at fit, a band above and below.
  const pic = { w: 800, h: 400 }
  const stage = { w: 800, h: 900 }
  // 2×: 1600×800, wider than the stage (moves 400 each way), still lower.
  assert.deepEqual(clampPan({ scale: 2, x: 999, y: -999 }, pic, stage), { scale: 2, x: 400, y: 0 })
  assert.deepEqual(clampPan({ scale: 2, x: -50, y: 20 }, pic, stage), { scale: 2, x: -50, y: 0 })
  // 3×: 2400×1200, taller than the stage: no empty band at the top or bottom.
  assert.deepEqual(clampPan({ scale: 3, x: 0, y: 500 }, pic, stage), { scale: 3, x: 0, y: 150 })
  assert.deepEqual(clampPan({ scale: 1, x: 50, y: 20 }, pic, stage), fit)
})
