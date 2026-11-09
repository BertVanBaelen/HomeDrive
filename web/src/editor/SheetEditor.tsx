import { memo, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { api, contentUrl, type Node, type SheetGrid, type SheetView } from '../api'
import { Icon, icons } from '../components/Icon'
import { useT, type MessageKey } from '../i18n'
import { cellKey, colName, MAX_CELL_CHARS, MAX_COLS, MAX_ROWS } from '../sheet/engine'
import {
  badFormula, clampSel, clean, cleared, load, move, paste, rectOf, table, toTsv, update, values,
  type Pos, type Sel, type Shown, type Snapshot,
} from '../sheet/grid'
import { FileState } from './TextEditor'
import type { EditorBodyProps, Session } from './useEditSession'

const ROW_PX = 28 // a row's height in editor.css
const AHEAD = 100 // rows drawn from the scroll position on; others are empty boxes

type Draft = { at: Pos; text: string; bar: boolean } // bar: typed in the formula bar
type Apply = (cells: Record<string, string>, rows?: number, cols?: number) => void

// SheetEditor edits a spreadsheet of one sheet in a grid; others show
// read-only. The browser computes what cells show (sheet/engine.ts) and the
// server everything again on save.
export default function SheetEditor({ session, header }: EditorBodyProps<Snapshot>) {
  const [file, setFile] = useState<SheetView | { error: unknown } | null>(null)
  const { revision } = session
  const id = session.node.id
  // Read again when the session loads a version it didn't write; the one
  // shown stays meanwhile (and with it the scroll position and selection).
  useEffect(() => {
    if (!revision) return
    let live = true
    api.getSheet(id).then(
      (v) => live && setFile(v),
      (error) => live && setFile({ error }),
    )
    return () => {
      live = false
    }
  }, [id, revision])
  if (!file || 'error' in file) return <FileState file={file} session={session} />
  if (file.editable && file.grid) return <EditSheet session={session} header={header} grid={file.grid} />
  return <ReadSheet view={file} id={id} header={header} />
}

function EditSheet({ session, header, grid }: { session: Session<Node, Snapshot>; header: HTMLElement | null; grid: SheetGrid }) {
  const t = useT()
  const [s, setS] = useState(() => load(grid))
  const [from, setFrom] = useState(grid)
  if (from !== grid) {
    setFrom(grid)
    setS(load(grid))
  }
  const ro = session.readOnly
  const apply: Apply = (cells, rows = s.rows, cols = s.cols) => {
    const next = !session.readOnly && update(s, cells, rows, cols, session.dirty)
    if (!next) return
    setS(next)
    session.change(next)
  }
  const add = (label: MessageKey, text: MessageKey, full: boolean, onClick: () => void) => (
    <button type="button" aria-label={t(label)} title={t(label)} disabled={ro || full} onClick={onClick}>
      <Icon d={icons.plus} size={16} />
      {t(text)}
    </button>
  )
  return (
    <>
      {header && createPortal(
        <div className="editor__modes" role="group" aria-label={t('sheet.size')}>
          {add('sheet.addRow', 'sheet.row', s.rows >= MAX_ROWS, () => apply({}, s.rows + 1))}
          {add('sheet.addCol', 'sheet.col', s.cols >= MAX_COLS, () => apply({}, s.rows, s.cols + 1))}
        </div>,
        header,
      )}
      <Grid s={s} ro={ro} label={t('sheet.grid')} apply={apply} />
    </>
  )
}

// ReadSheet shows the values of a spreadsheet that can't be edited here,
// why, its sheets as tabs and a download.
function ReadSheet({ view, id, header }: { view: SheetView; id: string; header: HTMLElement | null }) {
  const t = useT()
  const [i, setI] = useState(0)
  const sheets = view.sheets ?? []
  const sh = sheets[Math.min(i, sheets.length - 1)]
  const s = useMemo(() => sh && values(sh), [sh])
  const key = `sheet.reason.${view.reason}` as MessageKey
  const reason = view.reason === 'read_only' ? '' : t(key) === key ? t('error.not_editable') : t(key)
  return (
    <>
      {header && createPortal(
        <a className="btn btn--small" href={contentUrl(id, true)} download>
          <Icon d={icons.download} size={18} />
          {t('viewer.download')}
        </a>,
        header,
      )}
      {!s && <p className="editor__empty">{reason || t('error.not_editable')}</p>}
      {s && reason && <p className="editor__note">{reason}</p>}
      {s && <Grid key={i} s={s} ro label={sh.name || t('sheet.grid')} />}
      {sheets.length > 1 && (
        <div className="editor__modes sheet__tabs" role="tablist" aria-label={t('sheet.tabs')}>
          {sheets.map((x, j) => (
            <button key={j} type="button" role="tab" aria-selected={j === i} onClick={() => setI(j)}>{x.name}</button>
          ))}
        </div>
      )}
    </>
  )
}

// The cell's input takes the focus, with the caret at the end.
const focusEnd = (el: HTMLInputElement | null) => {
  el?.focus()
  el?.setSelectionRange(el.value.length, el.value.length)
}

const cellAt = (el: Element): Pos | null => {
  const c = el.closest<HTMLElement>('[data-c]')
  const r = c?.parentElement?.dataset.r
  return c && r ? { r: +r, c: +c.dataset.c! } : null
}

// Grid is the formula bar and the cells. Without apply it is read-only.
function Grid({ s, ro, label, apply }: { s: Snapshot; ro: boolean; label: string; apply?: Apply }) {
  const t = useT()
  const uid = useId()
  const [sel, setSel] = useState<Sel>({ a: { r: 0, c: 0 }, b: { r: 0, c: 0 } })
  const [draft, setDraft] = useState<Draft | null>(null)
  const [bad, setBad] = useState(false)
  const [top, setTop] = useState(0)
  const draftRef = useRef<Draft | null>(null)
  const gridRef = useRef<HTMLDivElement>(null)
  const onClip = useRef<(e: ClipboardEvent) => void>(null)
  const { a, b } = clampSel(sel, s)
  const rect = rectOf({ a, b })
  const key = cellKey(a.r, a.c)
  const bKey = cellKey(b.r, b.c)
  const editing = ro ? null : draft
  const contents = t('sheet.contents', { cell: key })

  function edit(d: Draft | null) {
    draftRef.current = d
    setDraft(d)
    setBad(false)
  }
  // commit enters the cell being edited; false when it holds a formula
  // that isn't valid, which stays to be corrected (or Escape).
  function commit(): boolean {
    const d = draftRef.current
    if (!d) return true
    const raw = clean(d.text)
    if (!ro && badFormula(raw)) {
      setBad(true)
      return false
    }
    edit(null)
    if (!ro) apply?.({ [cellKey(d.at.r, d.at.c)]: raw })
    return true
  }
  const focusGrid = () => gridRef.current?.focus({ preventScroll: true })
  const select = (p: Pos, extend = false) => setSel(extend ? { a, b: p } : { a: p, b: p })
  function begin(text: string) {
    if (ro || draftRef.current) return
    select(a)
    edit({ at: a, text, bar: false })
  }

  function onKey(e: React.KeyboardEvent) {
    const k = e.key
    const stop = () => {
      e.preventDefault()
      e.stopPropagation()
    }
    if (e.target !== gridRef.current || draftRef.current) {
      // Editing, in the cell's input or the formula bar.
      if (k === 'Escape' && draftRef.current) {
        stop()
        edit(null)
        focusGrid()
      } else if (k === 'Enter' || (k === 'Tab' && draftRef.current)) {
        stop()
        if (!commit()) return
        select(move(a, k, s, false, e.shiftKey)!)
        focusGrid()
      }
      return
    }
    const ctrl = e.ctrlKey || e.metaKey
    if (e.altKey) return
    if (ctrl && k.toLowerCase() === 'a') {
      stop()
      setSel({ a: { r: 0, c: 0 }, b: { r: s.rows - 1, c: s.cols - 1 } })
    } else if (k === 'Enter' || k === 'F2') {
      stop()
      begin(s.raws[key] ?? '')
    } else if ((k === 'Delete' || k === 'Backspace') && !ro) {
      stop()
      apply?.(cleared(s.raws, rect))
    } else if (k.length === 1 && !ctrl) {
      if (ro) return
      stop()
      begin(k)
    } else {
      const extend = e.shiftKey && k !== 'Tab'
      const to = move(extend ? b : a, k, s, ctrl, e.shiftKey)
      // Tab on the edge moves the focus on.
      if (!to || (k === 'Tab' && to.r === a.r && to.c === a.c)) return
      stop()
      select(to, extend)
    }
  }

  function onMouseDown(e: React.MouseEvent) {
    const el = e.target as HTMLElement
    if (e.button !== 0 || el.tagName === 'INPUT' || !el.closest('.sheet__row')) return
    e.preventDefault()
    if (!commit()) return
    focusGrid()
    const p = cellAt(el)
    if (p) select(p, e.shiftKey)
  }

  useEffect(() => {
    onClip.current = (e) => {
      if (!e.clipboardData) return
      e.preventDefault()
      if (e.type !== 'paste') {
        e.clipboardData.setData('text/plain', toTsv(table(rect, (k) => s.shown[k]?.v ?? '')))
        if (e.type === 'cut' && !ro) apply?.(cleared(s.raws, rect))
        return
      }
      const text = e.clipboardData.getData('text/plain')
      if (ro || !text) return
      const at = { r: rect.r0, c: rect.c0 }
      const p = paste(text, at, s)
      apply?.(p.cells, p.rows, p.cols)
      setSel({ a: at, b: p.end })
    }
  })
  // Clipboard events go to the document while the grid (no text field) has the focus.
  useEffect(() => {
    const on = (e: ClipboardEvent) => document.activeElement === gridRef.current && onClip.current?.(e)
    const types = ['copy', 'cut', 'paste'] as const
    for (const n of types) document.addEventListener(n, on)
    return () => {
      for (const n of types) document.removeEventListener(n, on)
    }
  }, [])
  useEffect(() => {
    if (apply) focusGrid()
  }, [])
  useEffect(() => {
    edit(null)
  }, [s.loaded, ro])
  // The corner moved shows; the active cell when both moved.
  const show = (k: string) => {
    document.getElementById(uid + k)?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }
  useLayoutEffect(() => show(bKey), [uid, bKey])
  useLayoutEffect(() => show(key), [uid, key])

  const input = editing && !editing.bar && (
    <input ref={focusEnd} className="sheet__edit" value={editing.text} maxLength={MAX_CELL_CHARS} aria-label={contents}
      aria-invalid={bad} autoComplete="off" spellCheck={false} onChange={(e) => edit({ ...editing, text: e.target.value })}
      onBlur={commit} />
  )
  const rows: ReactNode[] = []
  for (let r = 0; r < s.rows; r++) {
    const on = r >= rect.r0 && r <= rect.r1
    rows.push((r < top - 30 || r >= top + AHEAD) && r !== a.r && r !== b.r ? <div key={r} className="sheet__gap" /> : (
      <Row key={r} r={r} cols={s.cols} shown={s.shown} uid={uid} c0={on ? rect.c0 : -1} c1={on ? rect.c1 : -1}
        active={r === a.r ? a.c : -1} edit={r === a.r ? input : null} />
    ))
  }
  return (
    <div className="sheet" onKeyDown={onKey}>
      <div className="sheet__bar">
        <span className="sheet__ref" aria-hidden="true">{key}</span>
        <input className="sheet__input" aria-label={contents} value={editing ? editing.text : s.raws[key] ?? ''}
          readOnly={ro} maxLength={MAX_CELL_CHARS} aria-invalid={bad} autoComplete="off" spellCheck={false}
          onChange={(e) => edit({ at: a, text: e.target.value, bar: true })} onBlur={commit} />
      </div>
      {bad && <p className="sheet__bad" role="alert">{t('sheet.badFormula')}</p>}
      <div ref={gridRef} role="grid" tabIndex={0} className="sheet__grid" aria-label={label} aria-readonly={ro}
        aria-multiselectable="true" aria-rowcount={s.rows + 1} aria-colcount={s.cols + 1}
        aria-activedescendant={input ? undefined : uid + key}
        onScroll={(e) => setTop(Math.floor(e.currentTarget.scrollTop / ROW_PX / 10) * 10)}
        onMouseDown={onMouseDown}
        onMouseOver={(e) => {
          const p = e.buttons === 1 && document.activeElement === gridRef.current && cellAt(e.target as Element)
          if (p && (p.r !== b.r || p.c !== b.c)) select(p, true)
        }}
        onDoubleClick={(e) => cellAt(e.target as Element) && begin(s.raws[key] ?? '')}>
        <div role="row" aria-rowindex={1} className="sheet__row sheet__head">
          <div role="columnheader" />
          {Array.from({ length: s.cols }, (_, c) => <div key={c} role="columnheader">{colName(c)}</div>)}
        </div>
        {rows}
      </div>
    </div>
  )
}

const Row = memo(function Row({ r, cols, shown, uid, c0, c1, active, edit }: {
  r: number
  cols: number
  shown: Record<string, Shown>
  uid: string
  c0: number
  c1: number
  active: number
  edit: ReactNode
}) {
  const cells: ReactNode[] = []
  for (let c = 0; c < cols; c++) {
    const k = cellKey(r, c)
    const v = shown[k]
    cells.push(
      <div key={c} role="gridcell" id={uid + k} data-c={c} aria-selected={c >= c0 && c <= c1}
        className={'sheet__cell' + (v?.look ? ' sheet__cell--' + v.look : '') + (c === active ? ' sheet__cell--active' : '')}
        title={v && v.v.length > 12 ? v.v : undefined}>
        {c === active && edit ? edit : v?.v}
      </div>,
    )
  }
  return (
    <div role="row" aria-rowindex={r + 2} data-r={r} className="sheet__row">
      <div role="rowheader">{r + 1}</div>
      {cells}
    </div>
  )
})
