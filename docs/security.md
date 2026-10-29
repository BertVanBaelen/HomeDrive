# Homedrive security

Homedrive is reachable from the internet and holds private documents. This file is the threat model, the list of controls, and the plans for the security checks that gate the v1 and v2 releases.

## Threat model

| Attacker | Wants | Main defences |
|---|---|---|
| Internet scanner / bot | Any unauthenticated access, known CVEs, exposed admin pages | Host check, no default credentials, no setup wizard open to the world, minimal unauthenticated surface, dependency scanning |
| Credential stuffer | Log in with leaked passwords | Second factor by email, rate limits per IP and account, common-password blocklist, no account enumeration |
| Logged-in user (other household member) | Read or change someone else's files | Per-request authorization through drive membership and shares (on the item or a folder above it), IDOR tests for every endpoint |
| Drive member or share recipient | More than their role or share allows: moving items out of a share, sharing further, deleting for good | Every action checked in the service layer against the role or share (v2), a test per role and action |
| Malicious file (uploaded by a user, or shared to them) | Script execution in the app's origin, parser exploits | Sandboxed, non-inline serving; Markdown rendered through a fixed schema without raw HTML; parsers in limited subprocesses |
| Someone with a share link | More than the shared item | Link scope = one item and what is in it, view-only, expiry, optional password, revocation, rate limits |
| Someone with a copy of the database (for example a Home Assistant backup) | Open the files behind live public links | Accepted (2026-10-29) so *Copy link* always works: links can be turned off or given an expiry; keep backups as private as the files |
| Stolen / lost phone | Documents | No offline document cache, session revoke from the device list |
| Network position between Cloudflare and Homedrive | Spoof client IPs, bypass rate limits | `trusted_proxies`, no LAN port by default |

Out of scope: a compromised Home Assistant host or NAS, a compromised Cloudflare account, a compromised email account of a user (it is their second factor).

## Controls

### Authentication and sessions
- No self-registration; first admin through a one-time setup link in the log (URL fragment, 1 h validity).
- Argon2id password hashing with a concurrency cap; min 12 characters; blocklist.
- Email code second factor: 8 digits, 10 min, single use, hashed, 5 attempts, send-rate limit.
- `__Host-` session cookie, Secure, HttpOnly, SameSite=Lax; token hashed at rest; sliding 30 d / absolute 90 d; revocable device list; re-authentication for sensitive actions.
- Session ID rotated on login and privilege change; all sessions revoked on password change.
- Audit log: logins, failures, code sends, admin actions, device token use; from v2 also shared drive and membership changes, shares and public links.

### Requests
- Host header must match `public_url`. Origin must match on state-changing requests. JSON-only API bodies.
- Client IP from `CF-Connecting-IP` / `X-Forwarded-For` only when the peer is in `trusted_proxies`.
- Body size limits per route, header size limit, timeouts against slow clients.
- Response headers on every response:
  - `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; font-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`
  - `Strict-Transport-Security: max-age=31536000` (HTTPS only), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`, `Permissions-Policy` denying camera, microphone, geolocation, payment, USB and others.
  - No inline scripts or styles in the built frontend; no third-party origins at runtime (fonts are self-hosted).

### Files
- Paths on disk are built from validated random IDs only, inside an `os.Root`. Display names are data, never paths.
- File contents are served with `nosniff` and `Content-Security-Policy: sandbox`; anything outside the inline allowlist is `attachment`. SVG and HTML are never inline.
- Markdown is rendered through TipTap's schema (v2), which is an allowlist of nodes and marks: raw HTML stays visible as text, links are limited to `http`, `https` and `mailto` and open with `rel="noopener noreferrer"`. The app never sets HTML from file contents (`innerHTML`, `dangerouslySetInnerHTML`).
- Parsers (thumbnails, previews) run as subprocesses with timeouts, memory and pixel limits; archives are listed, never extracted. The npm `xlsx` package is not used (outdated, known CVEs). From v2:
  - libvips, ffmpeg and poppler are started through the `homedrive` binary itself, which sets memory, CPU-time and file-size limits (`setrlimit`) before `exec`. After 20 s the whole process group is killed. There is no shell; the arguments are fixed flags and the object's path, never a name chosen by a user.
  - libvips runs with `VIPS_BLOCK_UNTRUSTED=1`, with the loader picked by Homedrive from the file's first bytes (JPEG, PNG, WebP, GIF, HEIF/AVIF, TIFF, BMP). Images over 100 megapixels, read from the header, get no thumbnail.
  - ffmpeg gets a demuxer allowlist (MP4/MOV, Matroska/WebM, AVI, MPEG, MP3, AAC, FLAC, Ogg, WAV) and may only open its input, so playlists such as HLS or concat inside an upload can't read other files or reach the network.
  - `pdftoppm` renders only the first page at a fixed size.
  - Parsers inside the server are pure Go: excelize for `.xlsx` (files up to 20 MB, with its unzip size limits against zip bombs), `archive/zip` for listing (the central directory only, up to 4 MB of it) and `encoding/csv` for snippets (the first 2 KB).
- Zip downloads (v2) take names from the tree. Names never contain `/`, `\` or control characters and are never `.` or `..`, so entries can't escape the folder they are extracted to. No compression, at most 2 zips per user at a time.
- Disk-full guard and per-upload size limits; uploads verified by SHA-256.

### Shared drives and sharing (v2)
- A user's access to an item is the strongest of their drive role and every share on the item or a folder above it, worked out on the server for every request. IDs outside that answer 404.
- Paths for share recipients start at the shared item, so names of folders above it never reach them (also not through search, Recent, Starred, Trash or zips).
- *Can edit* shares can't move items out of the shared item, delete for good, or share. Only people who can edit an item on its drive may share it or make links.
- Admins manage the members of every shared drive, but read files only where they are a member themselves. Every membership change is audited.
- A drive always keeps a manager.
- Edit locks: a save needs the editor's lock and the version it started from (`If-Match`). A lock only blocks content changes.

### Share links (v2)
- 128-bit random tokens in the path `/s/<token>`. They are stored in full so *Copy link* keeps working (accepted, see the threat model) and looked up through their SHA-256.
- View and download only. The scope is one item and what is in it; every node ID in a link request is checked against it.
- Expiry; optional password (Argon2id, at least 8 characters, rate-limited). The password grant is a `__Host-` cookie, HttpOnly, with an HMAC over the link and its password hash, so changing the password ends it.
- Unknown, expired, switched-off and trashed links get the same answer.
- `X-Robots-Tag: noindex, nofollow`, `Cache-Control: no-store`. `Referrer-Policy: no-referrer` keeps tokens out of `Referer` headers, and request logs show them as `…`.
- Rate limits on unknown tokens, passwords and zips. The page shows no names of people. Creating, changing and removing links is audited.

### Container and supply chain
- Static Go binary on Alpine; no shell needed at runtime.
- Docker: non-root, read-only root filesystem, `cap_drop: ALL`, `no-new-privileges`.
- HA add-on: no host network, no privileged mode, no Docker/HA API access, Ingress off. Custom AppArmor profile once tested on a real installation.
- CI: `go vet`, tests, `govulncheck`, `npm audit`; GitHub Actions pinned to commit SHAs; Dependabot for Go, npm, Docker and Actions.
- Images built in GitHub Actions with provenance and SBOM.

### Cloudflare (operator configuration, documented in the add-on docs)
- WAF rate-limiting rule on `/api/auth/*`.
- Optional: country restriction, Bot Fight Mode.
- Optional: Cloudflare Access in front, with a bypass for the paths link pages need (`/s/*`, `/api/s/*` and the app's static files) so share links keep working.

## v1 release gate: security check

v1 is not released until all of this is done and findings are fixed or explicitly accepted in `docs/security-review-v1.md`.

1. **Automated**
   - `govulncheck`, `gosec`, `staticcheck`, `npm audit` clean.
   - Image scan of the release image (OS packages + Go binary).
   - Go fuzz tests for name validation, chunked upload offsets, `Range` parsing, config parsing.
   - OWASP ZAP baseline + API scan against a running instance.
2. **Manual, against OWASP ASVS 5.0 Level 2**
   - Authentication and session management (brute force, code reuse, code race conditions, fixation, logout, revoke).
   - Access control: a two-user test matrix over every endpoint (IDOR), shared drive roles, device token scope.
   - Stored XSS through uploads: HTML, SVG, PDF with JavaScript, polyglots, Markdown payloads, file names.
   - CSRF on every state-changing route; Host/Origin bypass attempts; CORS absence.
   - Resource abuse: disk fill, decompression bombs, slow clients, many small chunks, email bombing.
   - Header and cookie review on every response type (API, static, file content, errors).
3. **Deployment review**
   - HA add-on security rating, container runtime settings, secrets in options/env, logs free of secrets and codes.
   - Cloudflare configuration as documented.
   - Backup files: permissions, marker checks, restore test with `homedrive export`.

## v2 release gate: security check

v2 is not released until the v1 checks are repeated on the new surface and these are done, with findings fixed or accepted in `docs/security-review-v2.md`.

1. **Automated**
   - The v1 tools, plus an image scan with the new packages (libvips, ffmpeg and poppler often get CVEs: Alpine stays current through Dependabot).
   - Go fuzz tests for the spreadsheet formula grammar, link token parsing, zip entry names and the `.xlsx` ↔ grid conversion.
   - The thumbnail pipeline run against a set of broken and hostile images, videos, audio files and PDFs.
2. **Manual**
   - Access control matrix over every new endpoint for: viewer, editor, manager, *Can view* and *Can edit* recipients, an admin who isn't a member, and a stranger. Moving items out of shares, restoring from the trash, folder names leaking through paths, search, Recent, Starred, Trash or zips.
   - Public links: guessing and timing, password brute force, forged or reused grant cookies (also after a password change), node IDs outside the linked item, trashed items, expiry edges, very large zips, headers, logs.
   - Hostile files: ffmpeg playlists (HLS, concat) pointing at local files or URLs, image and PDF decompression bombs, odd HEIC files, `.xlsx` zip bombs, Markdown XSS (`javascript:` links, raw HTML, images).
   - Edit locks and versions: two editors racing, *Replace* during an edit, restore, deleting old versions.
   - Home Assistant login: rate limits through the Supervisor path, unlinked and disabled accounts, the add-on's security rating with `auth_api`.

## Reporting vulnerabilities

See [`SECURITY.md`](../SECURITY.md).
