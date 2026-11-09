import { useEffect, useState } from 'react'
import { ApiError, contentUrl, type Node } from '../api'
import { errorText } from '../components/Dialogs'
import { useT } from '../i18n'
import { readText, writeText, type TextFormat } from './session'
import type { EditorBodyProps, Session } from './useEditSession'

export type FileText = { text: string; format: TextFormat } | { error: unknown } | null

async function readFile(id: string) {
  const r = await fetch(contentUrl(id), { credentials: 'same-origin', cache: 'no-store' }).catch(() => null)
  if (!r) throw new ApiError(0, 'network')
  if (!r.ok) throw new ApiError(r.status, (await r.json().catch(() => ({}))).error ?? 'internal')
  const bytes = await r.arrayBuffer().catch(() => {
    throw new ApiError(0, 'network')
  })
  let raw: string
  try {
    raw = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes)
  } catch {
    throw new ApiError(400, 'invalid_text')
  }
  if (raw.includes('\0')) throw new ApiError(400, 'invalid_text')
  return readText(raw)
}

// useFileText reads the file being edited (UTF-8 only), and again whenever
// the session loads a version it didn't write itself.
export function useFileText(session: Session<Node, string>): FileText {
  const [file, setFile] = useState<FileText>(null)
  const { revision } = session
  const id = session.node.id
  useEffect(() => {
    if (!revision) return
    let live = true
    setFile(null)
    readFile(id).then(
      (f) => live && setFile(f),
      (error) => live && setFile({ error }),
    )
    return () => {
      live = false
    }
  }, [id, revision])
  return file
}

// SourceArea is the plain text editor, also the Markdown editor's .md view.
export function SourceArea({ session, text, label, onChange }: {
  session: Session<Node, string>
  text: string
  label: string
  onChange: (text: string) => void
}) {
  const code = session.node.type !== 'text'
  return (
    <textarea className="editor__source" aria-label={label} defaultValue={text} readOnly={session.readOnly}
      spellCheck={!code} wrap={code ? 'off' : 'soft'} autoComplete="off" autoCapitalize="off" autoFocus
      onChange={(e) => onChange(e.target.value)} />
  )
}

// FileState stands in for the file while it loads or when it can't be read.
export function FileState({ file, session }: { file: object | null; session: { reload(): void } }) {
  const t = useT()
  if (!file || !('error' in file)) return <p className="editor__empty">{t('loading')}</p>
  return (
    <div className="editor__empty" role="alert">
      <p>{errorText(t, file.error)}</p>
      <button type="button" className="btn btn--small" onClick={() => session.reload()}>{t('editor.retry')}</button>
    </div>
  )
}

export default function TextEditor({ session }: EditorBodyProps<string>) {
  const t = useT()
  const file = useFileText(session)
  if (!file || 'error' in file) return <FileState file={file} session={session} />
  return (
    <div className="editor__page editor__page--source">
      <SourceArea session={session} text={file.text} label={t('editor.textArea')}
        onChange={(s) => session.change(writeText(s, file.format))} />
    </div>
  )
}
