// The editors' logic without React: line endings, comparing markdown, and
// the edit session (lock, autosave, retries). No imports, so node --test
// can load it.

// TextFormat is how a file's text is written back: Windows line endings
// when most of its lines had them, its byte order mark, and (for markdown,
// which the page editor writes without one) its final newline.
export interface TextFormat {
  crlf: boolean
  bom: boolean
  finalNewline: boolean
}

export function readText(raw: string): { text: string; format: TextFormat } {
  const bom = raw.startsWith('\uFEFF')
  const body = bom ? raw.slice(1) : raw
  const crlf = (body.match(/\r\n/g)?.length ?? 0) > (body.match(/(?<!\r)\n/g)?.length ?? 0)
  return { text: body.replace(/\r\n?/g, '\n'), format: { crlf, bom, finalNewline: !body || body.endsWith('\n') } }
}

export function writeText(text: string, format: TextFormat): string {
  return (format.bom ? '\uFEFF' : '') + (format.crlf ? text.replace(/\n/g, '\r\n') : text)
}

// markdownText gives the page editor's markdown the file's final newline.
export function markdownText(md: string, format: TextFormat): string {
  const body = md.replace(/\n+$/, '')
  return body && format.finalNewline ? body + '\n' : body
}

// sameMarkdown compares markdown ignoring line endings and final newlines.
export function sameMarkdown(a: string, b: string): boolean {
  const norm = (s: string) => s.replace(/\r\n?/g, '\n').replace(/\n+$/, '')
  return norm(a) === norm(b)
}

export const backoff = (attempt: number) => Math.min(30_000, 1000 * 2 ** attempt)

// Links in documents may only go to web pages and e-mail addresses.
export function safeUrl(url: string): boolean {
  try {
    return ['http:', 'https:', 'mailto:'].includes(new URL(url).protocol)
  } catch {
    return false
  }
}

// linkHref completes an address typed for a link (example.com, a@b.org);
// it is '' when that may not be linked.
export function linkHref(input: string): string {
  const s = input.trim()
  const href = /^[a-z][a-z\d+.-]*:/i.test(s) ? s : /^[^\s@/:]+@[^\s@/:]+\.[^\s@/:]+$/.test(s) ? 'mailto:' + s : 'https://' + s
  return s && !/\s/.test(s) && safeUrl(href) ? href : ''
}

export interface MdToken {
  type: string
  href?: string
  tokens?: MdToken[]
  items?: MdToken[]
}

// pageUnsafe finds what the page editor must not show formatted (marked's
// tokens): HTML, images, and links other than to web pages and e-mail.
export function pageUnsafe(tokens: MdToken[]): boolean {
  return tokens.some(
    (t) =>
      t.type === 'html' ||
      t.type === 'image' ||
      (t.type === 'link' && !safeUrl(t.href ?? '')) ||
      pageUnsafe(t.tokens ?? []) ||
      pageUnsafe(t.items ?? []),
  )
}

export type SaveStatus =
  | { kind: 'loading' }
  | { kind: 'saving' }
  | { kind: 'saved' }
  // retrying: tried again by itself (network trouble, busy server).
  | { kind: 'error'; code: string; retrying: boolean }
  // The file can't be changed any more: lock_lost, locked, locked_self,
  // not_found (trashed), forbidden.
  | { kind: 'readonly'; code: string; holder?: string }
  | { kind: 'conflict'; by?: string; at?: string }

export interface SessionIO<N, P> {
  // load fetches the file's details: their version is what saves replace.
  load(): Promise<N>
  take(): Promise<string>
  renew(lockId: string): Promise<void>
  release(lockId: string): void
  save(lockId: string, version: string, payload: P): Promise<N>
}

// A payload may be a function, called only when it's saved, for editors
// whose content is costly to write out on every change.
export type Payload<P> = P | (() => P)

interface Failure {
  status: number
  code: string
  body: Record<string, unknown>
}

function failure(err: unknown): Failure {
  const e = (err ?? {}) as Partial<Failure>
  return {
    status: typeof e.status === 'number' ? e.status : -1,
    code: typeof e.code === 'string' ? e.code : 'internal',
    body: e.body ?? {},
  }
}

const text = (v: unknown) => (typeof v === 'string' && v ? v : undefined)
const retryable = (status: number) => status === 0 || status === 429 || status >= 500

// Session edits one file: it holds its lock (renewed while open, released
// when closed), saves changes a moment after the last one, one
// request at a time, and tries again after network trouble without losing
// changes. A lost lock is taken again once (the version check keeps others'
// changes safe); after that the file is read-only.
export class Session<N extends { version?: string }, P> {
  status: SaveStatus = { kind: 'loading' }
  node: N
  lockId: string | undefined
  // revision counts loads of the file's content; editors read it again when
  // it changes (0: not loaded yet).
  revision = 0

  private io: SessionIO<N, P>
  private update: () => void
  private delays: { save: number; renew: number }
  private pending: { payload: Payload<P> } | null = null
  private saving = false
  private stale = true
  private closed = false
  private busy: Promise<void> | null = null
  private timer: ReturnType<typeof setTimeout> | undefined
  private retryTimer: ReturnType<typeof setTimeout> | undefined
  private renewTimer: ReturnType<typeof setTimeout> | undefined
  private attempt = 0
  private relocks = 0

  constructor(
    node: N,
    lockId: string | undefined,
    io: SessionIO<N, P>,
    update: () => void,
    delays = { save: 700, renew: 30_000 },
  ) {
    this.node = node
    this.lockId = lockId
    this.io = io
    this.update = update
    this.delays = delays
    if (lockId) this.renewSoon()
    this.pump()
  }

  get readOnly(): boolean {
    return !['saving', 'saved', 'error'].includes(this.status.kind)
  }

  // dirty: there are changes the server doesn't have yet.
  get dirty(): boolean {
    return !!this.pending || this.saving
  }

  // unsaved gives the changes not saved, such as after the lock was lost.
  unsaved(): P | undefined {
    return this.pending ? this.resolve(this.pending) : undefined
  }

  change(payload: Payload<P>) {
    if (this.readOnly || this.closed) return
    this.pending = { payload }
    this.set({ kind: 'saving' })
    clearTimeout(this.timer)
    this.timer = setTimeout(() => {
      this.timer = undefined
      this.pump()
    }, this.delays.save)
  }

  // flush saves now and waits; it tells whether everything was saved.
  async flush(): Promise<boolean> {
    clearTimeout(this.timer)
    this.timer = undefined
    if (this.pending) this.resolve(this.pending)
    this.pump()
    while (this.busy) await this.busy
    return !this.pending
  }

  retry() {
    this.attempt = 0
    if (this.status.kind === 'error') this.pump()
  }

  // reload drops the unsaved changes and reads the file again.
  reload() {
    this.pending = null
    this.stale = true
    this.set({ kind: 'loading' })
    this.pump()
  }

  // hide saves what is waiting: a page that is hidden may never come back.
  // The lock stays: it lapses by itself, and its holder can still use it
  // until someone else takes the file.
  async hide() {
    if (this.dirty) await this.flush()
  }

  show() {
    this.pump()
  }

  // leave is for a page going away: the lock is given back, so others
  // needn't wait for it to lapse. If the page returns, show takes it again.
  async leave() {
    if (this.dirty) await this.flush()
    if (!this.dirty) this.drop()
  }

  async dispose() {
    this.closed = true
    if (this.dirty) await this.flush()
    clearTimeout(this.retryTimer)
    this.drop()
  }

  private resolve(p: { payload: Payload<P> }): P {
    if (typeof p.payload === 'function') p.payload = (p.payload as () => P)()
    return p.payload as P
  }

  private set(s: SaveStatus) {
    if (JSON.stringify(s) === JSON.stringify(this.status)) return
    this.status = s
    this.update()
  }

  private next(): (() => Promise<void>) | null {
    const stuck = this.status.kind === 'readonly' || this.status.kind === 'conflict'
    if (!stuck && !this.lockId && (this.pending || !this.closed)) return () => this.lock()
    if (this.stale && !this.closed && (stuck || this.lockId)) return () => this.load()
    if (!stuck && this.lockId && this.pending && !this.timer) return () => this.save()
    return null
  }

  // pump starts the work there is, unless a request is out already: run
  // picks up what changed meanwhile when it's done.
  private pump() {
    if (this.busy || !this.next()) return
    clearTimeout(this.retryTimer)
    this.busy = this.run().finally(() => (this.busy = null))
  }

  private async run() {
    for (let job = this.next(); job; job = this.next()) {
      try {
        await job()
      } catch (err) {
        if (!this.fail(err)) break
      }
    }
    if (this.status.kind === 'saving' && !this.pending && !this.timer) this.set({ kind: 'saved' })
  }

  private async lock() {
    const id = await this.io.take()
    if (this.closed && !this.pending) return this.io.release(id)
    this.lockId = id
    this.renewSoon()
  }

  private async load() {
    this.stale = false
    const n = await this.io.load().catch((err) => {
      this.stale = retryable(failure(err).status)
      throw err
    })
    // With changes waiting, the version they started from stays: if the file
    // changed meanwhile, saving them is a conflict rather than an overwrite.
    if (!this.pending) {
      if (!this.revision || this.status.kind === 'loading' || n.version !== this.node.version) this.revision++
      this.node = n
    }
    if (this.status.kind === 'loading') this.set({ kind: 'saved' })
    this.update()
  }

  private async save() {
    const p = this.pending!
    this.pending = null
    this.saving = true
    this.set({ kind: 'saving' })
    try {
      this.node = await this.io.save(this.lockId!, this.node.version ?? '', this.resolve(p))
      this.attempt = this.relocks = 0
      this.update()
    } catch (err) {
      this.pending ??= p
      throw err
    } finally {
      this.saving = false
    }
  }

  // fail tells whether to go on: with the lock taken again, or to load the
  // file read-only.
  private fail(err: unknown): boolean {
    const { status, code, body } = failure(err)
    if (code === 'lock_lost') {
      this.lost()
      if (this.relocks++ < 1) return true
    }
    if (code === 'edit_conflict') this.set({ kind: 'conflict', by: text(body.by), at: text(body.at) })
    else if (retryable(status)) {
      this.set({ kind: 'error', code, retrying: !this.closed })
      if (!this.closed) this.retryTimer = setTimeout(() => this.pump(), backoff(this.attempt++))
    } else if ([401, 403, 404, 409].includes(status)) {
      this.set({ kind: 'readonly', code, holder: text(body.holder) })
      return true
    } else this.set({ kind: 'error', code, retrying: false })
    return false
  }

  private lost() {
    clearTimeout(this.renewTimer)
    this.lockId = undefined
    this.stale = true
  }

  private drop() {
    if (this.lockId) this.io.release(this.lockId)
    this.lost()
  }

  private renewSoon() {
    clearTimeout(this.renewTimer)
    const id = this.lockId
    this.renewTimer = setTimeout(async () => {
      try {
        await this.io.renew(id!)
        this.relocks = 0
      } catch (err) {
        if (id === this.lockId && failure(err).code === 'lock_lost') {
          this.lost()
          if (this.relocks++ >= 1) this.set({ kind: 'readonly', code: 'lock_lost' })
          return this.pump()
        }
      }
      if (id === this.lockId) this.renewSoon()
    }, this.delays.renew)
  }
}
