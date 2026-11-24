# v1 security review

Status of the v1 release gate from [`security.md`](security.md#v1-release-gate-security-check). v1 is not released until every open item is done, or explicitly accepted below.

Last updated: 2026-11-18, on `main`.

## Source remediation status

| Item | Status | 2026-11-18 result |
|---|---|---|
| Password blocklist | Fixed | Password setup now combines the embedded blocklist, trivial-pattern checks and zxcvbn strength estimation. Weak passwords score `password_common`. |
| Go toolchain | Fixed | The toolchain and Docker build image are on Go 1.27.4 or newer. |
| Static analysis | Fixed | `staticcheck` and the triaged `gosec` findings now run in CI. The remaining gosec exclusions are documented above in the source review. |
| Container runtime | Fixed | The image runs as dedicated UID/GID 1000 with no shell or root runtime user. Plain Docker already drops all capabilities and disables privilege escalation. |
| npm dependencies | Fixed | Vite and plugin dependencies were updated; `npm audit --audit-level=high` is clean. |

The deployment image scan, live ZAP scan, real-device checks and real Home Assistant deployment review remain open.

## 1. Automated checks

| Check | Status | Notes |
|---|---|---|
| `govulncheck` | Runs in CI on every push | Not run locally in this round (the dev machine's sandbox didn't allow fetching the tool). Confirm a green CI run on the release commit. |
| `staticcheck` | Fixed | Pinned v0.8.2 runs in CI; 2026-11-18 rerun is clean. |
| `gosec` | Fixed | Pinned v2.30.0 runs in CI with documented exclusions; 2026-11-18 rerun has 0 untriaged findings. |
| `npm audit` | Done, 2026-10-28: 0 vulnerabilities | Includes `pdfjs-dist` 6.4.299. CI runs `npm audit --audit-level=high` on every push. |
| Image scan (OS packages + binary) | **Open** | Needs the release image from CI (for example Trivy or Grype on `ghcr.io/…:<version>`). |
| Fuzz tests | Done, 2026-10-28 | 30 s each, no failures: name validation (2.0 M runs), natural sort order (0.9 M), chunked upload offsets (56 k, against a real database and object store), export file names (0.8 M), config parsing (4.1 M). `Range` parsing is the standard library's `http.ServeContent` and is not fuzzed here. The seed inputs run with every `go test`. |
| OWASP ZAP baseline + API scan | **Open** | Needs a running instance reachable by ZAP. |

## 2. Manual review against OWASP ASVS 5.0 Level 2

A full walkthrough by a person is **open**. What the automated tests already pin down, per area:

**Authentication and sessions** (`server/internal/server/api_test.go`, `account_test.go`, `auth/auth_test.go`)
- Login with an unknown address and with a wrong password give identical responses.
- Email codes: 8 digits, bound to their challenge, 5 attempts, single use, 10 minutes; at most 5 codes per account per hour.
- Rate limits: 30 sign-in requests per 15 minutes per client address; 10 wrong passwords per hour per account. Device-token uploads only count wrong tokens.
- Session cookie `__Host-hd_session`, Secure, HttpOnly, SameSite=Lax, only the SHA-256 stored; sliding 30 days, absolute 90.
- Sessions end on password change (all others), password reset by link, admin recovery and account disable (all).
- Sensitive actions need the password within the last 15 minutes: user admin, permanent delete, emptying the trash, creating device keys.
- Admins can't disable or demote themselves; one active admin always remains.

**Access control**
- Two-user matrix over every file endpoint (`TestOtherUsersFiles`): reading, listing, downloading, renaming, moving (in and out), trashing, restoring, uploading and chunk writes into another user's drive all answer 404.
- Stars, search, Recent and the trash list only show drives the user can open (`TestStarsSearchRecent`, `TestDeleteForever`).
- Admin endpoints answer 403 for members (`TestInviteAndWelcome`).
- Device tokens: upload to the owner's Inbox only, never usable as a session, revocable (`TestDeviceTokenUpload`).

**Stored XSS through uploads** (`TestFileContentIsSandboxed`)
- File contents always carry `Content-Security-Policy: sandbox` and `nosniff`; HTML and SVG are only ever downloaded; text of any kind is shown as `text/plain`.
- Markdown isn't rendered in v1 (shown as plain text). The v2 editor must sanitize.
- File names are rendered as text by React, never as HTML.
- PDFs render with pdf.js from our own origin; the app's CSP has no `unsafe-eval`, and pdf.js scripting is not enabled.

**CSRF** (`TestJSONOnly`, `TestDeviceTokenUpload`)
- Go's `CrossOriginProtection` plus the Host check; JSON-only bodies on JSON routes; `application/octet-stream` on chunk uploads. A cross-site POST to `/api/inbox` with a valid token is refused.

**Resource abuse**
- Chunk size ≤ 32 MiB, a 50 GiB per-file limit, device uploads ≤ 95 MB, a 512 MB free-space reserve on the data disk, unfinished uploads expire after 24 h and are cleaned up hourly.
- Archives are never extracted. Thumbnails are not generated in v1.
- Server timeouts: header 10 s, read 1 min (15 min on upload routes), write 2 min (6 h on downloads).

**Headers** (`TestSecurityHeadersOnEveryResponse`): CSP, HSTS (on HTTPS), nosniff, frame denial, referrer and permissions policies on every response type.

## 3. Deployment review — open

Needs a real Home Assistant OS installation and the release image: add-on security rating, container runtime settings, secrets in options and environment, Cloudflare configuration as documented, backup file permissions, and a restore test with `homedrive export` from a real backup.

Logs are meant to contain no secrets, with two deliberate exceptions: the one-time setup and admin-recovery links (valid one hour, needed to get in), and, **only when SMTP isn't configured** (allowed for a `localhost` public URL only), the codes and links that would have been emailed.

## Findings and accepted risks

| # | Finding | Decision |
|---|---|---|
| F1 | The common-password list is short (about 130 hand-picked entries plus pattern checks for repeats, keyboard rows and the user's own name or address). | **Fixed 2026-11-18**: password checks now use zxcvbn strength estimation as well as the expanded blocklist and related-value checks. |
| F2 | Rate-limit counters live in memory and reset on restart. | Accepted for a household server; Cloudflare's rate limit on `/api/auth/` (documented) is the outer layer. |
| F3 | A database snapshot older than `backup_retention_days` may refer to files whose contents were already removed from the backup. | Accepted and documented; `homedrive export` lists such files instead of failing. |
| F4 | The service worker (app shell cache, Android share target) has not been checked in a real browser yet; the share flow was tested by simulating its IndexedDB handoff. | **Check before release** on an Android phone. |
| F5 | The iPhone Shortcut instructions were written from Apple's documented actions but not built on a real iPhone yet. | **Check before release.** |
