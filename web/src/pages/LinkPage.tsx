import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { api, ApiError, linkContent, linkZipUrl, type LinkPage as Page, type Node, type PublicNode } from '../api'
import { ContentUrlContext } from '../components/content'
import { errorText } from '../components/Dialogs'
import { Icon, icons } from '../components/Icon'
import { Empty, Grid, PageHead, type Crumb } from '../components/Items'
import { Logo } from '../components/Logo'
import { Viewer } from '../components/Viewer'
import { useT } from '../i18n'
import { linkPath, navigate } from '../router'
import { AuthLayout, Field } from './Login'

type State =
  | { kind: 'loading' }
  | { kind: 'page'; page: Page }
  | { kind: 'locked' }
  | { kind: 'dead' }
  | { kind: 'error'; err: unknown }

// toNode lets the drive's components show what a visitor sees.
const toNode = (p: PublicNode): Node => ({ ...p, driveId: '', starred: false, createdAt: p.modifiedAt })

// LinkPage is what someone with a public link sees at /s/<token>: one
// file in the viewer, or a folder to browse and download. It never shows
// who shared it. Folders inside the link are ?n=<id>.
export function LinkPage({ token, nodeId }: { token: string; nodeId: string | null }) {
  const t = useT()
  const content = useMemo(() => linkContent(token), [token])
  const [state, setState] = useState<State>({ kind: 'loading' })
  const [viewing, setViewing] = useState<number | null>(null)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let live = true
    setViewing(null)
    api.linkPage(token, nodeId ?? undefined).then(
      (page) => live && setState({ kind: 'page', page }),
      (err) => {
        if (!live) return
        const code = err instanceof ApiError ? err.code : ''
        // A folder that left the link (moved or trashed): back to its top.
        if (code === 'not_found' && nodeId) navigate(linkPath(token), true)
        else if (code === 'link_locked') setState({ kind: 'locked' })
        else if (code === 'link_dead' || code === 'not_found') setState({ kind: 'dead' })
        else setState({ kind: 'error', err })
      },
    )
    return () => {
      live = false
    }
  }, [token, nodeId, attempt])

  const name = state.kind === 'page' ? state.page.node.name : ''
  useEffect(() => {
    if (!name) return
    document.title = `${name} – ${t('app.name')}`
    return () => {
      document.title = t('app.name')
    }
  }, [name, t])

  if (state.kind === 'loading') {
    return (
      <div className="splash">
        <Logo size={48} />
      </div>
    )
  }
  if (state.kind === 'locked') return <Unlock token={token} onUnlocked={() => setAttempt((a) => a + 1)} />
  if (state.kind === 'dead') {
    return (
      <AuthLayout>
        <span className="auth__mail">
          <Icon d={icons.link} size={24} />
        </span>
        <h1>{t('link.deadTitle')}</h1>
        <p>{t('link.deadBody')}</p>
      </AuthLayout>
    )
  }
  if (state.kind === 'error') {
    return (
      <AuthLayout>
        <h1>{t('link.errorTitle')}</h1>
        <p role="alert">{errorText(t, state.err)}</p>
        <button type="button" className="btn btn--primary btn--block" onClick={() => {
          setState({ kind: 'loading' })
          setAttempt((a) => a + 1)
        }}>
          {t('link.retry')}
        </button>
      </AuthLayout>
    )
  }

  const { page } = state
  const node = toNode(page.node)
  if (node.kind === 'file') {
    return (
      <ContentUrlContext value={content}>
        <Viewer files={[node]} index={0} location="" onIndex={() => {}} showInfo={false} />
      </ContentUrlContext>
    )
  }

  const items = (page.children ?? []).map(toNode)
  const files = items.filter((n) => n.kind === 'file')
  const crumbs: Crumb[] = page.path.length
    ? [...page.path, { id: node.id, name: node.name }].map((c, i) => ({
        name: c.name,
        href: i === 0 ? linkPath(token) : linkPath(token, c.id),
      }))
    : []

  return (
    <ContentUrlContext value={content}>
      <div className="app visitor">
        <header className="app-header">
          <div className="app-header__inner">
            <span className="app-header__brand">
              <Logo label={t('app.name')} />
              <span>{t('app.name')}</span>
            </span>
          </div>
        </header>
        <div className="app-body">
          <main className="main">
            <PageHead crumbs={crumbs} title={node.name} count={items.length}
              actions={items.length > 0 && (
                <a className="btn btn--small btn--primary" href={linkZipUrl(token, node.id)} download>
                  <Icon d={icons.download} size={16} />
                  {t('link.downloadAll')}
                </a>
              )} />
            <div className="files-row">
              <section aria-label={node.name} className="files">
                {items.length === 0 ? (
                  <Empty icon={icons.folder} title={t('empty.folder.title')} body={t('link.emptyBody')} />
                ) : (
                  <Grid items={items} selected={null}
                    onOpen={(n) => {
                      if (n.kind === 'folder') navigate(linkPath(token, n.id))
                      else setViewing(files.findIndex((f) => f.id === n.id))
                    }} />
                )}
              </section>
            </div>
          </main>
        </div>
        {viewing !== null && files[viewing] && (
          <Viewer files={files} index={viewing} location="" onIndex={setViewing} onClose={() => setViewing(null)}
            showInfo={false} />
        )}
      </div>
    </ContentUrlContext>
  )
}

// Unlock asks for the link's password. The server answers with a cookie
// that lets this browser in for 12 hours.
function Unlock({ token, onUnlocked }: { token: string; onUnlocked: () => void }) {
  const t = useT()
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const form = useRef<HTMLFormElement>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.unlockLink(token, password)
      onUnlocked()
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
      form.current?.querySelector('input')?.select()
    }
  }

  return (
    <AuthLayout>
      <span className="auth__mail">
        <Icon d={icons.lock} size={24} />
      </span>
      <h1>{t('link.lockedTitle')}</h1>
      <p>{t('link.lockedBody')}</p>
      <form ref={form} onSubmit={submit}>
        <Field label={t('link.password')} type="password" autoComplete="off" required autoFocus value={password}
          onChange={(e) => setPassword(e.target.value)} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <button type="submit" className="btn btn--primary btn--block" disabled={busy || !password}>
          {t('link.unlock')}
        </button>
      </form>
    </AuthLayout>
  )
}
