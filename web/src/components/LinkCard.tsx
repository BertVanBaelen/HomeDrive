import { useState } from 'react'
import type { LinkInfo, Node } from '../api'
import { displayName } from '../format'
import { useT } from '../i18n'
import { useThumbUrl } from './content'
import { fileTypes, Icon, icons } from './Icon'

// Sites whose links get a thumbnail from the server (their oEmbed
// providers); every other site shows the globe.
const previewSites = new Set(['YouTube', 'TikTok'])

export function hasThumb(link?: LinkInfo): boolean {
  return !!link && previewSites.has(link.site)
}

// linkByline is "YouTube · Cats Inc": the site, and the author when known.
export function linkByline(link: LinkInfo): string {
  return [link.site, link.author].filter(Boolean).join(' · ')
}

// LinkThumb is a saved link's thumbnail on its card: the video's picture
// with a play mark, else the globe in the link colours. The site is the
// badge where other files show their extension.
export function LinkThumb({ node }: { node: Node }) {
  const thumbUrl = useThumbUrl()
  const [failed, setFailed] = useState(false)
  const t = fileTypes.link
  const site = node.link?.site
  if (hasThumb(node.link) && !failed) {
    return (
      <span className="thumb thumb--dark" style={{ background: '#111827' }} data-thumb>
        <img src={thumbUrl(node.id, node.version)} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />
        <span className="thumb__play">
          <Icon d={icons.play} size={40} stroke="#fff" width={1.5} />
        </span>
        <span className="thumb__badge thumb__badge--site">{site}</span>
      </span>
    )
  }
  return (
    <span className="thumb" style={{ background: t.bg }} data-thumb>
      <span className="thumb__icon">
        <Icon d={icons.globe} size={44} stroke={t.c} width={1.5} />
      </span>
      <span className="thumb__badge thumb__badge--site">{site ?? 'url'}</span>
    </span>
  )
}

// LinkCard is a saved link in the viewer. The whole card opens the
// address in a new tab, so on a phone YouTube and TikTok links open in
// their apps; noreferrer keeps Homedrive's address from the site.
export function LinkCard({ node }: { node: Node }) {
  const t = useT()
  const thumbUrl = useThumbUrl()
  const [failed, setFailed] = useState(false)
  const link = node.link!
  return (
    <a className="link-card" href={link.url} target="_blank" rel="noopener noreferrer">
      <span className="link-card__media">
        {hasThumb(link) && !failed ? (
          <>
            <img src={thumbUrl(node.id, node.version)} alt="" onError={() => setFailed(true)} />
            <span className="thumb__play">
              <Icon d={icons.play} size={56} stroke="#fff" width={1.5} />
            </span>
          </>
        ) : (
          <Icon d={icons.globe} size={64} stroke={fileTypes.link.lc} width={1.25} />
        )}
      </span>
      <span className="link-card__body">
        <strong>{displayName(node)}</strong>
        <span className="link-card__byline">{linkByline(link)}</span>
        {node.description && <span className="link-card__text">{node.description}</span>}
        <span className="link-card__url">{link.url}</span>
        <span className="btn btn--primary">
          <Icon d={icons.external} size={18} />
          {t('link.openIn', { site: link.site })}
        </span>
      </span>
    </a>
  )
}
