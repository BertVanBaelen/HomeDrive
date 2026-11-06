import { Editor as TipTap } from '@tiptap/core'
import { Markdown } from '@tiptap/markdown'
import StarterKit from '@tiptap/starter-kit'
import { useEffect, useId, useReducer, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Modal } from '../components/Dialogs'
import { Icon, icons } from '../components/Icon'
import { useT, type MessageKey } from '../i18n'
import { linkHref, markdownText, pageUnsafe, safeUrl, sameMarkdown, writeText, type MdToken } from './session'
import { FileState, SourceArea, useFileText } from './TextEditor'
import type { EditorBodyProps } from './useEditSession'

const paths = {
  undo: 'M3 10h10a5 5 0 015 5v2M3 10l5-5M3 10l5 5',
  redo: 'M21 10H11a5 5 0 00-5 5v2m15-7l-5-5m5 5l-5 5',
  bullets: 'M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01',
  numbers: 'M10 6h10M10 12h10M10 18h10M4 6h1v4M4 10h2M6 18H4c0-1 2-2 2-3s-1-1.5-2-1',
  quote: 'M7 8h4v4c0 2-1 3.5-3 4M14 8h4v4c0 2-1 3.5-3 4',
}

// What the page editor knows: no images, no HTML, no strike-through or
// underline (Markdown has none), and links to web pages and e-mail only.
const extensions = [
  StarterKit.configure({
    heading: { levels: [1, 2, 3] },
    strike: false,
    underline: false,
    trailingNode: false,
    link: {
      openOnClick: false,
      autolink: false,
      protocols: [],
      defaultProtocol: 'https',
      isAllowedUri: (url) => safeUrl(url),
      HTMLAttributes: { target: '_blank', rel: 'noopener noreferrer nofollow' },
    },
  }),
  Markdown,
]

// pageSafe tells whether the page editor shows md as it is: the Markdown it
// writes back is the same, and there is nothing it must not format.
function pageSafe(editor: TipTap, md: string): boolean {
  return !pageUnsafe(editor.markdown!.instance.lexer(md) as unknown as MdToken[]) && sameMarkdown(editor.getMarkdown(), md)
}

type Mode = 'page' | 'source' | 'forced'

// MarkdownEditor edits Markdown on a page, or as its source (.md). A file
// the page editor would change opens as source and stays there.
export default function MarkdownEditor({ session, header }: EditorBodyProps<string>) {
  const t = useT()
  const file = useFileText(session)
  const [mode, setMode] = useState<Mode>('page')
  const [editor, setEditor] = useState<TipTap | null>(null)
  const [linking, setLinking] = useState(false)
  const [, redraw] = useReducer((n: number) => n + 1, 0)
  const host = useRef<HTMLDivElement>(null)
  // text is the Markdown as last shown in the source view (or loaded); read
  // gives the latest, also from the page editor.
  const text = useRef('')
  const read = useRef(() => text.current)
  const seen = useRef(file)
  if (seen.current !== file) {
    seen.current = file
    if (file && 'text' in file) text.current = file.text
  }
  const loaded = file && 'text' in file ? file : null

  useEffect(() => {
    if (!loaded || mode !== 'page') return
    const fmt = loaded.format
    const ed = new TipTap({
      element: null,
      extensions,
      content: text.current,
      contentType: 'markdown',
      injectCSS: false,
      editable: !session.readOnly,
      editorProps: {
        attributes: { class: 'editor__rich', role: 'textbox', 'aria-multiline': 'true', 'aria-label': t('editor.docText') },
      },
    })
    if (!pageSafe(ed, text.current)) {
      ed.destroy()
      setMode('forced')
      return
    }
    ed.mount(host.current!)
    read.current = () => ed.getMarkdown()
    ed.on('update', () => session.change(() => writeText(markdownText(read.current(), fmt), fmt)))
    ed.on('transaction', redraw)
    setEditor(ed)
    return () => {
      // Changes not saved yet are written out from what was on the page; a
      // new page editor starts from it too, unless the file was read again.
      const last = ed.getMarkdown()
      read.current = () => last
      if (seen.current === loaded) text.current = markdownText(last, fmt)
      setEditor(null)
      ed.destroy()
    }
  }, [loaded, mode, session, t])

  useEffect(() => editor?.setEditable(!session.readOnly, false), [editor, session.readOnly])

  if (!loaded) return <FileState file={file} session={session} />
  const fmt = loaded.format
  const toggle = header && createPortal(
    <div className="editor__modes" role="group" aria-label={t('editor.modes')}>
      <button type="button" aria-pressed={mode === 'page'} disabled={mode === 'forced'} onClick={() => setMode('page')}>
        {t('editor.page')}
      </button>
      <button type="button" aria-pressed={mode !== 'page'} aria-label={t('editor.sourceLabel')}
        onClick={() => {
          if (mode !== 'page') return
          text.current = markdownText(read.current(), fmt)
          setMode('source')
        }}>
        {t('editor.source')}
      </button>
    </div>,
    header,
  )

  if (mode !== 'page') {
    return (
      <div className="editor__page editor__page--source">
        {toggle}
        {mode === 'forced' && <p className="editor__note">{t('editor.sourceOnly')}</p>}
        <SourceArea session={session} text={text.current} label={t('editor.sourceLabel')}
          onChange={(s) => {
            text.current = s
            session.change(writeText(s, fmt))
          }} />
      </div>
    )
  }
  return (
    <>
      {toggle}
      {editor && <Toolbar editor={editor} onLink={() => setLinking(true)} />}
      <div className="editor__scroll">
        <div ref={host} className="editor__paper" />
      </div>
      {linking && editor && <LinkDialog editor={editor} onClose={() => setLinking(false)} />}
    </>
  )
}

function Toolbar({ editor, onLink }: { editor: TipTap; onLink: () => void }) {
  const t = useT()
  const go = () => editor.chain().focus()
  const off = !editor.isEditable
  const is = (name: string, attrs?: object) => editor.isActive(name, attrs)
  const tools: ({ label: MessageKey; glyph: ReactNode; run: () => void; on?: boolean; can?: boolean } | null)[] = [
    { label: 'editor.undo', glyph: <Icon d={paths.undo} size={18} />, run: () => go().undo().run(), can: editor.can().undo() },
    { label: 'editor.redo', glyph: <Icon d={paths.redo} size={18} />, run: () => go().redo().run(), can: editor.can().redo() },
    null,
    { label: 'editor.h1', glyph: 'H1', run: () => go().toggleHeading({ level: 1 }).run(), on: is('heading', { level: 1 }) },
    { label: 'editor.h2', glyph: 'H2', run: () => go().toggleHeading({ level: 2 }).run(), on: is('heading', { level: 2 }) },
    { label: 'editor.paragraph', glyph: <span className="editor__serif">T</span>, run: () => go().setParagraph().run(), on: is('paragraph') },
    null,
    { label: 'editor.bold', glyph: <b>B</b>, run: () => go().toggleBold().run(), on: is('bold') },
    { label: 'editor.italic', glyph: <i className="editor__serif">I</i>, run: () => go().toggleItalic().run(), on: is('italic') },
    null,
    { label: 'editor.bullets', glyph: <Icon d={paths.bullets} size={18} />, run: () => go().toggleBulletList().run(), on: is('bulletList') },
    { label: 'editor.numbers', glyph: <Icon d={paths.numbers} size={18} />, run: () => go().toggleOrderedList().run(), on: is('orderedList') },
    { label: 'editor.quote', glyph: <Icon d={paths.quote} size={18} />, run: () => go().toggleBlockquote().run(), on: is('blockquote') },
    { label: 'editor.link', glyph: <Icon d={icons.link} size={18} />, run: onLink, on: is('link') },
  ]
  return (
    // Buttons keep the focus (and selection) in the text.
    <div className="editor__toolbar" role="toolbar" aria-label={t('editor.toolbar')} onMouseDown={(e) => e.preventDefault()}>
      {tools.map((b, i) =>
        b ? (
          <button key={b.label} type="button" className="editor__tool" aria-label={t(b.label)} title={t(b.label)}
            aria-pressed={b.on} disabled={off || b.can === false} onClick={b.run}>
            {b.glyph}
          </button>
        ) : (
          <span key={i} className="editor__sep" aria-hidden="true" />
        ),
      )}
    </div>
  )
}

function LinkDialog({ editor, onClose }: { editor: TipTap; onClose: () => void }) {
  const t = useT()
  const id = useId()
  const was = editor.getAttributes('link').href as string | undefined
  const [url, setUrl] = useState(was ?? '')
  const [error, setError] = useState('')
  function save(e: React.FormEvent) {
    e.preventDefault()
    const href = linkHref(url)
    if (!href) return setError(t('editor.linkInvalid'))
    const go = editor.chain().focus().extendMarkRange('link')
    if (editor.state.selection.empty && !was) go.insertContent({ type: 'text', text: href, marks: [{ type: 'link', attrs: { href } }] }).run()
    else go.setLink({ href }).run()
    onClose()
  }
  return (
    <Modal title={t('editor.link')} onClose={onClose}>
      <form className="field" onSubmit={save}>
        <label htmlFor={id}>{t('editor.linkUrl')}</label>
        <input id={id} value={url} onChange={(e) => setUrl(e.target.value)} inputMode="url"
          autoComplete="off" spellCheck={false} placeholder="https://" />
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="modal__foot">
          {was && (
            <button type="button" className="btn" onClick={() => {
              editor.chain().focus().extendMarkRange('link').unsetLink().run()
              onClose()
            }}>
              {t('editor.linkRemove')}
            </button>
          )}
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="submit" className="btn btn--primary">{t('save.save')}</button>
        </div>
      </form>
    </Modal>
  )
}
