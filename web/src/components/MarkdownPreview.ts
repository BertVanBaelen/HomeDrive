import { marked } from 'marked'
import { createElement as h, Fragment, type ReactNode } from 'react'
import { safeUrl } from '../editor/session'

// MarkdownPreview shows a Markdown file as a formatted page. It never
// renders HTML: marked's tokens become React elements, so raw HTML shows as
// text, images as their description and address (never loaded), and links
// only go to web pages and e-mail, in a new tab without a referrer.

interface Token {
  type: string
  text?: string
  href?: string
  depth?: number
  ordered?: boolean
  start?: number | ''
  task?: boolean
  checked?: boolean
  align?: (string | null)[]
  tokens?: Token[]
  items?: Token[]
  header?: Token[]
  rows?: Token[][]
}

const named: Record<string, string> = {
  amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: '\u00a0', shy: '\u00ad',
  copy: '©', reg: '®', trade: '™', hellip: '…', mdash: '—', ndash: '–', bull: '•', middot: '·',
  lsquo: '‘', rsquo: '’', ldquo: '“', rdquo: '”', laquo: '«', raquo: '»', euro: '€', pound: '£',
  deg: '°', times: '×', divide: '÷', plusmn: '±', para: '¶', sect: '§', larr: '←', rarr: '→',
}

// decodeEntities turns the character references marked leaves in text
// (&amp;, &#233;, &nbsp;) into the characters; React shows text as it is.
export function decodeEntities(s: string): string {
  if (!s.includes('&')) return s
  return s.replace(/&(#[xX][0-9a-fA-F]{1,6}|#[0-9]{1,7}|[A-Za-z][A-Za-z0-9]{1,31});/g, (whole, ref: string) => {
    if (ref[0] !== '#') return named[ref] ?? whole
    const code = ref[1] === 'x' || ref[1] === 'X' ? parseInt(ref.slice(2), 16) : parseInt(ref.slice(1), 10)
    return code > 0 && code <= 0x10ffff && (code < 0xd800 || code > 0xdfff) ? String.fromCodePoint(code) : '\ufffd'
  })
}

function each(tokens: Token[] | undefined, render: (t: Token) => ReactNode): ReactNode[] {
  return (tokens ?? []).map((t, i) => h(Fragment, { key: i }, render(t)))
}

function inline(t: Token): ReactNode {
  switch (t.type) {
    case 'text':
      return t.tokens ? each(t.tokens, inline) : decodeEntities(t.text ?? '')
    case 'escape':
      return decodeEntities(t.text ?? '')
    case 'strong':
      return h('strong', null, each(t.tokens, inline))
    case 'em':
      return h('em', null, each(t.tokens, inline))
    case 'del':
      return h('del', null, each(t.tokens, inline))
    case 'codespan':
      return h('code', null, t.text ?? '')
    case 'br':
      return h('br')
    case 'link': {
      const href = decodeEntities(t.href ?? '')
      return href && safeUrl(href)
        ? h('a', { href, target: '_blank', rel: 'noopener noreferrer nofollow' }, each(t.tokens, inline))
        : each(t.tokens, inline)
    }
    case 'image':
      return h('span', { className: 'markdown-preview__image' }, `[${decodeEntities(t.text ?? '')}] ${t.href ?? ''}`)
    case 'checkbox':
      return null
    default:
      // Raw HTML, and anything marked adds later: as text.
      return t.text ?? ''
  }
}

function block(t: Token): ReactNode {
  switch (t.type) {
    case 'space':
    case 'def':
    case 'checkbox':
      return null
    case 'heading': {
      const level = Math.max(1, Math.min(6, t.depth ?? 1))
      return h(`h${level}`, null, each(t.tokens, inline))
    }
    case 'paragraph':
      return h('p', null, each(t.tokens, inline))
    case 'text':
      return t.tokens ? each(t.tokens, inline) : decodeEntities(t.text ?? '')
    case 'blockquote':
      return h('blockquote', null, each(t.tokens, block))
    case 'code':
      return h('pre', null, h('code', null, t.text ?? ''))
    case 'hr':
      return h('hr')
    case 'list': {
      const start = t.ordered && typeof t.start === 'number' && t.start !== 1 ? t.start : undefined
      return h(t.ordered ? 'ol' : 'ul', { start, className: t.items?.some((i) => i.task) ? 'markdown-preview__tasks' : undefined },
        each(t.items, block))
    }
    case 'list_item':
      return h('li', null,
        t.task ? h('input', { type: 'checkbox', checked: !!t.checked, disabled: true, readOnly: true }) : null,
        each(t.tokens, block))
    case 'html':
      return h('p', { className: 'markdown-preview__html' }, t.text ?? '')
    case 'table': {
      const align = (i: number) => (t.align?.[i] ? { textAlign: t.align[i] as 'left' | 'center' | 'right' } : undefined)
      return h('div', { className: 'markdown-preview__table' },
        h('table', null,
          h('thead', null, h('tr', null, (t.header ?? []).map((c, i) => h('th', { key: i, style: align(i) }, each(c.tokens, inline))))),
          h('tbody', null, (t.rows ?? []).map((row, r) =>
            h('tr', { key: r }, row.map((c, i) => h('td', { key: i, style: align(i) }, each(c.tokens, inline))))))))
    }
    default:
      return inline(t)
  }
}

export function MarkdownPreview({ markdown }: { markdown: string }) {
  const tokens = marked.lexer(markdown) as unknown as Token[]
  return h('article', { className: 'markdown-preview' }, each(tokens, block))
}
