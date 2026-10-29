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

// Drive is where an item lives. Kind 'share' is an item someone shared with
// the user: its name and rootId are the shared item's.
export interface Drive {
  id: string
  kind: 'personal' | 'shared' | 'share'
  name: string
  rootId: string
  role: 'viewer' | 'editor' | 'manager'
  sharedBy?: string
  // The user may share items here with people and by link.
  canShare?: boolean
}

export type DriveRole = Drive['role']

// Member is someone in a shared drive.
export interface Member {
  userId: string
  name: string
  email: string
  role: DriveRole
  disabled: boolean
}

// Person is an account that can be added to a shared drive.
export interface Person {
  id: string
  name: string
  email: string
}

// AdminDrive is a shared drive in the admin's list: no file names.
export interface AdminDrive {
  id: string
  name: string
  size: number
  joinByDefault: boolean
  members: Member[]
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
  // Shared with people or by link; only told to those who can share.
  shared?: boolean
  // A folder's first items, for its thumbnail.
  preview?: Mini[]
}

export interface Mini {
  id: string
  kind: 'folder' | 'file'
  type: FileType
  name: string
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

export type ShareAccess = 'view' | 'edit'

// Share is a person an item is shared with. From is set when the share is
// on a folder above the item.
export interface Share {
  userId: string
  name: string
  email: string
  access: ShareAccess
  disabled?: boolean
  from?: Crumb
}

export type LinkExpiry = 'never' | '7d' | '1d'

export interface Link {
  id: string
  token: string
  expiry: LinkExpiry
  expiresAt?: string
  expired?: boolean
  password: boolean
  createdAt: string
}

// Sharing is what the share dialog shows for an item.
export interface Sharing {
  drive: Drive
  members: number
  // Drive editors and managers: they have access already.
  editors: string[]
  people: Share[]
  inherited: Share[]
  link: Link | null
}

export interface SharedItem extends Located {
  sharedBy: string
  sharedAt: string
  access: ShareAccess
}

// PublicNode is an item as a link visitor sees it.
export interface PublicNode {
  id: string
  kind: 'folder' | 'file'
  type: FileType
  name: string
  size: number
  mime?: string
  modifiedAt: string
  childCount?: number
  preview?: Mini[]
}

export interface LinkPage {
  node: PublicNode
  // From the linked item down to the node's parent.
  path: Crumb[]
  children?: PublicNode[]
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

export interface BackupStatus {
  enabled: boolean
  time?: string
  retentionDays?: number
  running: boolean
  nextRun?: string
  lastRun?: string
  lastSuccess?: string
  lastError?: string
  durationSec?: number
  copied: number
  removed: number
  snapshots: number
  objects: number
  bytes: number
}

// DeviceToken is an upload key for the iPhone Shortcut.
export interface DeviceToken {
  id: string
  name: string
  createdAt: string
  lastUsedAt?: string
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
  recoverCheck: (token: string) => request<void>('POST', '/api/recover/check', { token }),
  recoverBegin: (p: { token: string; email: string; password: string }) => request<void>('POST', '/api/recover/begin', p),
  recoverFinish: (token: string, code: string) => request<User>('POST', '/api/recover/finish', { token, code }),

  drives: () => request<Drive[]>('GET', '/api/drives'),
  createDrive: (name: string) => request<Drive>('POST', '/api/drives', { name }),
  renameDrive: (id: string, name: string) => request<Drive>('PATCH', `/api/drives/${encodeURIComponent(id)}`, { name }),
  deleteDrive: (id: string) => request<void>('DELETE', `/api/drives/${encodeURIComponent(id)}`),
  members: (id: string) => request<Member[]>('GET', `/api/drives/${encodeURIComponent(id)}/members`),
  setMember: (id: string, userId: string, role: DriveRole) =>
    request<void>('PUT', `/api/drives/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}`, { role }),
  removeMember: (id: string, userId: string) =>
    request<void>('DELETE', `/api/drives/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}`),
  people: () => request<Person[]>('GET', '/api/people'),
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
  deleteForever: (id: string) => request<void>('DELETE', `/api/nodes/${encodeURIComponent(id)}?permanent=true`),
  emptyTrash: () => request<{ deleted: number }>('DELETE', '/api/trash'),
  starred: () => request<Located[]>('GET', '/api/starred'),
  recent: () => request<Located[]>('GET', '/api/recent'),
  search: (q: string) => request<Located[]>('GET', `/api/search?q=${encodeURIComponent(q)}`),

  sharedWithMe: () => request<SharedItem[]>('GET', '/api/shared'),
  sharing: (id: string) => request<Sharing>('GET', `/api/nodes/${encodeURIComponent(id)}/shares`),
  setShare: (id: string, userId: string, access: ShareAccess) =>
    request<void>('PUT', `/api/nodes/${encodeURIComponent(id)}/shares/${encodeURIComponent(userId)}`, { access }),
  removeShare: (id: string, userId: string) =>
    request<void>('DELETE', `/api/nodes/${encodeURIComponent(id)}/shares/${encodeURIComponent(userId)}`),
  // An empty password removes it; leaving it out keeps it.
  setLink: (id: string, p: { expiry?: LinkExpiry; password?: string }) =>
    request<Link>('PUT', `/api/nodes/${encodeURIComponent(id)}/link`, p),
  removeLink: (id: string) => request<void>('DELETE', `/api/nodes/${encodeURIComponent(id)}/link`),

  linkPage: (token: string, id?: string) =>
    request<LinkPage>('GET', `/api/s/${encodeURIComponent(token)}${id ? `/nodes/${encodeURIComponent(id)}` : ''}`),
  unlockLink: (token: string, password: string) =>
    request<void>('POST', `/api/s/${encodeURIComponent(token)}/unlock`, { password }),

  reauth: (password: string) => request<void>('POST', '/api/auth/reauth', { password }),
  updateMe: (p: { name?: string; locale?: string }) => request<User>('PATCH', '/api/me', p),
  changePassword: (current: string, password: string) =>
    request<void>('POST', '/api/me/password', { current, password }),
  sessions: () => request<SessionInfo[]>('GET', '/api/me/sessions'),
  revokeSession: (id: string) => request<void>('DELETE', `/api/me/sessions/${encodeURIComponent(id)}`),
  deviceTokens: () => request<DeviceToken[]>('GET', '/api/me/devices'),
  createDeviceToken: (name: string) => request<{ device: DeviceToken; token: string }>('POST', '/api/me/devices', { name }),
  revokeDeviceToken: (id: string) => request<void>('DELETE', `/api/me/devices/${encodeURIComponent(id)}`),

  users: () => request<UserInfo[]>('GET', '/api/admin/users'),
  inviteUser: (p: { email: string; name: string; role: string; locale: string; driveIds?: string[] }) =>
    request<{ user: UserInfo; error?: string }>('POST', '/api/admin/users', p),
  updateUser: (id: string, p: { name?: string; role?: string; disabled?: boolean }) =>
    request<UserInfo>('PATCH', `/api/admin/users/${encodeURIComponent(id)}`, p),
  sendLink: (id: string) => request<void>('POST', `/api/admin/users/${encodeURIComponent(id)}/link`),
  adminDrives: () => request<AdminDrive[]>('GET', '/api/admin/drives'),
  setJoinByDefault: (id: string, joinByDefault: boolean) =>
    request<void>('PATCH', `/api/admin/drives/${encodeURIComponent(id)}`, { joinByDefault }),
  adminSetMember: (id: string, userId: string, role: DriveRole) =>
    request<void>('PUT', `/api/admin/drives/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}`, { role }),
  adminRemoveMember: (id: string, userId: string) =>
    request<void>('DELETE', `/api/admin/drives/${encodeURIComponent(id)}/members/${encodeURIComponent(userId)}`),
  backupStatus: () => request<BackupStatus>('GET', '/api/admin/backup'),
  backupNow: () => request<void>('POST', '/api/admin/backup'),

  welcomeCheck: (token: string) => request<PasswordLink>('POST', '/api/welcome/check', { token }),
  welcomeFinish: (token: string, password: string) =>
    request<User>('POST', '/api/welcome/finish', { token, password }),

  createUpload: (parentId: string, name: string, size: number) =>
    request<Upload>('POST', '/api/uploads', { parentId, name, size }),
  uploadStatus: (id: string) => request<Upload>('GET', `/api/uploads/${encodeURIComponent(id)}`),
  completeUpload: (id: string) => request<Node>('POST', `/api/uploads/${encodeURIComponent(id)}/complete`),
  cancelUpload: (id: string) => request<void>('DELETE', `/api/uploads/${encodeURIComponent(id)}`),
}

// ContentUrl gives the address of a file's content (download: as an
// attachment). Link visitors use linkContent instead of the default.
export type ContentUrl = (id: string, download?: boolean) => string

export const contentUrl: ContentUrl = (id, download = false) =>
  `/api/nodes/${encodeURIComponent(id)}/content${download ? '?download=1' : ''}`

export function zipUrl(id: string): string {
  return `/api/nodes/${encodeURIComponent(id)}/zip`
}

export function linkContent(token: string): ContentUrl {
  const base = `/api/s/${encodeURIComponent(token)}/nodes/`
  return (id, download = false) => `${base}${encodeURIComponent(id)}/content${download ? '?download=1' : ''}`
}

export function linkZipUrl(token: string, id: string): string {
  return `/api/s/${encodeURIComponent(token)}/nodes/${encodeURIComponent(id)}/zip`
}

export function linkAddress(token: string): string {
  return `${location.origin}/s/${token}`
}
