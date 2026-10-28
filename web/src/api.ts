// Typed access to Homedrive's JSON API (server/internal/server). Errors carry
// the server's stable error code, which the UI translates.

export type FileType = 'folder' | 'text' | 'sheet' | 'doc' | 'pdf' | 'image' | 'video' | 'audio' | 'archive' | 'code'

export interface User {
  id: string
  email: string
  name: string
  role: 'admin' | 'member'
  locale: string
}

export interface Drive {
  id: string
  kind: 'personal' | 'shared'
  name: string
  rootId: string
  role: 'viewer' | 'editor' | 'manager'
}

export interface Node {
  id: string
  driveId: string
  parentId?: string
  kind: 'folder' | 'file'
  type: FileType
  name: string
  size: number
  mime?: string
  createdAt: string
  modifiedAt: string
  trashedAt?: string
  childCount?: number
  starred: boolean
}

export interface Crumb {
  id: string
  name: string
}

export interface Details {
  node: Node
  drive: Drive
  path: Crumb[]
}

// Located is a node listed outside its folder, with where it lives.
export interface Located extends Node {
  drive: Drive
  path: Crumb[]
}

export interface UserInfo extends User {
  createdAt: string
  status: 'active' | 'invited' | 'disabled'
  lastSeenAt?: string
}

export interface SessionInfo {
  id: string
  createdAt: string
  lastSeenAt: string
  userAgent: string
  ip: string
  current: boolean
}

export interface PasswordLink {
  email: string
  name: string
  purpose: 'invite' | 'reset'
}

export interface Upload {
  id: string
  parentId: string
  name: string
  size: number
  received: number
  chunkSize: number
}

export type SortKey = 'name' | 'modified' | 'size'

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly body: Record<string, unknown> = {},
  ) {
    super(code)
  }
}

// Called when any request finds the session gone, so the app can show the
// sign-in page.
let onUnauthenticated: () => void = () => {}
export function setUnauthenticatedHandler(fn: () => void) {
  onUnauthenticated = fn
}

// Sensitive actions answer reauth_required when the password wasn't entered
// recently. The app asks for it (resolving true once confirmed) and the
// request is sent again.
let onReauth: () => Promise<boolean> = async () => false
export function setReauthHandler(fn: () => Promise<boolean>) {
  onReauth = fn
}

// Requests that need the password at the same time share one prompt.
let reauthPrompt: Promise<boolean> | null = null

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  try {
    return await send<T>(method, path, body)
  } catch (err) {
    if (!(err instanceof ApiError && err.code === 'reauth_required')) throw err
    reauthPrompt ??= onReauth().finally(() => (reauthPrompt = null))
    if (!(await reauthPrompt)) throw err
    return send<T>(method, path, body)
  }
}

async function send<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method,
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
    })
  } catch {
    throw new ApiError(0, 'network')
  }
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const code = typeof data?.error === 'string' ? data.error : 'internal'
    if (res.status === 401 && code === 'unauthenticated') onUnauthenticated()
    throw new ApiError(res.status, code, data)
  }
  return data as T
}

export const api = {
  me: () => request<User>('GET', '/api/me'),
  login: (email: string, password: string) =>
    request<{ challenge: string }>('POST', '/api/auth/login', { email, password }),
  verify: (challenge: string, code: string) => request<User>('POST', '/api/auth/verify', { challenge, code }),
  logout: () => request<void>('POST', '/api/auth/logout'),

  setupCheck: (token: string) => request<void>('POST', '/api/setup/check', { token }),
  setupBegin: (p: { token: string; email: string; name: string; password: string; locale: string }) =>
    request<void>('POST', '/api/setup/begin', p),
  setupFinish: (token: string, code: string) => request<User>('POST', '/api/setup/finish', { token, code }),

  drives: () => request<Drive[]>('GET', '/api/drives'),
  node: (id: string) => request<Details>('GET', `/api/nodes/${encodeURIComponent(id)}`),
  children: (id: string, sort: SortKey, desc: boolean) =>
    request<Node[]>('GET', `/api/nodes/${encodeURIComponent(id)}/children?sort=${sort}&order=${desc ? 'desc' : 'asc'}`),
  createFolder: (parentId: string, name: string) => request<Node>('POST', '/api/folders', { parentId, name }),
  rename: (id: string, name: string) => request<Node>('PATCH', `/api/nodes/${encodeURIComponent(id)}`, { name }),
  move: (id: string, parentId: string) => request<Node>('PATCH', `/api/nodes/${encodeURIComponent(id)}`, { parentId }),
  star: (id: string, starred: boolean) => request<Node>('PATCH', `/api/nodes/${encodeURIComponent(id)}`, { starred }),
  trash: (id: string) => request<void>('DELETE', `/api/nodes/${encodeURIComponent(id)}`),
  restore: (id: string) => request<Node>('POST', `/api/nodes/${encodeURIComponent(id)}/restore`),
  trashList: () => request<Located[]>('GET', '/api/trash'),
  starred: () => request<Located[]>('GET', '/api/starred'),
  recent: () => request<Located[]>('GET', '/api/recent'),
  search: (q: string) => request<Located[]>('GET', `/api/search?q=${encodeURIComponent(q)}`),

  reauth: (password: string) => request<void>('POST', '/api/auth/reauth', { password }),
  updateMe: (p: { name?: string; locale?: string }) => request<User>('PATCH', '/api/me', p),
  changePassword: (current: string, password: string) =>
    request<void>('POST', '/api/me/password', { current, password }),
  sessions: () => request<SessionInfo[]>('GET', '/api/me/sessions'),
  revokeSession: (id: string) => request<void>('DELETE', `/api/me/sessions/${encodeURIComponent(id)}`),

  users: () => request<UserInfo[]>('GET', '/api/admin/users'),
  inviteUser: (p: { email: string; name: string; role: string; locale: string }) =>
    request<{ user: UserInfo; error?: string }>('POST', '/api/admin/users', p),
  updateUser: (id: string, p: { name?: string; role?: string; disabled?: boolean }) =>
    request<UserInfo>('PATCH', `/api/admin/users/${encodeURIComponent(id)}`, p),
  sendLink: (id: string) => request<void>('POST', `/api/admin/users/${encodeURIComponent(id)}/link`),

  welcomeCheck: (token: string) => request<PasswordLink>('POST', '/api/welcome/check', { token }),
  welcomeFinish: (token: string, password: string) =>
    request<User>('POST', '/api/welcome/finish', { token, password }),

  createUpload: (parentId: string, name: string, size: number) =>
    request<Upload>('POST', '/api/uploads', { parentId, name, size }),
  uploadStatus: (id: string) => request<Upload>('GET', `/api/uploads/${encodeURIComponent(id)}`),
  completeUpload: (id: string) => request<Node>('POST', `/api/uploads/${encodeURIComponent(id)}/complete`),
  cancelUpload: (id: string) => request<void>('DELETE', `/api/uploads/${encodeURIComponent(id)}`),
}

export function contentUrl(id: string, download = false): string {
  return `/api/nodes/${encodeURIComponent(id)}/content${download ? '?download=1' : ''}`
}
