import type { Locale, Translate } from './i18n'
import type { Node } from './api'

// Sizes use decimal units, like the design: 184 KB, 1.2 MB.
export function formatSize(bytes: number, locale: Locale): string {
  if (!bytes) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = bytes
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  const digits = i > 0 && v < 10 ? 1 : 0
  return `${v.toLocaleString(locale, { maximumFractionDigits: digits, minimumFractionDigits: digits })} ${units[i]}`
}

// formatAmount is formatSize for totals, where nothing is "0 B".
export function formatAmount(bytes: number, locale: Locale): string {
  return bytes > 0 ? formatSize(bytes, locale) : `${(0).toLocaleString(locale)} B`
}

// "Just now", "5 min ago", "3 h ago", "Yesterday", then a date.
export function formatWhen(iso: string, t: Translate, locale: Locale, now = Date.now()): string {
  const then = Date.parse(iso)
  const min = Math.floor((now - then) / 60000)
  if (min < 1) return t('when.now')
  if (min < 60) return t('when.minutes', { n: min })
  if (min < 24 * 60) return t('when.hours', { n: Math.round(min / 60) })
  const d = new Date(then)
  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  if (d.toDateString() === yesterday.toDateString()) return t('when.yesterday')
  return d.toLocaleDateString(locale, { day: 'numeric', month: 'short', year: 'numeric' })
}

export function formatDateTime(iso: string, locale: Locale): string {
  return new Date(iso).toLocaleString(locale, { dateStyle: 'medium', timeStyle: 'short' })
}

// File extensions are hidden in titles; Info shows the full name.
export function displayName(n: Pick<Node, 'name' | 'kind'>): string {
  if (n.kind === 'folder') return n.name
  const dot = n.name.lastIndexOf('.')
  return dot > 0 ? n.name.slice(0, dot) : n.name
}
