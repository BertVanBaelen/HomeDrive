import { useState } from 'react'
import { contentUrl, type Node } from '../api'
import { fileTypes, Icon } from './Icon'

// Images that browsers can show; others (HEIC, TIFF, SVG) get the icon.
const showable = /\.(jpe?g|png|gif|webp|avif|bmp)$/i

// Thumbnails show the file's content in the design. Server-side previews of
// documents come in v2; until then photos show themselves and other types a
// styled placeholder in their colours.
export function Thumb({ node }: { node: Node }) {
  const [failed, setFailed] = useState(false)
  const t = fileTypes[node.type]
  const ext = node.kind === 'file' ? extension(node.name) : ''

  if (node.type === 'image' && showable.test(node.name) && !failed) {
    return (
      <span className="thumb">
        <img src={contentUrl(node.id)} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />
      </span>
    )
  }
  if (node.type === 'pdf' || node.type === 'doc' || node.type === 'text') {
    return (
      <span className="thumb" style={{ background: t.bg }}>
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
    <span className={'thumb' + (dark ? ' thumb--dark' : '')} style={{ background: dark ? darkBg[node.type] : t.bg }}>
      <span className="thumb__icon">
        <Icon d={t.d} size={node.type === 'folder' ? 56 : 44} stroke={dark ? t.lc : t.c} width={1.5}
          fill={node.type === 'folder' ? '#fef9c3' : 'none'} />
      </span>
      {ext && <span className="thumb__badge">{ext}</span>}
    </span>
  )
}

const darkBg: Record<string, string> = { video: '#4c1d95', audio: '#831843', code: '#1e293b' }

function extension(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot + 1, dot + 6) : ''
}
