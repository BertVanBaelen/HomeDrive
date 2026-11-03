import { useEffect, useRef, useState, type RefObject } from 'react'
import type { Mini, Node, Snippet } from '../api'
import { displayName, formatDuration } from '../format'
import { useT } from '../i18n'
import { useThumbUrl } from './content'
import { fileTypes, Icon } from './Icon'
import { LinkThumb } from './LinkCard'

// Thumbnails show the file's content (mockup Thumb.dc.html): the server's
// images of photos, video frames, audio covers and PDF pages, snippets of
// text, code, spreadsheets and zips laid out here, folders as their first
// four items. Without one a file shows a placeholder in its type's
// colours. small is the Share dialog's thumbnail: half the type size, the
// list-size image.
// data-thumb and data-mini let the folder zoom find where things are.
export function Thumb({ node, small }: { node: Node; small?: boolean }) {
  const thumbUrl = useThumbUrl()
  const [failed, setFailed] = useState(false)
  const t = fileTypes[node.type]
  const cls = 'thumb' + (small ? ' thumb--small' : '')

  if (node.kind === 'folder' && node.preview?.length) {
    return (
      <span className={cls} style={{ background: t.bg }} data-thumb>
        <span className="thumb__minis">
          {node.preview.map((m) => (
            <MiniTile key={m.id} m={m} />
          ))}
        </span>
      </span>
    )
  }
  if (node.type === 'link') return <LinkThumb node={node} small={small} />
  if (node.thumb === 'snippet') return <SnippetThumb node={node} small={small} />
  if (node.thumb !== 'image' || failed) return <Placeholder node={node} small={small} />

  const img = (
    <img src={thumbUrl(node.id, node.version, small ? 'list' : 'grid')} alt="" loading="lazy" decoding="async"
      onError={() => setFailed(true)} />
  )
  const length = node.durationMs ? <span className="thumb__duration">{formatDuration(node.durationMs)}</span> : null
  switch (node.type) {
    case 'pdf':
      return (
        <span className={cls} style={{ background: t.bg }} data-thumb>
          <span className="thumb__page thumb__page--image">{img}</span>
        </span>
      )
    case 'video':
      return (
        <span className={cls + ' thumb--dark'} style={{ background: darkBg.video }} data-thumb>
          {img}
          <span className="thumb__video">
            <span className="thumb__play-btn">
              <svg aria-hidden="true" viewBox="0 0 24 24" fill="#1f2937">
                <path d="M8 5v14l11-7z" />
              </svg>
            </span>
          </span>
          {length}
        </span>
      )
    case 'audio':
      return (
        <span className={cls + ' thumb--dark'} style={{ background: darkBg.audio }} data-thumb>
          {img}
          {length}
        </span>
      )
  }
  return (
    <span className={cls} style={{ background: t.bg }} data-thumb>
      {img}
    </span>
  )
}

// Placeholder stands in for files without a thumbnail: a blank page for
// documents, the title-and-waveform card for audio, the type's icon for
// the rest, with the extension as a badge.
function Placeholder({ node, small }: { node: Node; small?: boolean }) {
  const t = fileTypes[node.type]
  const ext = node.kind === 'file' ? extension(node.name) : ''
  const cls = 'thumb' + (small ? ' thumb--small' : '')
  if (node.type === 'audio') return <AudioCard node={node} small={small} />
  if (node.type === 'pdf' || node.type === 'doc' || node.type === 'text') {
    return (
      <span className={cls} style={{ background: t.bg }} data-thumb>
        <span className="thumb__page thumb__page--blank">
          <i style={{ background: t.c }} />
          <i />
          <i />
          <i style={{ width: '80%' }} />
          <i />
          <i style={{ width: '60%' }} />
        </span>
        {ext && !small && <span className="thumb__badge">{ext}</span>}
      </span>
    )
  }
  const dark = node.type === 'video' || node.type === 'code'
  return (
    <span className={cls + (dark ? ' thumb--dark' : '')} style={{ background: dark ? darkBg[node.type] : t.bg }} data-thumb>
      <span className="thumb__icon">
        <Icon d={t.d} size={small ? 24 : node.type === 'folder' ? 56 : 44} stroke={dark ? t.lc : t.c} width={1.5}
          fill={node.type === 'folder' ? '#fef9c3' : 'none'} />
      </span>
      {ext && !small && <span className="thumb__badge">{ext}</span>}
    </span>
  )
}

// AudioCard is an audio file without cover art: its title, its length
// and a waveform that is the same every time for the same file.
function AudioCard({ node, small }: { node: Node; small?: boolean }) {
  const t = useT()
  const seed = (hash(node.id) % 50) + 1
  const bars = Array.from({ length: 22 }, (_, i) => {
    const x = Math.sin(seed * 97 + i * 13.7) * 10000
    return Math.round(20 + (x - Math.floor(x)) * 80)
  })
  return (
    <span className={'thumb thumb--dark' + (small ? ' thumb--small' : '')} style={{ background: darkBg.audio }} data-thumb>
      <span className="thumb__audio">
        <span className="thumb__audio-text">
          <strong>{displayName(node)}</strong>
          <span>{node.durationMs ? formatDuration(node.durationMs) : t('short.audio')}</span>
        </span>
        <span className="thumb__wave" aria-hidden="true">
          {bars.map((h, i) => (
            <i key={i} style={{ height: `${h}%` }} />
          ))}
        </span>
      </span>
    </span>
  )
}

// Snippets are fetched once per version and kept for the session; cards
// ask only when they come near the screen.
const snippets = new Map<string, Promise<Snippet | null>>()

function loadSnippet(url: string): Promise<Snippet | null> {
  let p = snippets.get(url)
  if (!p) {
    p = fetch(url, { credentials: 'same-origin' })
      .then((r) => (r.ok ? (r.json() as Promise<Snippet>) : null))
      .catch(() => null)
    snippets.set(url, p)
  }
  return p
}

function SnippetThumb({ node, small }: { node: Node; small?: boolean }) {
  const thumbUrl = useThumbUrl()
  const ref = useRef<HTMLSpanElement>(null)
  const near = useNear(ref)
  const [snippet, setSnippet] = useState<Snippet | null | undefined>(undefined)
  const url = thumbUrl(node.id, node.version, 'snippet')
  useEffect(() => {
    if (!near) return
    let live = true
    loadSnippet(url).then((s) => live && setSnippet(s))
    return () => {
      live = false
    }
  }, [near, url])

  if (snippet === null) return <Placeholder node={node} small={small} />
  const cls = 'thumb' + (small ? ' thumb--small' : '')
  const t = fileTypes[node.type]
  if (!snippet) {
    // Until it arrives: the type's background, nothing that jumps.
    return <span ref={ref} className={cls + (node.type === 'code' ? ' thumb--dark' : '')}
      style={{ background: node.type === 'code' ? darkBg.code : t.bg }} data-thumb />
  }
  switch (snippet.kind) {
    case 'code':
      return (
        <span className={cls + ' thumb--dark'} style={{ background: darkBg.code }} data-thumb>
          <span className="snip-code">
            {(snippet.lines ?? []).map((l, i) => (
              <span key={i} style={{ color: codeColour(l) }}>{l || ' '}</span>
            ))}
          </span>
        </span>
      )
    case 'sheet':
      return (
        <span className={cls} style={{ background: t.bg }} data-thumb>
          <span className="snip-sheet">
            {(snippet.rows ?? []).map((r, ri) => (
              <span key={ri} className={'snip-sheet__row' + (ri === 0 ? ' snip-sheet__row--head' : '')}
                style={ri === 0 ? { background: fileTypes.sheet.c } : undefined}>
                {Array.from({ length: 4 }, (_, ci) => (
                  <span key={ci}>{r[ci] ?? ''}</span>
                ))}
              </span>
            ))}
          </span>
        </span>
      )
    case 'archive':
      return <ArchiveThumb node={node} snippet={snippet} small={small} />
  }
  const blocks = pageBlocks(snippet.lines ?? [], /\.(md|markdown)$/i.test(node.name))
  return (
    <span className={cls} style={{ background: t.bg }} data-thumb>
      <span className="thumb__page snip-page">
        {blocks.map((b, i) => (
          <span key={i} className={`snip-page__${b.k}`}>{b.t}</span>
        ))}
      </span>
    </span>
  )
}

function ArchiveThumb({ node, snippet, small }: { node: Node; snippet: Snippet; small?: boolean }) {
  const t = useT()
  const entries = snippet.entries ?? []
  const more = (snippet.total ?? entries.length) - entries.length
  return (
    <span className={'thumb' + (small ? ' thumb--small' : '')} style={{ background: fileTypes.archive.bg }} data-thumb>
      <span className="snip-archive">
        <strong>{displayName(node)}</strong>
        {entries.map((e, i) => (
          <span key={i}>
            <i style={{ background: e.endsWith('/') ? '#60a5fa' : '#d1d5db' }} />
            {e}
          </span>
        ))}
        {more > 0 && <span className="snip-archive__more">{t('thumb.more', { count: more })}</span>}
      </span>
    </span>
  )
}

// pageBlocks lays out a text's first lines like the mockup's pages:
// Markdown headings as headings, list items with bullets, rules as lines.
function pageBlocks(lines: string[], markdown: boolean): { k: 'h' | 'sec' | 'p' | 'rule'; t: string }[] {
  const out: { k: 'h' | 'sec' | 'p' | 'rule'; t: string }[] = []
  const strip = (s: string) => s.replace(/\*\*|__|\*|`/g, '')
  for (const raw of lines) {
    const line = raw.trimEnd()
    if (!markdown) {
      out.push({ k: 'p', t: line || ' ' })
      continue
    }
    let m: RegExpMatchArray | null
    if (!line.trim()) continue
    if ((m = line.match(/^#\s+(.*)$/))) out.push({ k: 'h', t: strip(m[1]) })
    else if ((m = line.match(/^#{2,6}\s+(.*)$/))) out.push({ k: 'sec', t: strip(m[1]) })
    else if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) out.push({ k: 'rule', t: '' })
    else if ((m = line.match(/^\s*[-*+]\s+(?:\[[ xX]\]\s+)?(.*)$/))) out.push({ k: 'p', t: '•  ' + strip(m[1]) })
    else out.push({ k: 'p', t: strip(line) })
  }
  return out
}

// codeColour tints code lines like the mockup's editor.
function codeColour(line: string): string {
  const s = line.trim()
  if (s.startsWith('#') || s.startsWith('//')) return '#93c5fd'
  if (/[:{]$/.test(s)) return '#f9a8d4'
  if (s.startsWith('-')) return '#86efac'
  return '#e2e8f0'
}

// MiniTile is one item inside a folder thumbnail: its picture when it has
// one, else a tile in its type's colours (mockup: a colour with a stripe).
function MiniTile({ m }: { m: Mini }) {
  const thumbUrl = useThumbUrl()
  const [failed, setFailed] = useState(false)
  if (m.thumb === 'image' && !failed) {
    return (
      <span className="mini" data-mini={m.id} style={{ background: darkBg[m.type] ?? '#f3f4f6' }}>
        <img src={thumbUrl(m.id, m.version, 'list')} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />
      </span>
    )
  }
  const tile = miniColours[m.type] ?? { bg: '#ffffff', stripe: '#e5e7eb' }
  return (
    <span className="mini" data-mini={m.id} style={{ background: tile.bg }}>
      <span className="mini__stripe" style={{ background: tile.stripe }} />
    </span>
  )
}

const miniColours: Partial<Record<Node['type'], { bg: string; stripe: string }>> = {
  pdf: { bg: '#ffffff', stripe: '#fecaca' },
  doc: { bg: '#ffffff', stripe: '#bfdbfe' },
  text: { bg: '#ffffff', stripe: '#bae6fd' },
  sheet: { bg: '#ffffff', stripe: '#86efac' },
  code: { bg: '#1e293b', stripe: '#475569' },
  archive: { bg: '#fef3c7', stripe: '#f59e0b' },
  audio: { bg: '#831843', stripe: '#f9a8d4' },
  folder: { bg: '#fef9c3', stripe: '#fde047' },
  image: { bg: '#ccfbf1', stripe: '#5eead4' },
  video: { bg: '#ede9fe', stripe: '#a78bfa' },
  link: { bg: '#eef2ff', stripe: '#a5b4fc' },
}

const darkBg: Partial<Record<Node['type'], string>> = { video: '#4c1d95', audio: '#831843', code: '#1e293b' }

// useNear turns true once the element comes within a screen of the view.
function useNear(ref: RefObject<HTMLElement | null>): boolean {
  const [near, setNear] = useState(false)
  useEffect(() => {
    const el = ref.current
    if (near || !el) return
    if (typeof IntersectionObserver === 'undefined') {
      setNear(true)
      return
    }
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) setNear(true)
    }, { rootMargin: '400px' })
    io.observe(el)
    return () => io.disconnect()
  }, [ref, near])
  return near
}

function hash(s: string): number {
  let h = 7
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 100003
  return h
}

function extension(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot + 1, dot + 6) : ''
}

