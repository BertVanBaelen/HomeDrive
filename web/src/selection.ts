import { useCallback, useEffect, useRef, useState } from 'react'

export const maxSelection = 500

// useSelection is the set of items selected in a list, with an anchor for
// Shift ranges.
export function useSelection<T extends { id: string }>(items: T[] | null) {
  const [ids, setIds] = useState<Set<string>>(new Set())
  const anchor = useRef<string | null>(null)

  const clear = useCallback(() => {
    setIds(new Set())
    anchor.current = null
  }, [])

  // toggle adds or removes one item; with range, everything from the anchor
  // to it is selected.
  const toggle = useCallback((id: string, range: boolean) => {
    setIds((old) => selectionToggle(old, items ?? [], anchor.current, id, range))
    if (!range || !anchor.current) anchor.current = id
  }, [items])

  const selectAll = useCallback(() => setIds(new Set(items?.slice(0, maxSelection).map((n) => n.id) ?? [])), [items])

  // Items that went away (moved, trashed elsewhere) drop out.
  useEffect(() => {
    if (!items) return
    setIds((old) => {
      const live = new Set(items.map((n) => n.id))
      const next = new Set([...old].filter((id) => live.has(id)))
      return next.size === old.size ? old : next
    })
  }, [items])

  return { ids, clear, toggle, selectAll }
}

export function selectionToggle(
  selected: Set<string>,
  items: { id: string }[],
  anchor: string | null,
  id: string,
  range: boolean,
): Set<string> {
  const next = new Set(selected)
  if (range && anchor) {
    const a = items.findIndex((n) => n.id === anchor)
    const b = items.findIndex((n) => n.id === id)
    if (a >= 0 && b >= 0) {
    for (let i = Math.min(a, b); i <= Math.max(a, b) && next.size < maxSelection; i++) next.add(items[i].id)
      return next
    }
  }
  if (next.has(id)) next.delete(id)
  else if (next.size < maxSelection) next.add(id)
  return next
}

// ownsKeys tells whether a key press is the file list's to handle: not
// while typing, and not while a dialog, the viewer or the editor is open.
export function ownsKeys(e: KeyboardEvent): boolean {
  const target = e.target as HTMLElement | null
  if (target?.closest?.('input, textarea, select, [contenteditable="true"]')) return false
  return !document.querySelector('[aria-modal="true"], .editor')
}

// useSelectionKeys: Ctrl/⌘+A selects everything in the list, Esc clears.
export function useSelectionKeys(selection: { ids: Set<string>; clear: () => void; selectAll: () => void }) {
  const ref = useRef(selection)
  ref.current = selection
  useEffect(() => {
    function keys(e: KeyboardEvent) {
      if (!ownsKeys(e)) return
      if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === 'a') {
        e.preventDefault()
        ref.current.selectAll()
      } else if (e.key === 'Escape' && ref.current.ids.size) {
        ref.current.clear()
      }
    }
    document.addEventListener('keydown', keys)
    return () => document.removeEventListener('keydown', keys)
  }, [])
}
