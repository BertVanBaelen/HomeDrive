# Homedrive roadmap

## v1 — usable, polished and checked

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
- The motion spec (card flip-in, folder open/close, viewer zoom, sheet slide), with `prefers-reduced-motion`.
- Accessibility checklist from the mockup README.
- English and Dutch.

Release gate
- The security check in [`security.md`](security.md#v1-release-gate-security-check).

## v2

- Shared drives: create, rename, members with viewer / editor / manager roles.
- Content thumbnails (images, video posters, PDF first page, documents, spreadsheets, folder 2×2).
- Markdown editor (WYSIWYG + source) and spreadsheet editor (`.xlsx`).
- Sharing with people and public view-only links.
- Storage page: usage by type, largest files, free-up-space actions, backup status.

## Later

- Word document preview.
- Passkeys and authenticator apps as additional second factors.
- Custom AppArmor profile for the add-on (after testing on a real installation).
