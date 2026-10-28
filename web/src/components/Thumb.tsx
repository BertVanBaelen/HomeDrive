import { useState } from 'react'
import { contentUrl, type Mini, type Node } from '../api'
import { fileTypes, Icon } from './Icon'

// Images that browsers can show; others (HEIC, TIFF, SVG) get the icon.
const showable = /\.(jpe?g|png|gif|webp|avif|bmp)$/i

// Thumbnails show the file's content in the design. Server-side previews of
// documents come in v2; until then photos show themselves, folders their
// first four items, and other types a styled placeholder in their colours.
// data-thumb and data-mini let the folder zoom find where things are.
export function Thumb({ node }: { node: Node }) {
  const [failed, setFailed] = useState(false)
  const t = fileTypes[node.type]
  const ext = node.kind === 'file' ? extension(node.name) : ''

  if (node.kind === 'folder' && node.preview?.length) {
    return (
      <span className="thumb" style={{ background: t.bg }} data-thumb>
        <span className="thumb__minis">
          {node.preview.map((m) => (
            <MiniTile key={m.id} m={m} />
          ))}
        </span>
      </span>
    )
  }
  if (node.type === 'image' && showable.test(node.name) && !failed) {
    return (
      <span className="thumb" data-thumb>
        <img src={contentUrl(node.id)} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />
      </span>
    )
  }
  if (node.type === 'pdf' || node.type === 'doc' || node.type === 'text') {
    return (
      <span className="thumb" style={{ background: t.bg }} data-thumb>
        <span className="thumb__page">
          <i style={{ background: t.c }} />
          <i />
          <i />
          <i style={{ width: '80%' }} />
          <i />
          <i style={{ width: '60%' }} />
        </span>
        {ext && <span className="thumb__badge">{ext}</span>}
      </span>
    )
  }
  const dark = node.type === 'video' || node.type === 'audio' || node.type === 'code'
  return (
    <span className={'thumb' + (dark ? ' thumb--dark' : '')} style={{ background: dark ? darkBg[node.type] : t.bg }} data-thumb>
      <span className="thumb__icon">
        <Icon d={t.d} size={node.type === 'folder' ? 56 : 44} stroke={dark ? t.lc : t.c} width={1.5}
          fill={node.type === 'folder' ? '#fef9c3' : 'none'} />
      </span>
      {ext && <span className="thumb__badge">{ext}</span>}
    </span>
  )
}

// MiniTile is one item inside a folder thumbnail: the photo itself, or a
// small tile in the item's type colours.
function MiniTile({ m }: { m: Mini }) {
  const [failed, setFailed] = useState(false)
  const t = fileTypes[m.type]
  const dark = m.type === 'video' || m.type === 'audio' || m.type === 'code'
  if (m.type === 'image' && showable.test(m.name) && !failed) {
    return (
      <span className="mini" data-mini={m.id}>
        <img src={contentUrl(m.id)} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />
      </span>
    )
  }
  return (
    <span className="mini" data-mini={m.id} style={{ background: dark ? darkBg[m.type] : m.kind === 'folder' ? '#fef9c3' : '#fff' }}>
      <Icon d={t.d} size={20} stroke={dark ? t.lc : t.c} width={1.75} />
    </span>
  )
}

const darkBg: Record<string, string> = { video: '#4c1d95', audio: '#831843', code: '#1e293b' }

function extension(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot + 1, dot + 6) : ''
}
