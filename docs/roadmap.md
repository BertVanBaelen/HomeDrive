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

- Shared drives: create, rename, members with viewer / editor / manager roles.
- Content thumbnails (images, video posters, PDF first page, documents, spreadsheets, folder 2×2).
- Markdown editor (WYSIWYG + source) and spreadsheet editor (`.xlsx`).
- Sharing with people and public view-only links.
- Storage page: usage by type, largest files, free-up-space actions, backup status.
- Descriptions on files and folders: plain text, edited in the info panel, found by search. Text shared together with a file fills it in.
- Saving links from the share sheet (Android share target and the iPhone Shortcut) as `.url` files, shown as link cards: thumbnail, title, site and author for YouTube and TikTok through their oEmbed endpoints, the shared title for other sites. Tapping a card opens the original app or site. Design in [`architecture.md`](architecture.md#descriptions-and-saved-links-v2).
- Log in with a Home Assistant username and password (add-on only, opt-in): a "Log in with Home Assistant" button under the email form opens its own login page, headed by the Home Assistant logo and the Homedrive logo joined by a chain link. The password is checked through the Supervisor, and the email code still follows. Design in [`architecture.md`](architecture.md#5-authentication).

## v3

- `homedrive restore <backup folder> [snapshot]`: rebuild a working installation from a backup, offline, after the data disk or the state dir is lost. It writes the database snapshot (newest by default) to the state dir and copies the referenced objects into an empty `data_path`. The instance ID stays the same, so the data folder and backup folder markers keep matching. It refuses a non-empty target and lists files whose contents are missing from the backup. Documented in `DOCS.md` together with moving the data folder to another drive (copy the folder with its `.homedrive` marker, change `data_path`), which already works without a restore.
- Email notifications through the SMTP server Homedrive already requires:
  - something is shared with you (a file, a folder or a link), with who shared it and a link that opens it;
  - you are added to a shared drive, or your role there changes;
  - security: a login from a new device, a new device key for the iPhone Shortcut, a changed password or email address;
  - admins: a failed backup, or the data disk running low on free space.

  Per user, each kind can be turned off in Account Settings (security emails cannot), and sharing emails can be bundled into one daily summary. Emails never contain file contents or previews, only names and links that require a login. Sent in the user's language.

## Later

- Word document preview.
- Passkeys and authenticator apps as additional second factors.
- Custom AppArmor profile for the add-on (after testing on a real installation).

## Not planned

- A "Shorts" folder that downloads videos from TikTok, YouTube or Instagram (considered 2026-10-29). Downloading needs a scraper such as yt-dlp: it breaks whenever the platforms change, Instagram needs a logged-in session, it goes against the platforms' terms, and the server would fetch addresses sent by users. Saved links (v2) cover the useful part.
