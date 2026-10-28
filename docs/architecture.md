# Homedrive architecture

Status: agreed design, before implementation. Decisions here are binding until changed in this file.
The visual design and interaction spec live in [`homedrive-mockup/README.md`](../homedrive-mockup/README.md).

## 1. Goals and hard requirements

- Self-hosted "online drive" for a household's **documents** (small files, not a media server).
- Runs as a **Home Assistant OS add-on** and as a **standalone Docker container**, from the same image.
- **Configurable data path** (where files are stored) and an **optional backup path**.
- **Public on the internet** through a Cloudflare Tunnel. Security comes before features.
- **Installable PWA on phones** that can receive files from the share sheet. On desktop it is a normal website.
- **Generic**: nothing in code, config or docs refers to a specific domain or installation. Examples use `example.com`.

## 2. Deployment

```
Browser / PWA
   │ HTTPS
Cloudflare edge (TLS, WAF, rate limits)
   │ tunnel
cloudflared (HA add-on, or a container next to Homedrive)
   │ HTTP on the internal Docker network — never exposed on the LAN by default
Homedrive :8080 — one Go process: JSON API + static PWA + background jobs
   ├── state dir    SQLite database, thumbnail cache            (local disk, never a network share)
   ├── data_path    file contents, owned exclusively by Homedrive (local disk, USB disk or NAS share)
   └── backup_path  optional nightly backup
```

### One image, two run modes

| | Home Assistant add-on | Standalone Docker |
|---|---|---|
| Image | `ghcr.io/bertvanbaelen/homedrive:<version>` (amd64, aarch64) | same |
| Configuration | add-on options → `/data/options.json` | `HOMEDRIVE_*` environment variables |
| State dir | `/data` (add-on private, part of add-on backups) | `/config` volume |
| `data_path` | a folder under `/share` or `/media`: HA's own disk (default `/share/homedrive`), a USB disk (`/media/usb/homedrive`) or a NAS share added under *Settings → System → Storage* | `/data` volume: a local folder or a mounted network share |
| `backup_path` | under `/share` or `/media`, empty = off | `/backup` volume, empty = off |
| User | root inside the container, confined by Supervisor/AppArmor (HA-owned mounts are root-writable only) | non-root (`user: "1000:1000"` in the compose example), read-only root filesystem, all capabilities dropped |
| Network | no host port; the Cloudflared add-on connects to `http://<addon-hostname>:8080` | no published port when cloudflared runs in the same compose project |
| Sidebar (Ingress) | **no** — the PWA needs its own origin | n/a |

The add-on sets `HOMEDRIVE_STATE_DIR=/data` and `HOMEDRIVE_OPTIONS_FILE=/data/options.json` through `environment:` in its `config.yaml`. There is no auto-detection.

### Configuration

Every option has the same name in the add-on (`snake_case`) and as an environment variable (`HOMEDRIVE_` + upper case). Environment variables override the options file.

| Option | Default | Notes |
|---|---|---|
| `public_url` | — (required) | `https://drive.example.com`. Origin only, no path. Used for Host/Origin checks, cookies, links in emails. `http://` is only accepted for `localhost` during development. |
| `data_path` | `/data` (Docker), `/share/homedrive` (add-on) | Absolute. Must not overlap the state dir or `backup_path`. |
| `backup_path` | empty | Absolute, optional. |
| `backup_time` | `03:00` | Local time (`TZ`). |
| `backup_retention_days` | `30` | How long deleted and overwritten files stay in the backup. |
| `smtp_host`, `smtp_port`, `smtp_security` (`starttls`/`tls`), `smtp_username`, `smtp_password`, `smtp_from` | port `587`, `starttls` | Required: email is the second login factor. Plain-text SMTP is not supported. `HOMEDRIVE_SMTP_PASSWORD_FILE` reads the password from a Docker secret. |
| `trusted_proxies` | loopback + private ranges | Only requests from these addresses may set `CF-Connecting-IP` / `X-Forwarded-For`. |
| `log_level` | `info` | `debug`, `info`, `warn`, `error`. |
| `listen` (env only) | `:8080` | |
| `web_dir` (env only) | `/app/web` | Built frontend. |

## 3. Storage model

The data folder belongs to Homedrive alone: there are no imports and nothing else should write into it. It can be on the local disk, a USB disk or a NAS share; the code treats them the same. Because of that the database is the source of truth for the folder tree, and file contents are stored as immutable objects named by random IDs. File contents are never stored in SQLite; the database only holds names, the folder tree and other metadata. On disk:

```
<data_path>/
  .homedrive              marker: {"instance_id": "...", "format": 1}
  objects/3f/3f9a…c1      file contents, one object per file version
  uploads/<upload-id>     chunked uploads in progress
```

Why not a readable folder tree on disk:

- **Any file name works on any disk.** NAS shares (SMB) and USB disks formatted as exFAT or NTFS are case-insensitive and forbid characters (`: * ? " < > |`, trailing dots/spaces) that phones happily put in file names. With IDs on disk, the storage location never limits names.
- **No path traversal by construction.** User input never becomes a path. Object paths are built from validated IDs only, through Go's `os.Root`.
- **Rename and move are database updates**, not slow renames over SMB.
- **Versioning for free.** Saving a file writes a new object; the old one is kept until retention removes it. The backup uses the same property.
- **No file watching.** Nothing else writes into the folder, so there's nothing to watch or rescan (network mounts couldn't report changes anyway).

Restoring without the app stays possible: `homedrive export <backup> <target>` rebuilds a normal folder tree from a backup (snapshot DB + objects), offline.

### Safety guards at startup

- The state dir must be on local disk: SQLite over SMB/NFS corrupts under locking.
- First start: `data_path` must be empty or missing (its parent must exist), Homedrive writes the marker and records the instance ID in the database.
- Later starts: if the marker is missing or belongs to another instance, Homedrive **refuses to start**. This catches a USB disk or NAS share that isn't mounted, where `/media/usb/homedrive` would otherwise silently become an empty folder on the system disk.
- A non-empty folder without a marker is never adopted.
- A write + fsync + delete probe checks the share is writable.
- New objects are written to `uploads/`, fsynced, then renamed into `objects/` (same filesystem, atomic).

## 4. Data model (SQLite, WAL)

```
meta            (key, value)                      instance_id, schema info
users           (id, email UNIQUE, name, password_hash, role admin|member, locale, created_at, disabled_at)
sessions        (id_hash, user_id, created_at, last_seen_at, expires_at, recent_auth_at, user_agent, ip)
email_codes     (id, user_id, purpose, code_hash, expires_at, attempts, used_at)
device_tokens   (id, user_id, name, token_hash, scope, created_at, last_used_at, revoked_at)   iOS Shortcut uploads
drives          (id, kind personal|shared, name, owner_user_id NULL for shared, created_at)
drive_members   (drive_id, user_id, role viewer|editor|manager)
nodes           (id, drive_id, parent_id, kind folder|file, name, current_version_id, size, mime,
                 created_by, created_at, modified_at, trashed_at, trashed_by)
                 UNIQUE (drive_id, parent_id, lower(name)) WHERE trashed_at IS NULL
versions        (id, node_id, object_id, size, sha256, created_by, created_at, superseded_at)
stars           (user_id, node_id)
recents         (user_id, node_id, at)
people_shares   (node_id, user_id, access view|edit)                      v2
links           (id, node_id, token_hash, expires_at, password_hash, created_by, created_at, revoked_at)   v2, view-only
audit_log       (id, at, user_id, ip, event, detail)
```

- **Drives from day one.** Every user has a personal drive ("My Files"). v1 ships one shared drive, "Family", with every user as editor. v2 adds creating shared drives with members and roles.
- IDs are random (UUIDv7 for rows, 128-bit random for objects). Every API call that takes an ID checks access through the drive membership or share — never "the ID was valid".

## 5. Authentication

- **No self-registration.** The admin creates accounts; users receive an email invitation.
- **First admin:** on first start with no users, Homedrive logs a one-time setup link `https://…/setup#<token>` (valid 1 h, token in the URL fragment so it never reaches proxies or Cloudflare logs). The setup page creates the admin and verifies the email address with a code, which also proves SMTP works.
- **Login:** email + password, then a code sent by email (second factor). Codes are 8 digits, valid 10 minutes, single use, stored hashed, 5 attempts.
- **Passwords:** Argon2id (64 MiB, t=3, p=1), minimum 12 characters, a blocklist of common passwords, at most 2 hashes computed concurrently so login can't exhaust memory.
- **Sessions:** 256-bit random token in a `__Host-hd_session` cookie (Secure, HttpOnly, SameSite=Lax, Path=/), only its SHA-256 stored. Sliding 30-day expiry, absolute 90 days. Device list with revoke. Sensitive actions (password, email, user admin, permanent delete, device tokens) require the password again within 15 minutes.
- **Password reset:** by the admin only (email is already the second factor, so an email reset link would collapse login to one factor). Admin lockout: set the add-on option / env `admin_recovery=true` and restart to get a new one-time link in the log.
- **Rate limits:** per client IP and per account, progressive delays, plus a cap on codes sent per account per hour (no email bombing). Login responses don't reveal whether an account exists.
- **iPhone uploads:** an Apple Shortcut authenticates with a per-device token (scope: upload to own inbox only), created by the user after re-authentication and revocable in the device list.

## 6. HTTP surface

- `/api/…` JSON API (shape as sketched in the mockup README), `/s/<token>` share pages (v2), `/healthz`, static PWA.
- **Host check:** requests whose `Host` isn't the `public_url` host are rejected (stops DNS rebinding and direct LAN access with spoofed headers). `/healthz` is exempt.
- **CSRF:** SameSite cookies + `Origin` must equal `public_url` for every state-changing request + API only accepts `application/json` (or the upload content type on upload routes).
- **Uploads:** chunked and resumable, chunks ≤ 32 MiB because Cloudflare rejects request bodies over 100 MB on Free/Pro plans. Each chunk must start where the last one ended; the SHA-256 is computed on completion and stored with the version. Uploads that would leave less than 512 MB free on the data disk are refused.
- **Device uploads:** `POST /api/inbox?name=…` with `Authorization: Bearer hd_…` (a device token, scope `upload`) stores one file of at most 95 MB in the owner's *Inbox* folder. No cookie is involved, so the CSRF rules don't apply; cross-site browser requests are still refused by the Origin check. Only wrong tokens count against the client address's rate limit.
- **Downloads:** `Range` support; files are served `Content-Disposition: attachment` unless the type is on an inline allowlist (images except SVG, PDF, audio, video, plain text), always with `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`.
- **Timeouts:** header 10 s, idle 120 s, per-route read/write deadlines for uploads and downloads.

## 7. PWA

- Manifest + service worker, `display: standalone`, scope `/`.
- The service worker caches only the **app shell** (hashed static assets, index for offline start). It never caches `/api/*` or file contents, so a lost phone holds no documents.
- **Android share target:** the manifest's `share_target` POSTs to `/share-target`. The service worker intercepts it, stores the files in IndexedDB, and redirects to `/save`: pick a folder (last used preselected), rename, upload with progress. Files survive an expired session or a lost connection and are deleted from IndexedDB after upload. If the service worker isn't active, the server answers `/share-target` with a redirect to an explanation page.
- **iPhone:** iOS has no Web Share Target support, so the PWA can't be in the share sheet. Homedrive provides an Apple Shortcut ("Save to Homedrive") that uploads to the user's inbox with a device token.
- **Desktop:** normal website, drag-and-drop upload. The app never shows an install prompt on desktop (browsers may still offer install themselves).
- **HTTPS only:** secure cookies and service workers require it, so Homedrive is always used through its public URL, also at home.

## 8. Backup

- Nightly at `backup_time` when `backup_path` is set:
  1. Consistent database snapshot (`VACUUM INTO`) → `backup_path/db/homedrive-<timestamp>.db`, keep 14 + monthly.
  2. Copy new objects to `backup_path/objects/` (objects are immutable, so this is incremental by nature).
  3. Objects no longer referenced by any version stay until `backup_retention_days` have passed.
- `backup_path` carries the same instance marker as `data_path`; a mismatch disables backups with a visible error instead of mixing installations.
- Status (last run, duration, size, errors) is shown to admins and logged.
- Home Assistant's own backups contain only the add-on state (`/data`, database + settings; the thumbnail cache is excluded). File contents are covered by Homedrive's backup. The add-on uses `backup: cold` so HA never copies a database mid-write.

## 9. Code layout

```
server/                Go module (API, storage, jobs)
  cmd/homedrive/       entry point: serve (default), healthcheck, export (later)
  internal/config/     options file + environment, validation
  internal/server/     HTTP server, middleware, routes
  internal/db/         SQLite open + embedded migrations
  internal/storage/    data path guards, object store
web/                   React + TypeScript + Vite PWA
homedrive/             Home Assistant add-on (config.yaml, docs, translations, icons)
repository.yaml        Home Assistant add-on repository descriptor
Dockerfile             one multi-arch image for both run modes
docker-compose.example.yml
docs/                  architecture, security, roadmap
```

Stack: Go (standard library HTTP, `modernc.org/sqlite` — pure Go, so cross-compiling needs no C toolchain), React + TypeScript + Vite, self-hosted Inter font, Heroicons. Later: TipTap (Markdown), SheetJS official build + a formula engine, pdf.js, libvips/ffmpeg/poppler as sandboxed subprocesses for thumbnails.

UI languages: English and Dutch, chosen from the browser language, switchable per user.
