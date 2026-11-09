# v2 security review

Status of the v2 release gate from [`security.md`](security.md#v2-release-gate-security-check). It covers everything v2 added (shared drives, share links, saved links, thumbnails and previews, zip downloads, Home Assistant sign-in, and the editors, versions and locks of 2.7.0). The areas that did not change since v1 are in [`security-review-v1.md`](security-review-v1.md).

Last updated: 2026-11-06, on branch `v2/development` (release 2.7.0).

**Read this first.** The checks below were run by the developers and by AI coding agents, not by an independent party. Everything that needs a running instance, a real Home Assistant, or the release image is marked **Open**. The report says what is pinned down by tests and what is only reasoned about.

## 0. How security is managed

| Layer | How it works | Where it is enforced or checked |
|---|---|---|
| Identity | Email + password (hashed) or a Home Assistant login. Sessions are random, only the SHA-256 is stored, in a `__Host-` cookie (Secure, HttpOnly, SameSite=Lax). Sensitive actions need the password again within 15 minutes. Device tokens can only upload to the owner's Inbox. | `auth`, `accounts`, `server/api_test.go`, `account_test.go` ([v1 review](security-review-v1.md)) |
| Access control | One server-side rule for every request: the strongest of the user's drive role and every share on the item or a folder above it. IDs outside that answer `404`, never `403`, so existence doesn't leak. Paths for recipients start at the shared item. | `files` (tree and access), `TestSharingAccess`, `TestSharedDriveRoles`, `TestAdminManagesDrives`, `TestOtherUsersFiles`, `TestEditLocks` |
| Public links | A 128-bit random token, optional password (grant cookie bound to link and password), expiry, per-address limits, same answer for every token that isn't a live link. Visitors can't see versions, lock holders or editor marks, and never make the server contact another host. | `TestPublicLinks`, `TestLinkLimits`, `FuzzValidLinkToken` |
| Content isolation | Every file is served with `Content-Security-Policy: sandbox`, `nosniff`; only an allowlist is inline, the rest is an attachment (HTML and SVG never inline). The app's own CSP has no `unsafe-inline`/`unsafe-eval`; names and contents are rendered as text. | `TestFileContentIsSandboxed`, `TestSecurityHeadersOnEveryResponse` |
| Untrusted parsers | libvips, ffmpeg and poppler run as sandboxed subprocesses (`setrlimit`, `no_new_privs`, absolute tool paths, no shell, protocol and demuxer allowlists, 20 s). Parsers inside the server are pure Go with hard caps; the `.xlsx` reader and formula engine are fuzzed. | `thumbs` tests (real tools in CI), `sheet` tests and fuzzers |
| Editing integrity | An edit lock (128-bit secret, stored as a hash, header only) plus a version check on every save (`If-Match`). Lock changes run in one write transaction, so two editors can't both win. A save never overwrites a change it didn't see. | `TestEditLocks`, `TestEditSaving`, `files/TestEditing` |
| Data safety | Every save is a new version under a random object ID; objects are never shared between versions; old versions are deleted after `backup_retention_days`; version history is for editors only; restore is audited. | `TestVersionsAndRestore`, `files/TestVersionCleanup` |
| Abuse limits | Rate limits (sign-in, codes, link guesses, passwords, lookups, editing 300/min per user), upload and file size limits, a free-space reserve, at most 2 zips per user, 2 media jobs and 2 spreadsheet jobs at a time, timeouts on every server and parser step. | `TestLinkLimits`, `TestCodeAttemptsAreLimited`, `sheet/TestSemaphore` |
| Outbound requests | Only the fixed oEmbed endpoints of YouTube and TikTok, with strict image hosts, size, redirect and time limits; nothing for link visitors. | `savedlink` tests, `TestSavedLinks` |
| Audit and logs | Sensitive actions (membership, links, restore, replace, lock takeover, permanent delete, sign-ins) go to the audit log. Tokens, passwords, lock IDs and saved-link addresses stay out of logs. | `TestPublicLinks`, `TestSavedLinks`, `TestEditLocks` |
| Supply chain | Few dependencies (no npm `xlsx`; `excelize` only builds blank workbooks), actions pinned by hash, Dependabot, `govulncheck` and `npm audit` in CI, minimal Alpine image, sandboxed tools updated with the image. | `.github/workflows/ci.yml`, `.github/dependabot.yml` |
| Process | This gate: the checks in `security.md`, then findings fixed or accepted below. | this file |

The design rules behind this: the server never trusts the browser (formulas, sizes, names and roles are all checked again); every new endpoint is added to the access matrix; secrets travel in headers, not URLs; and a feature that would need a general-purpose parser on user files gets a small capped one instead.

## 1. Automated checks

| Check | Status | Notes |
|---|---|---|
| `gofmt`, `go vet ./...` | Done, 2026-11-06: clean | CI also runs them. |
| `go test ./...` | Done, 2026-11-06: all packages pass | CI runs it with `-race` and the real thumbnail tools. `-race` needs cgo, which the developer machine doesn't have, so the race detector was **not** run locally for the new editing code (its locking relies on the database's write-exclusive transactions). Confirm a green CI run on the release commit. |
| `govulncheck` | Done, 2026-11-06: no vulnerability in code that is called | One finding in a required module that is not called: GO-2026-5932 in `golang.org/x/crypto` v0.58.0. The fix exists only as an unreleased pseudo-version, so it was not pinned; Dependabot will propose the release. Accepted (F1). |
| `staticcheck` | Done, 2026-11-06: 3 findings, none in v2 code paths of concern | `ST1005` twice (an error string that starts with the proper noun "Home Assistant") and `SA4000` in `TestGate` (the same call repeated on purpose). Not added to CI yet (**open**, low value for the effort). |
| `gosec` | Done, 2026-11-06: 87 findings, triaged below, one fixed | 59 × G104 (unchecked `Close`/`Remove` on clean-up paths); 9 × G304 (files opened by variable: paths are built from validated IDs or from the operator's configuration); 8 × G306/G302 (cache and backup files are 0640 on purpose: owner and group, never other users); 2 × G202 (SQL built from constant fragments, values are bound); 1 × G204 (the sandbox starts fixed tools by absolute path, no shell); G703/G704/G705 (a path built from a validated version ID, the container health check to localhost, cached WebP bytes written with a fixed type); 4 × G115 (integer conversions): sizes of uploads are validated first, and the zip64 directory size in the archive snippet could wrap to a negative number and pass the limit; **fixed** (`size < 0` is refused). |
| `npm audit` | Done, 2026-11-06: 0 vulnerabilities | CI runs `--audit-level=high`. TipTap and its Markdown extension were added in 2.7.0. |
| Web tests, typecheck, build | Done, 2026-11-06: 126 tests pass, typecheck and build clean | CI now runs `npm test` too. The build has no inline `<style>`, `style=` or inline scripts in the HTML, and the CSS has no `@import` or external URLs; the editor and grid code has no `style=`, `innerHTML` or `eval` (checked by search). Typing in the real editor was tried in a headless browser against a fake server by the agent that wrote it; no real-browser run against the real server yet (**open**, with F11). |
| Fuzz tests | Done, 2026-11-06, no failures | Formula validator (1.4 M runs, then 1.35 M again today), `.xlsx` reader (2.6 M, then 1.5 M today), sheet XML writer (225 k); link tokens (8.6 M, 15 s), zip entry names (0.8 M), `.url` reader (0.6 M) and file names (0.6 M). One real crash was found in the `.xlsx` reader while it was written and fixed; its input is kept in `sheet/testdata/fuzz/FuzzRead` and runs with every `go test`. 60,000 random sheets gave the same answers in the Go and TypeScript formula engines. |
| Hostile `.xlsx` files | Done in tests | `sheet/TestZipBombs` (entry count, part size and unzipped size caps), `TestUnsupported` (merged cells, unknown formulas, too many sheets, too big a grid), `TestStylesSurvive`, `TestEditInvalid` (external references, `INDIRECT`, `WEBSERVICE`, unbalanced input), and `thumbs/TestSheetSnippets` for the preview reader. Real-world workbooks from other tools are **not** covered (see F4). |
| Image scan (OS packages + binary) | **Open** | Needs the release image from CI (for example Trivy or Grype on `ghcr.io/…:<version>`). |
| OWASP ZAP baseline + API scan | **Open** | Needs a running instance reachable by ZAP. |

## 2. Manual review per area

A full walk-through of OWASP ASVS 5.0 Level 2 by a person is still **open**. What is pinned down by tests, per area:

**Shared drives and sharing** (`TestSharingAccess`, `TestSharingFromMyFiles`, `TestSharedDriveRoles`, `TestAdminManagesDrives`, `TestDeleteSharedDrive`)
- The access matrix covers drive viewer, editor and manager, *Can view* and *Can edit* recipients, an admin who isn't a member, and a stranger, over every new endpoint.
- Outsiders see nothing of a shared drive (404); the last manager can't be removed; names follow the rules.

**Public links** (`TestPublicLinks`, `TestLinkLimits`, `FuzzValidLinkToken`)
- A visitor sees the linked item only; IDs outside it, or in the trash, don't exist; visitors change nothing.
- Tokens that aren't a live link all look the same. Wrong passwords and bad tokens are limited per address. A new password ends the grants given before.
- Tokens and passwords stay out of logs and the audit log. The page itself is never cached.

**Zip downloads** (`TestFolderZip`, `FuzzZipSegment`): names come from the tree and can't contain separators, `.` or `..`, so entries can't leave the folder they are extracted to.

**Saved links** (`TestSavedLinks`, `savedlink` tests): addresses are `http(s)` only, without user info; lookups only go to the fixed providers with strict limits; other sites get the host only; link visitors never cause a lookup; the address isn't logged.

**Thumbnails and previews** (`thumbs` tests): the sandbox refuses other programs (`TestSandboxRefusesOtherPrograms`), a playlist that points at local files is refused (`TestRealPlaylistRefused`), and bytes that aren't a recognised image never reach libvips, nor do images over 100 megapixels (`TestPhotoRefused`). The tests that need the real tools are skipped off Linux, so they only run in CI.

**Edit locks and versions** (`TestEditLocks`, `TestEditSaving`, `TestVersionsAndRestore`, `TestReplaceOnUpload`, `TestEditableMarks`, `files/TestEditing`, `TestVersionCleanup`, `TestReplaceUpload`)
- Only editors see who is editing and only editors can lock; another person's lock can't be released and a taken-over lock can't be renewed; the release needs the lock ID in the `X-Lock-Id` header; a takeover moves the lock, kills the old one and is audited.
- Every save needs the lock (`409 lock_lost`) and the version it started from (`412 edit_conflict`, with who and when). Images and other types have no editor, so a save is refused whoever asks. Text must be UTF-8 without NUL and at most 1 MB.
- Whoever can't edit sees no history, not even that there is one (`canEdit`/`editor` are never sent to link visitors). Old content is a download, never rendered in the page.
- *Restore* waits until nobody is editing, keeps the history, shares no object with the current version and is audited. *Replace* on upload needs edit rights on the target (checked again at completion), waits for an editor, and falls back to a new file when the target was renamed or moved meanwhile.
- Versions older than the retention and locks a day after they expire are removed by the clean-up; a permanent delete cascades to its versions, and unreferenced objects are removed within the hour.

**Spreadsheets** (`sheet` tests, `TestSpreadsheets`, `web/src/sheet/engine.test.ts`, `web/test/sheetGrid.test.ts`)
- The browser sends cell text only. The server checks each formula against the grammar (numbers, cell references, `+ - * /`, brackets, `SUM`, `AVERAGE`, `MIN`, `MAX`, `COUNT`; at most 500 characters and 32 levels), calculates the results itself and writes text as inline strings. External references and every other function answer `400 invalid_sheet`. Text that starts with `=` but isn't allowed is never stored as a formula.
- Workbooks are read and written by `internal/sheet`, which streams the XML with hard caps and splices edits into the original, instead of loading user files into a general library. Preview snippets use the same reader (this replaced excelize on files of up to 20 MB, a pre-existing risk).
- Both engines pass the shared `formulas.json` cases; the browser engine has no `eval`, `Function` or regular expressions.
- At most two workbooks are handled at a time for the whole server, each with a 20 s limit (waiting included).

**Markdown and text editors** (`web/test/editorSession.test.ts`, code review)
- The editor is an allowlist schema (TipTap): raw HTML stays visible as text; images, relative and unsafe links make a file open as text instead of being rewritten; typed, pasted or loaded links are limited to `http`, `https` and `mailto` and open with `rel="noopener noreferrer"`; pasted HTML goes through the schema.
- No `innerHTML`, `dangerouslySetInnerHTML`, `eval`, or injected styles (`injectCSS: false`); the editor and grid code has no `style` attributes, and the build has no inline styles or scripts and no external CSS.

**CSRF** (`TestCrossOriginWritesRejected`, `TestJSONOnly`): the new save and lock routes need custom headers (`X-Lock-Id`, `If-Match`), which a cross-site page can't send without a preflight; the server never answers a preflight, and Go's cross-origin protection is on as well.

**Headers** (`TestSecurityHeadersOnEveryResponse`): unchanged from v1, also for the new endpoints.

## 3. Deployment review — open

Needs a real Home Assistant OS installation and the release image: add-on security rating with `auth_api`, container runtime settings, the sandbox on the real kernel (`setrlimit`, `no_new_privs`), secrets in options and environment, Cloudflare configuration as documented, backup file permissions and a restore test of a 2.7.0 database (new tables `edit_locks`, columns `versions.edit_lock` and `uploads.replace_node_id`; migration `0010_editing.sql`).

## Findings and accepted risks

| # | Finding | Decision |
|---|---|---|
| F1 | `golang.org/x/crypto` v0.58.0 has GO-2026-5932; the code that is affected isn't called, and the fix is only an unreleased pseudo-version. | Accepted; take the release when Dependabot offers it. |
| F2 | The spreadsheet limit of two at a time is for the whole server, so one user could hold both slots for up to 20 s and make others wait. Rate limits and file sizes keep this short. | Accepted for a household server. |
| F3 | Rate-limit counters live in memory and reset on restart (also the editing limit). | Accepted, as in v1 (F2 there). |
| F4 | Formulas inside defined names, conditional formatting or data validation are not checked, and hyperlinks, OLE and ActiveX parts are kept as they are when a workbook is saved. The editor never adds any of them, and a workbook with formulas this app can't evaluate opens read-only. A file that someone uploaded is as risky to open in Excel as it was before; Excel's own Protected View applies. The engine has only been tried against files the tests build and the files of a few real tools. | Accepted; try real workbooks from Excel, LibreOffice and Google Sheets before release (**check**). |
| F5 | If the grid is ever exported to CSV, cells starting with `=`, `+`, `-` or `@` must be escaped. The only export today is the spreadsheet's *Save as a copy*, a `.csv` of the shown values, which prefixes such cells with `'` (tested in `web/test/sheetGrid.test.ts`). | Accepted; keep this rule for any new export. |
| F6 | Version history keeps content that an editor removed on purpose for `backup_retention_days` (and in backups for as long as they last). It is visible to editors only. | Accepted and documented; *Delete old versions* in the storage page removes them earlier. |
| F7 | A browser that goes away mid-edit leaves a lock for up to 2 minutes. A lock that ran out still serves its holder until someone else takes the file. | Accepted by design (no data loss, no stuck files). |
| F8 | The editor keeps retrying on 429 and 507 with backoff; a sign-in that ends mid-edit loses unsaved text; on a phone the final save may not finish when the page is closed. | Accepted for v2; the editor warns before closing with unsaved changes. |
| F9 | The Markdown page editor collapses trailing newlines and turns a pasted leading `#` or `- ` into formatting. It can't change what is stored beyond that, and files that wouldn't survive it open as text. | Accepted. |
| F10 | The code was written with AI agents (the sheet engine, formula parser and editors) and reviewed by the developer and by tests, not by an independent person. | **Before a wide release:** an independent review of `internal/sheet` and `web/src/editor`. |
| F11 | The service worker, the iPhone Shortcut (v1 F4 and F5) and the real Home Assistant sign-in have not been checked on real devices. | **Check before release**, as in v1. |
