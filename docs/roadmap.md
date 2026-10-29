# Homedrive roadmap

## v1 — usable, polished and checked

Status 2026-10-28: everything below is built on `v1/vertical-slice` except what is marked open; the release gate is tracked in [`security-review-v1.md`](security-review-v1.md).

Foundation
- Project skeleton, config loading and validation, add-on + Docker packaging, CI.
- SQLite with migrations; storage guards and object store.

Accounts
- First-admin setup link, invitations, login with password + email code.
- Sessions and device list, admin user management (create, disable, reset password), admin recovery.
- Rate limiting, audit log.

Files
- Personal drive ("My Files") per user and one shared "Family" drive.
- Browse in grid and list, sort, breadcrumbs, back navigation.
- Folders, chunked resumable upload (drag and drop on desktop), download, rename, move.
- Trash with restore and empty, search, starred, recent.
- Viewers: images, video, audio, PDF (pdf.js), text and code.

Mobile
- Installable PWA, Android share target with the "Save to Homedrive" screen.
- iPhone: Apple Shortcut + device tokens.

Backup
- Nightly snapshot + object mirror with retention, status for admins, `homedrive export`.

Look and feel
- The full visual design from the mockup on desktop and mobile: design tokens, header, sidebar, bottom tab bar, FAB, bottom sheets, dialogs, toasts.
- Motion: items fade in with a small rise (calmer than the mockup's card flip, after feedback), viewer zoom, sheet slide, with `prefers-reduced-motion`. Folder open/close transitions are left for later.
- Accessibility checklist from the mockup README (dialogs trap focus and close on Esc, labelled icon buttons, `role=status` toasts; a full audit is open).
- English and Dutch.

Release gate
- The security check in [`security.md`](security.md#v1-release-gate-security-check) — **open**, see [`security-review-v1.md`](security-review-v1.md).

## v2

Status 2026-10-29: refined and in progress on `v2/development`, built in this order. The design is in [`architecture.md`](architecture.md); the release gate in [`security.md`](security.md#v2-release-gate-security-check).

1. **Shared drives** ([design](architecture.md#shared-drives-and-roles-v2)): anyone creates them and becomes manager; members are viewer, editor or manager. "Family" becomes a normal shared drive; the invite form picks the drives a new user joins. Admins manage every drive's members without seeing its files. A manager deletes a drive once it is empty. New navigation: a *Shared drives* group in the sidebar, *Shared with me*, and on phones the tabs Files, Shared, Recent, Starred, Trash.
2. **Sharing and public links** ([people](architecture.md#sharing-with-people-v2), [links](architecture.md#public-links-v2)): share with people as *Can view* or *Can edit* (only people who can edit an item may share it). One view-only public link per item with expiry Never / 7 days / 1 day and an optional password. Visitors browse shared folders and download single files or everything as a `.zip`. Signed-in users can download folders as `.zip` too.
3. **Descriptions and saved links** ([design](architecture.md#descriptions-and-saved-links-v2)): plain-text descriptions on files and folders, edited in the info panel and found by search; text shared together with a file fills it in. Links saved from the share sheet (Android share target and the iPhone Shortcut) become `.url` files shown as link cards, with thumbnail, title, site and author for YouTube and TikTok through their oEmbed endpoints. Tapping a card opens the original app or site.
4. **Thumbnails** ([design](architecture.md#thumbnails-and-previews-v2)): content previews for images, video posters with their length, audio cover art, PDF first pages, text and code snippets, spreadsheet tables, `.zip` contents lists, and folders as 2×2. iPhone HEIC photos open in the viewer through a converted preview. Made on the server with libvips, ffmpeg and poppler (the image grows by about 150 MB).
5. **Storage page** ([design](architecture.md#storage-page-v2)): your usage by type, your largest files, *Empty trash* and *Delete old versions*. Admins also see the data disk, the usage per person and per shared drive, and the backup status (the Backup page moves here).
6. **Home Assistant login** ([design](architecture.md#5-authentication), add-on only, opt-in): a "Log in with Home Assistant" button under the email form opens its own login page, headed by the Home Assistant logo and the Homedrive logo joined by a chain link. The password is checked through the Supervisor, and the email code still follows.
7. **Editors** ([design](architecture.md#editors-versions-and-edit-locks-v2)): Markdown (WYSIWYG + source), a plain text editor for `.txt` and code, and a spreadsheet grid for `.xlsx` (one sheet; arithmetic, `SUM`, `AVERAGE`, `MIN`, `MAX`, `COUNT`; anything more opens read-only). One person edits at a time: others see who is editing, and an overlapping save offers *Reload* or *Save as a copy*. Versions in the info panel, kept for `backup_retention_days`, can be opened and restored. Uploading a name that already exists asks *Replace* (a new version) or *Keep both*.

Not in v2: storage quotas, disk health and RAID status, public links that allow editing, editing `.xls`, `.ods` or `.docx`, playing saved videos inside Homedrive.

## v3

- `homedrive restore <backup folder> [snapshot]`: rebuild a working installation from a backup, offline, after the data disk or the state dir is lost. It writes the database snapshot (newest by default) to the state dir and copies the referenced objects into an empty `data_path`. The instance ID stays the same, so the data folder and backup folder markers keep matching. It refuses a non-empty target and lists files whose contents are missing from the backup. Documented in `DOCS.md` together with moving the data folder to another drive (copy the folder with its `.homedrive` marker, change `data_path`), which already works without a restore.
- **Nicer emails** ([design](architecture.md#email-and-push-notifications-v3), mockups in [`homedrive-mockup/emails.html`](../homedrive-mockup/emails.html)): every email becomes an HTML email in the mockups' style, with a plain-text part, in the user's language. The existing ones are redone: sign-in code (also for setup and recovery), invitation and the admin's password reset. A self-service *Forgot password* is not in v3 (see VX).
- **Notifications** by email through the SMTP server Homedrive already requires, and by push on phones and computers:
  - something is shared with you, with who shared it, an optional note from them, a thumbnail and a link that opens it;
  - you are added to a shared drive, or your role there changes;
  - security: a sign-in from a new device, a new device key for the iPhone Shortcut, a changed password or email address;
  - admins: a failed backup, or the data disk 85% or 95% full.

  Per user, each kind can be turned off for email and for push in Account Settings (security emails cannot). There is no daily summary: each email is sent when it happens. Push uses Web Push to the installed app; on iPhone only when Homedrive is on the home screen. Mockups without a design of their own are derived from the same parts.

Not in v3 (left out of the mockups): disk health (SMART) and RAID, the approximate location of a sign-in, a "weeks left" estimate, and clean-up suggestions the Storage page doesn't have.

## VX — version not decided

- Real-time co-editing: several people typing in the same document or spreadsheet at once, seeing each other's cursors. Needs a live connection (WebSockets) and merging of edits (for example CRDTs). Until then, v2's edit lock lets one person edit at a time.
- Approve link ([design](architecture.md#5-authentication)): the email with a sign-in code also gets an *Approve sign-in* button, for logins, first setup and admin recovery. It fills in the code for the page that is waiting, so the browser that typed the password continues by itself, also when the link is tapped on a phone. The code stays in the email as a fallback.
- *Forgot password?* on the login page. It can't simply email a reset link, because email is already the second factor; for example the request goes to an admin, who approves it before the user gets the reset email.

## Later

- Word document preview.
- Passkeys and authenticator apps as additional second factors.
- Custom AppArmor profile for the add-on (after testing on a real installation).

## Not planned

- A "Shorts" folder that downloads videos from TikTok, YouTube or Instagram (considered 2026-10-29). Downloading needs a scraper such as yt-dlp: it breaks whenever the platforms change, Instagram needs a logged-in session, it goes against the platforms' terms, and the server would fetch addresses sent by users. Saved links (v2) cover the useful part.
