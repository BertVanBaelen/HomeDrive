# v3 handoff — 2026-11-18

Where the v3 usability work stands, what was checked, and what is left. The plan is [`v3-ux-plan.md`](v3-ux-plan.md) (its status table is kept up to date); the design is [`architecture.md`](architecture.md).

## Branch

- Work is on `v3/development`. It starts from `main` plus `7dd8ca9` *Security fixes for 2.8.1*, which is **not on `origin/main` yet**. Merging `v3/development` brings 2.8.1 along; to release 2.8.1 on its own first, push or open a pull request for `main` before merging v3.
- The version stays 2.8.1 (`homedrive/config.yaml`). v3 gets its version and its `homedrive/CHANGELOG.md` entry when it is released.

## Status per work package

| WP | State | What's left |
|---|---|---|
| 1 Markdown preview and page editing | Done | The source-mode note doesn't say *which* construct blocks the page (tables, images, HTML …), only that it does. The round-trip test uses a fixed list of documents, not generated ones. |
| 2 History and the back button | Partly | Launch rewrite and Home guard for the installed app; the *Close Homedrive?* sheet; back layers for the phone info sheet, account popover, `Select` and drive menus; `web/test/history.test.ts` against a fake `History`. |
| 3 + in the tab bar, Trash in the account menu | Done | Why + is dimmed is a `title` tooltip only, which phones don't show. |
| 4 New folder in the picker, Home after saving | Done | Back from Home after saving doesn't ask to close (needs the WP2 guard). |
| 5 Select several | Partly | Long-press to select on touch; Ctrl/⌘- and Shift-click on the card or row itself (now only on the circle); *Select all* in the bar; selection as a back layer; *Undo* in the toasts; zip name rule (below). |
| 6 Right-click menu and shortcuts | Not started | All of it. A first try with Kokonut UI's marking menu was removed: its ring of options covered the page and blocked clicks. |
| 7 Zoom in the viewer | Done | The zoom bar doesn't hide by itself on phones; no page number in the PDF bar; text size isn't offered on phones (the bar has no room). |
| 8 Back animations | Not started | All of it; `motion.ts` still uses the 2-second window. |
| 9 Fill in and sign PDFs | Left out | Not part of this pass. |

## Built

**Server**

- `POST /api/nodes/batch` (`server/internal/server/batch_handlers.go`): move, star, trash, restore, delete forever for up to 500 IDs; each item goes through the same `files` call and access check as the single endpoint and answers `{done, skipped: [{id, code}]}`. Delete forever needs a recent password and is audited as `deleted_forever`, like a single delete; other batch actions aren't audited (the single ones aren't either).
- Zip of several: `POST /api/zips {ids}` → `{url}`; `GET /api/zips/{token}` streams the zip. The token is 128 random bits, kept in memory for 2 minutes, single use (also used up by a wrong user or session), tied to the user and to a SHA-256 of the session cookie, at most 5 pending per user (429). Access is checked again when the download starts, and the per-user zip gate applies. The token is masked in the request log. `files.ArchiveMany` drops duplicate IDs and names equal top-level names `name (2).ext`, case-insensitively.
- Tests: `TestBatchNodes` and `TestZipSelection` in `server/internal/server/batch_test.go`.

**Web**

- History (`web/src/router.ts`): `history.state.hd = {doc, i, layer, token}`, the trail of paths per window in `sessionStorage` (`hd.trail`), `navigate`, `goUp`, `goTop`, `useBackLayer(open, close, key)` and `closeBackLayer`. Used by `Modal`, the viewer and the editor; *← Back* uses `goUp`, saving uses `goTop`.
- Markdown: `components/MarkdownPreview.ts` (tokens → React elements, no HTML), `editor/markdownSchema.ts` (the page editor's extensions and `sameMeaning`), `editor/session.ts` `pageUnsafe` as an allowlist, the `tidy` / `blocked` modes and the *Switch* question in `editor/MarkdownEditor.tsx`.
- Tab bar with + and Trash in the account menu (`components/Shell.tsx`); toast actions (`components/Toast.tsx`).
- Folder picker *New Folder* (`components/Dialogs.tsx`); Home and *Open* toast after the last share is saved (`pages/Save.tsx`).
- Selection: `web/src/selection.ts` (state, Shift ranges, Ctrl/⌘+A and Esc that leave inputs and dialogs alone), circles in `components/Items.tsx`, `components/SelectionBar.tsx`, wired into the folder, Recent/Starred/search, Shared with me and Trash views in `pages/Browser.tsx`.
- Zoom: `web/src/zoom.ts` (pure math), `components/ZoomStage.tsx` (pinch, double-click/tap, Ctrl + wheel, `+ − 0`, drag, swipe at fit), `components/PdfView.tsx` (zoom bar, scrolling both ways, re-render 150 ms after zooming, 16 MP per canvas), text size for text and Markdown.

## Fixed in the review of the first implementation

The first pass of WPs 1–8 was reviewed and tested; these were wrong and are fixed:

1. Tailwind scanned all source files and turned words such as `table` and `grid` into global utility classes, which broke the list view. It now only scans `web/src/components/ui` (`styles/theme.css`).
2. The multi-zip was fetched into a `Blob` in the page (the whole zip in memory). Replaced by the single-use download address above.
3. Batch actions were audited under new `batch.*` events for every operation. Now only delete forever, with the existing event.
4. `useBackLayer` re-pushed an entry on every render and left entries behind when a layer closed from the UI. Rewritten.
5. Back closed the editor even while it asked about unsaved changes. The editor's layer is pushed again when it stays open.
6. The *tidy* switch could change what a file means (headings 4–6 became text, `\-` became a list, a list from 0 restarted at 1, task lists lost their checkboxes). Headings 1–6 are kept; anything that would change meaning stays in source (`sameMeaning`, task lists in `pageUnsafe`).
7. After *Switch*, a later change of mode skipped all checks. Fixed.
8. Ctrl+A in the search box or the editor selected all files; Esc in dialogs cleared the selection. The keys now only apply to the file list.
9. Esc in the picker's new-folder field closed the whole picker.
10. Ctrl + wheel zoomed the page too (React's wheel listener is passive), and pointer capture swallowed clicks on the zoom bar.
11. A zoomed photo could be dragged off so an empty band showed; it now always covers the stage where it is larger than it.
12. PDF zoom stretched pages (`max-width: 100%` with a fixed height) and the bar scrolled away.
13. The Markdown preview flattened nested lists and quotes and showed `&amp;` literally.
14. On phones the viewer hid *Page | .md* entirely.
15. The phone selection buttons lost their accessible names (`display: none` labels).
16. The last items stayed under the selection bar.
17. Toast plurals, and the selection bar missing in Shared with me.
18. The docs claimed WPs 1–8 were done.

## Differences from `architecture.md` still to settle

- **Zip name:** the design names the zip after the folder when all items share one (`Photos.zip`), else `<app name> <date>.zip`. It is always `Homedrive.zip` now (`server/internal/files/zip.go`).
- **History state:** the design has `{i, kind, from}` with `page`, `layer`, `root` and `guard`; the code has `{doc, i, layer, token}`. `root` and `guard` come with the WP2 launch work; update the design or the code then.
- **Empty lines (WP1):** the design describes a custom paragraph writer for `&nbsp;`. TipTap 3.33 already writes and reads empty paragraphs that way, so there is no custom code; `web/test/markdownPage.test.ts` covers it. A document of only one empty paragraph is still lost, and trailing newlines are trimmed (known).
- **marked version:** the app uses marked 18.2.1, `@tiptap/markdown` bundles 17.0.6, so two copies ship. The design wanted the same version.

## Decision needed: UI libraries

Kokonut UI and Untitled UI were asked for. Installed for them: `tailwindcss`, `@tailwindcss/vite`, `tailwind-merge`, `tailwindcss-animate`, `react-aria-components`, `tailwindcss-react-aria-components`, `motion`, `lucide-react`, `clsx`, `class-variance-authority`, `@untitledui/icons`, plus `web/components.json`, `web/src/styles/theme.css`, `web/src/lib/utils.ts`, `web/src/utils/*`, `web/src/components/ui/ParticleStatusButton.tsx` and the `@` alias in `vite.config.ts` / `tsconfig.app.json`. **None of the visible app uses them now.** Either use them for WP6 (an Untitled UI dropdown needs the Untitled theme tokens added) or remove them all, which shrinks the dependency list of a public app.

## Next steps, in order

1. Get 2.8.1 onto `main` (push or pull request).
2. Try what is built on real devices: Android Chrome installed and in a tab, iPhone Safari from the home screen and in a tab, desktop Chrome, Firefox and Safari (see the checklist below).
3. Decide on the UI libraries.
4. Finish WP2 (launch rewrite, Home guard, close sheet, remaining back layers, `history.test.ts`).
5. Finish WP5 (long-press, click modifiers, *Select all*, selection back layer, *Undo*, zip name).
6. WP6, then WP8.
7. The small items in the status table, then the v3 release check (security review as for v2, version, changelog).

## Running and checking

```powershell
cd web; npm ci; npm run typecheck; npm test; npm run build; npm audit
cd ..\server; go vet ./...; go test ./...
staticcheck ./...    # v0.8.2
gosec -exclude=G104,G202,G204,G302,G304,G306,G703,G704,G705 ./...    # v2.30.0
```

Local server, with sign-in codes in the log because SMTP isn't set:

```powershell
$env:HOMEDRIVE_PUBLIC_URL = 'http://localhost:8080'
$env:HOMEDRIVE_STATE_DIR  = '<folder>\config'
$env:HOMEDRIVE_DATA_PATH  = '<folder>\data'
$env:HOMEDRIVE_BACKUP_PATH = '<folder>\backup'
$env:HOMEDRIVE_WEB_DIR    = (Resolve-Path web\dist).Path
cd server; go run ./cmd/homedrive
```

A new state dir logs a setup link for the first account.

## Manual checklist for what is built

- List view and grid view on a computer and a phone; nothing runs off the side.
- Select with the circles, Shift for a range, Ctrl/⌘+A and Esc on the list; Ctrl+A in the search box selects text only.
- Download several: one `.zip`, saved straight to disk; the address works once.
- Move several, with *New Folder* in the picker (Esc only closes the name field); trash a selection where one item can't be changed: the toast says how many were skipped; restore from Trash.
- Photo: + / − / Fit, double-click, Ctrl + wheel and pinch zoom only the photo; drag when zoomed; swipe to the next photo at fit on a phone.
- PDF: zoom stays sharp at 300%, scrolls both ways, the bar stays put.
- Markdown: preview with nested lists, tables, task lists, `&amp;`; scripts and HTML show as text, images as text, `javascript:` links not linked; *Page | .md* remembered.
- Editor: a `*`-bullet file asks before *Page* and isn't saved until typing; `\- text`, tables and task lists keep *Page* disabled.
- Back: closes a dialog, then the editor, then the viewer, then goes up a folder; closing with ✕ leaves no extra Back step.
- Phone: + in the tab bar, dimmed where adding isn't possible; Trash in the account menu; Save to Homedrive ends on Home with *Open*.

## Notes for whoever continues

- React's `onWheel` and touch listeners are passive, so `preventDefault()` does nothing; use `addEventListener(..., { passive: false })`.
- `setPointerCapture` on a parent swallows clicks on child buttons in Chrome; `ZoomStage` ignores pointer events that start in `.zoom-bar`.
- The CSP is `style-src 'self'`: React `style` props are fine, `innerHTML` is not (the security review searches for it).
- Node tests run TypeScript directly (`node --test`); they can't load JSX, and imports without an extension need the `registerHooks` resolve hook (see `web/test/sheetGrid.test.ts`).
- Files created on Windows may get CRLF; run `gofmt -w` on Go files and keep TypeScript in LF.
- `go test -race` needs a C compiler and doesn't run on this Windows machine; CI runs it.
