import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { MarkdownManager } from '@tiptap/markdown'
import { marked } from 'marked'

registerHooks({ resolve: (s, c, next) => next(s.startsWith('.') && !/\.\w+$/.test(s) ? s + '.ts' : s, c) })
const { pageExtensions, sameMeaning } = await import('../src/editor/markdownSchema.ts')
const { pageUnsafe } = await import('../src/editor/session.ts')

// The page editor's Markdown, as MarkdownEditor gets it.
const md = new MarkdownManager({ extensions: pageExtensions })
const page = (s: string) => md.serialize(md.parse(s))
const opens = (s: string) => !pageUnsafe(marked.lexer(s) as never) && sameMeaning(page(s), s)

test('files the page editor opens keep their meaning, and a second save changes nothing', () => {
  const files = [
    '# T\n\nA & B &amp; C\n\n- a\n  - b\n\n1. x\n2. y\n\n> **q** and *e*\n\n```js\nx<y\n```\n\n---\n\n[l](https://a.b) `c` \\* not\n\nline  \nbreak',
    '#### four\n\n###### six',
    'A\n\n\n\nB',
    '3. three\n4. four',
    '* star\n* more',
    'Title\n=====',
    '_e_ __s__ ***both***',
    '[a *b*](mailto:x@y.z) <https://a.b>',
    '> > nested',
    '    indented',
    'snake_case_word a\\\\b',
  ]
  for (const f of files) {
    assert.ok(opens(f), f)
    const once = page(f)
    assert.equal(page(once), once, f)
  }
})

test('files the page editor would change in meaning stay as text', () => {
  for (const f of ['\\- not a list', '1\\. not a list', '\\+ x', '0. zero\n1. one', '- [ ] todo', '- [x] done', '<b>x</b>', '| A |\n|---|\n| 1 |', '![i](https://a.b/i.png)', '[x](javascript:alert(1))'])
    assert.ok(!opens(f), f)
})

test('sameMeaning ignores empty lines only', () => {
  assert.ok(sameMeaning('a\n\n\n\n\n\nb', 'a\n\n&nbsp;\n\nb'))
  assert.ok(sameMeaning('* a\n* b\n', '- a\n- b'))
  assert.ok(!sameMeaning('# a', 'a'))
  assert.ok(!sameMeaning('\\# a', '# a'))
})
