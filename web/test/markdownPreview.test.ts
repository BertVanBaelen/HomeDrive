import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

registerHooks({ resolve: (s, c, next) => next(s.startsWith('.') && !/\.\w+$/.test(s) ? s + '.ts' : s, c) })
const { MarkdownPreview, decodeEntities } = await import('../src/components/MarkdownPreview.ts')

const html = (markdown: string) => renderToStaticMarkup(createElement(MarkdownPreview, { markdown }))

test('nothing in a file becomes HTML, scripts or loaded images', () => {
  const out = html([
    '<script>alert(1)</script>',
    '',
    'a <img src=x onerror=alert(1)> b <b>bold</b>',
    '',
    '![logo](https://evil.example/t.png)',
    '',
    '[js](javascript:alert(1)) [data](data:text/html,x) [ent](java&#115;cript:alert(1)) [rel](notes.md)',
  ].join('\n'))
  assert.doesNotMatch(out, /<(script|img|b)[\s>/]|<[^>]*\sonerror=|href="(?!https?:|mailto:)/i)
  assert.match(out, /&lt;script&gt;alert\(1\)&lt;\/script&gt;/)
  assert.match(out, /\[logo\] https:\/\/evil\.example\/t\.png/)
  assert.match(out, /<p>js data ent rel<\/p>/)
  assert.doesNotMatch(out, /<a /)
})

test('safe links open in a new tab without a referrer', () => {
  assert.equal(
    html('[a *b*](https://x.y/?p=1&amp;q=2) <mailto:a@b.c>'),
    '<article class="markdown-preview"><p><a href="https://x.y/?p=1&amp;q=2" target="_blank" rel="noopener noreferrer nofollow">a <em>b</em></a> ' +
      '<a href="mailto:a@b.c" target="_blank" rel="noopener noreferrer nofollow">mailto:a@b.c</a></p></article>',
  )
})

test('blocks: nested lists, quotes, code, tasks and tables', () => {
  const out = html('- a\n  - b\n\n> **q**\n>\n> r\n\n3. three\n\n```\nx < y && z\n```\n\n- [x] done\n- [ ] todo\n\n| L | R |\n|:--|--:|\n| 1 | 2 |')
  assert.match(out, /<ul><li>a<ul><li>b<\/li><\/ul><\/li><\/ul>/)
  assert.match(out, /<blockquote><p><strong>q<\/strong><\/p><p>r<\/p><\/blockquote>/)
  assert.match(out, /<ol start="3"><li>three<\/li><\/ol>/)
  assert.match(out, /<pre><code>x &lt; y &amp;&amp; z<\/code><\/pre>/)
  assert.match(out, /<ul class="markdown-preview__tasks"><li><input type="checkbox" disabled="" readonly="" checked=""\/>done<\/li><li><input type="checkbox" disabled="" readonly=""\/>todo/i)
  assert.match(out, /<th style="text-align:left">L<\/th><th style="text-align:right">R<\/th>/)
  assert.match(out, /<td style="text-align:left">1<\/td>/)
})

test('character references are shown once, code stays as written', () => {
  assert.equal(decodeEntities('Tom &amp; Jerry &copy; &#233; &#x1F600; &amp;lt; &bogus; &#0; &#xD800;'), 'Tom & Jerry © é 😀 &lt; &bogus; \ufffd \ufffd')
  const out = html('A &amp; B &nbsp;x `&amp;` &amp;#115;')
  assert.match(out, /A &amp; B \u00a0x <code>&amp;amp;<\/code> &amp;#115;/)
})
