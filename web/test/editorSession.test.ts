import assert from 'node:assert/strict'
import { test } from 'node:test'
import { marked } from 'marked'
import { linkHref, markdownText, pageUnsafe, readText, safeUrl, sameMarkdown, Session, writeText, type MdToken, type SessionIO } from '../src/editor/session.ts'

test('line endings: the dominant one is kept, a byte order mark too', () => {
  for (const raw of ['a\r\nb\r\n', 'a\nb\n', 'a\r\nb\r\nc\n', 'a\nb\nc\r\n', '\uFEFFx\r\ny', '', 'x']) {
    const { text, format } = readText(raw)
    assert.ok(!text.includes('\r'))
    const back = writeText(text, format)
    if (!raw.includes('\r\n') || !/(?<!\r)\n/.test(raw)) assert.equal(back, raw)
  }
  assert.equal(writeText(readText('a\r\nb\r\nc\n').text, readText('a\r\nb\r\nc\n').format), 'a\r\nb\r\nc\r\n')
  assert.equal(writeText(readText('a\nb\nc\r\n').text, readText('a\nb\nc\r\n').format), 'a\nb\nc\n')
})

test('markdown keeps the final newline and compares without line endings', () => {
  const { format } = readText('# A\r\n\r\nb\r\n')
  assert.equal(writeText(markdownText('# A\n\nb', format), format), '# A\r\n\r\nb\r\n')
  assert.equal(markdownText('b', readText('b').format), 'b')
  assert.equal(markdownText('', readText('').format), '')
  assert.ok(sameMarkdown('# A\r\n\r\nb\r\n\r\n', '# A\n\nb'))
  assert.ok(!sameMarkdown('* a', '- a'))
})

test('links go only to web pages and e-mail, also in loaded markdown', () => {
  for (const u of ['javascript:alert(1)', ' JavaScript:alert(1)', 'data:text/html,x', 'vbscript:x', '/x', 'file:///c:/x', ''])
    assert.ok(!safeUrl(u), u)
  assert.equal(linkHref('example.com/a?b'), 'https://example.com/a?b')
  assert.equal(linkHref('anna@example.org'), 'mailto:anna@example.org')
  assert.equal(linkHref('http://x.y'), 'http://x.y')
  for (const u of ['javascript:alert(1)', 'data:text/html,x', 'a b.com', '  ']) assert.equal(linkHref(u), '', u)
  const unsafe = (md: string) => pageUnsafe(marked.lexer(md) as unknown as MdToken[])
  assert.ok(!unsafe('# A\n\n- [x](https://a.b)\n  - <mailto:a@b.c>\n\n> **b**'))
  for (const md of ['[x](javascript:alert(1))', '- a\n  - [x](data:text/html,x)', 'a <b>b</b>', '<div>x</div>', '![i](https://a.b/i.png)', '[x](rel.md)'])
    assert.ok(unsafe(md), md)
})

type N = { version: string }
const tick = (ms = 5) => new Promise((r) => setTimeout(r, ms))
const err = (status: number, code: string, body = {}) => Object.assign(new Error(code), { status, code, body })

function fake(over: Partial<SessionIO<N, string>> = {}) {
  const log: string[] = []
  let version = 1
  let saving = 0
  const io: SessionIO<N, string> = {
    load: async () => ({ version: `v${version}` }),
    take: async () => (log.push('take'), 'L'),
    renew: async () => void log.push('renew'),
    release: (id) => void log.push(`release ${id}`),
    save: async (lockId, v, p) => {
      assert.equal(++saving, 1, 'one save at a time')
      log.push(`save ${lockId} ${v} ${p}`)
      await tick()
      saving--
      return { version: `v${++version}` }
    },
    ...over,
  }
  return { io, log }
}

const open = (io: SessionIO<N, string>, lockId?: string) =>
  new Session<N, string>({ version: 'v0' }, lockId, io, () => {}, { save: 10, renew: 1000 })

test('changes are saved after a pause, one request at a time, and the lock is released', async () => {
  const { io, log } = fake()
  const s = open(io, 'L')
  await tick()
  assert.deepEqual([s.status.kind, s.revision, s.node.version], ['saved', 1, 'v1'])
  s.change('a')
  s.change('ab')
  await tick(20)
  s.change('abc')
  await tick(12)
  s.change(() => 'abcd')
  assert.equal(await s.flush(), true)
  assert.deepEqual(log, ['save L v1 ab', 'save L v2 abc', 'save L v3 abcd'])
  assert.equal(s.status.kind, 'saved')
  await s.dispose()
  assert.equal(log.at(-1), 'release L')
})

test('network trouble keeps the changes and tries again', async () => {
  let fails = 1
  const { io, log } = fake()
  const save = io.save
  io.save = async (...a) => (fails-- > 0 ? Promise.reject(err(0, 'network')) : save(...a))
  const s = open(io)
  await tick()
  assert.deepEqual(log, ['take'])
  s.change('x')
  await tick(20)
  assert.deepEqual(s.status, { kind: 'error', code: 'network', retrying: true })
  assert.equal(s.dirty, true)
  s.retry()
  await s.flush()
  assert.equal(s.status.kind, 'saved')
  assert.equal(log.at(-1), 'save L v1 x')
  await s.dispose()
})

test('a lost lock is taken again once, then the file is read-only with the changes kept', async () => {
  let takes = 0
  const { io, log } = fake({
    take: async () => (++takes === 1 ? 'L2' : Promise.reject(err(409, 'locked', { holder: 'Anna' }))),
  })
  io.save = async () => Promise.reject(err(409, 'lock_lost'))
  const s = open(io, 'L')
  await tick()
  s.change('mine')
  await s.flush()
  assert.deepEqual(s.status, { kind: 'readonly', code: 'lock_lost', holder: undefined })
  assert.equal(s.unsaved(), 'mine')
  s.change('ignored')
  assert.equal(s.unsaved(), 'mine')
  await s.dispose()
  assert.deepEqual(log, [])
})

test('someone else holding the lock makes the file read-only, still loaded', async () => {
  const { io } = fake({ take: async () => Promise.reject(err(409, 'locked', { holder: 'Anna' })) })
  const s = open(io)
  await tick()
  assert.deepEqual(s.status, { kind: 'readonly', code: 'locked', holder: 'Anna' })
  assert.equal(s.revision, 1)
  await s.dispose()
})

test('an edit conflict waits for the user; reload drops the changes', async () => {
  const { io } = fake()
  io.save = async () => Promise.reject(err(412, 'edit_conflict', { by: 'Anna', at: '2026-01-02T03:04:05Z' }))
  const s = open(io, 'L')
  await tick()
  s.change('x')
  assert.equal(await s.flush(), false)
  assert.deepEqual(s.status, { kind: 'conflict', by: 'Anna', at: '2026-01-02T03:04:05Z' })
  s.reload()
  await tick()
  assert.deepEqual([s.status.kind, s.unsaved()], ['saved', undefined])
  await s.dispose()
})

test('hiding the page saves but keeps the lock, so a long break makes no extra versions', async () => {
  const { io, log } = fake()
  const s = open(io, 'L')
  await tick()
  s.change('x')
  await s.hide()
  assert.deepEqual([log, s.lockId, s.status.kind], [['save L v1 x'], 'L', 'saved'])
  s.show()
  await tick()
  await s.dispose()
})

test('leaving the page gives the lock back; coming back takes it again', async () => {
  const { io, log } = fake()
  const s = open(io, 'L')
  await tick()
  await s.leave()
  assert.deepEqual([log, s.lockId], [['release L'], undefined])
  s.show()
  await tick()
  assert.deepEqual([log.at(-1), s.lockId, s.status.kind], ['take', 'L', 'saved'])
  await s.dispose()
})
