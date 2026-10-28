# Homedrive

A self-hosted, "online drive"-style file manager for a home server (NAS). Snappy, lightweight and easy to use on desktop and phone.

This package is the **design handoff**: the interactive mockup source plus everything a developer needs to build the real app.

```
homedrive-mockup/
├── README.md              ← this file
└── design/
    ├── Main.dc.html       ← the full app mockup (desktop + responsive mobile layout, all logic)
    ├── Mobile.dc.html     ← phone frame (390×844) that mounts Main with mobile=true
    ├── Thumb.dc.html      ← file preview / thumbnail renderer used everywhere
    └── canvas.json        ← canvas layout of the artboards
```

> The `.dc.html` files are Design Component pages. They render inside the Claude Design canvas (they need its `support.js` runtime), so they are a **reference, not production code**. Open the canvas to click through the prototype; use the canvas **Share › Export** menu for PDF/PNG snapshots.

---

## 1. Product scope

| Area | What it does |
|---|---|
| **Browse** | Folders and files in a grid (with real content previews) or list (sortable by name, modified, size). |
| **Navigate** | Breadcrumb with home icon, a history-aware **Back** button ("Back to Photos"), and in list view a **vertical breadcrumb** ("You are here") to the left of the list. |
| **Sections** | My Files, Recent, Starred, Shared, Trash. Storage sits behind the **"nas.local · online"** pill in the header. |
| **Search** | Live filter across every folder by name. |
| **Add** | **+ New** (desktop sidebar) / floating **+** (mobile): Folder, Text document, Spreadsheet, Upload files. |
| **Open** | Clicking a file opens it: viewer for photos, video, audio, PDF, code, archives; **editors** for text (Markdown) and spreadsheets. |
| **Info** | ⓘ button on every card/row opens an info panel (side panel on desktop, bottom sheet on mobile). |
| **Share** | Share people on the server (Can view / Can edit) and a share link (Can view / Can edit, expiry Never / 7 days / 1 day, copy). |
| **Storage** | Capacity by type, disk health (mirrored RAID 1), largest files, free-up-space actions (trash, old backups, large videos). |
| **Trash** | Soft delete, restore, empty trash. |

All data in the mockup (files, people, sizes, disks) is **sample data**.

---

## 2. Screens & layout

### Desktop (≥ 700 px)
- **Header** (sticky, `#166534`): logo + "Homedrive", search, server pill (opens storage popover), avatar.
- **Sidebar** (220 px): **+ New**, nav list with counts.
- **Main**: breadcrumb + back button → H1 (28 px) + item count → view toggle (Grid / List) → quick access (starred folders, root only) → files.
- **Info panel**: 280 px column to the right of the files.

### Mobile (< 700 px, or `mobile` prop)
- Sidebar hidden; **bottom tab bar** (Files, Recent, Starred, Shared); **FAB +** above it.
- 2-column grid, 120 px thumbnails; info, share and add open as **bottom sheets**.
- Vertical breadcrumb hidden (horizontal breadcrumb scrolls sideways).
- The layout switches automatically on window resize (`window.innerWidth < 700`).

### Overlays
- **Viewer** (dark `#111827`): back, title + meta, Share, Download, Info drawer, prev/next, media controls for audio/video.
- **Text editor**: WYSIWYG page + formatting toolbar (Undo, Redo, H1, H2, Normal, Bold, Italic, bullets, numbers, quote), **Edit / .md** toggle to see/edit the Markdown source, editable file name, save status.
- **Spreadsheet editor**: formula bar (cell ref + raw value), A1-style grid, add row/column.
- **Share dialog**, **Add dialog**, **Server popover**, **Toast**.

---

## 3. Design system

Based on the organisation's design system (Inter, flat surfaces, Heroicons outline), recoloured to **green**.

### Colour tokens
| Token | Hex | Use |
|---|---|---|
| `primary-900` | `#166534` | Header, active nav/tab, strong accents |
| `primary-700` | `#15803d` | Primary buttons, links (white text passes 5:1) |
| `primary-100` | `#dcfce7` | Active nav background, selected toggle |
| `primary-50` | `#f0fdf4` | Selected card/row, quote blocks |
| `primary-400` | `#4ade80` | Online dot, progress on dark |
| `focus` | `#22c55e` | Focus rings (2 px) |
| `gray-50` | `#f9fafb` | Page background |
| `gray-200` | `#e5e7eb` | Borders |
| `gray-500` | `#6b7280` | Muted text |
| `gray-800` | `#1f2937` | Body text |
| `gray-900` | `#111827` | Titles, viewer background |
| `danger` | `#dc2626` / `#b91c1c` | Delete |
| `star` | `#f59e0b` | Starred |

### File type colours
| Type | Colour | Notes |
|---|---|---|
| Folder | `#a16207` on `#fef9c3` | Yellow so it doesn't clash with green UI |
| Text (.md) | `#0369a1` | Editable |
| Spreadsheet | `#15803d` | Editable |
| Word | `#1d4ed8` | |
| PDF | `#b91c1c` | |
| Image | `#0f766e` | |
| Video | `#6d28d9` | |
| Audio | `#be185d` | |
| Archive | `#b45309` | |
| Code | `#475569` | |

### Type, spacing, shape
- **Inter** 400/500/600/700. H1 28 px (24 mobile), card titles 15 px/600 (2-line clamp), body 14 px, meta 12 px.
- 4 px spacing scale; page gutter 16 px (12 mobile).
- Radius: 8 px controls, 12 px cards/buttons, 16 px dialogs/sheets, full for pills/avatars.
- Flat: borders, not shadows. Shadows only on floating things (header, dialogs, FAB, toast).
- Icons: Heroicons outline 24, 2 px stroke. Logo: original house outline with a drive and a green LED (no background).
- Touch targets ≥ 44 px; all text ≥ 4.5:1 contrast.

### Copy rules
Sentence case for body, Title Case for buttons and dialog titles. Middle dot `·` as separator (`PDF · 184 KB · 2 h ago`). File extensions are hidden in titles (shown under Info → File name).

---

## 4. Thumbnails (Thumb component)

Thumbnails show the **file's content**, not an icon. The type icon sits in front of the title instead.

| Type | Preview |
|---|---|
| Image | The image (mockup: generated landscape) |
| Video | Poster frame + play button + duration |
| PDF / Word / Text | Page with real first lines (headings, rows, totals) |
| Spreadsheet | First rows/4 columns of evaluated cells, coloured header row |
| Code | Dark editor with syntax-tinted lines |
| Archive | List of contained entries |
| Audio | Cover with title + waveform |
| Folder | 2×2 grid of its first four items |

**Production approach:** generate thumbnails server-side on upload/change and cache them (e.g. `libvips`/`sharp` for images, `ffmpeg` for video posters, `pdftoppm`/`pdf.js` for PDFs, render Markdown/XLSX to a small HTML/canvas snapshot). Serve as WebP at 2 sizes (grid ~400 px, list ~80 px). Folder previews compose the first four child thumbnails.

---

## 5. Interactions & motion

Motion is short (≤ 0.5 s), transform/opacity only, staggers capped at ~12 items, and **fully disabled with `prefers-reduced-motion`**.

| Moment | Animation |
|---|---|
| Section/view load | Cards flip in (`rotateX(-70°)` → overshoot → rest, 440 ms, 30 ms stagger); list rows slide in from the left; title pops |
| **Open folder – grid** | A yellow glow expands from the folder thumbnail to fill the area and fades; the first 4 items grow out of their exact mini-tile positions in the folder thumbnail and **spread** to their grid slots; remaining items pop from the folder centre (FLIP via Web Animations API, 520 ms spring) |
| **Go up – grid** | The folder you left shrinks back into its card (scale 1.7 → 1); siblings fade in |
| **Open folder – list** | The folder name slides **left into the vertical breadcrumb** (shared-element FLIP); contents slide in from the right |
| **Go up – list** | Rail item slides back into its row |
| Open file | Viewer fades in; content zooms up with a soft overshoot (`cubic-bezier(.34,1.56,.64,1)`, 340 ms); next/prev re-plays |
| Open editor | Slides up 40 px with overshoot |
| Dialogs | Pop from 0.88 scale (bottom sheets slide up on mobile) |
| Sharing | People pop into the list; link switch knob bounces; toast bounces in with a self-drawing ✓; **confetti burst** on share / link created / link copied / upload complete |
| Hover | Cards lift 3 px; share button grows; online dot pulses |

Implementation notes: the mockup uses a "parity" class swap to replay CSS animations, and `element.animate()` for FLIP. In React, use a small FLIP helper (or Framer Motion `layoutId` for the list→rail shared element and `AnimatePresence` for overlays).

---

## 6. Editors

### Text documents (Markdown)
- Stored on disk as **`.md`**. The UI is WYSIWYG; the **.md** toggle shows/edits the raw Markdown.
- Supported: `#`/`##`/`###`, paragraphs, `**bold**`, `*italic*`, `` `code` ``, `-` bullets, `1.` lists, `>` quotes.
- Mockup uses `contentEditable` + `execCommand` with a hand-written MD↔HTML converter. **For production use a proper editor** with Markdown round-tripping, e.g. **TipTap** (+ `tiptap-markdown`), **Milkdown** or **ProseMirror**.
- Autosave: debounce ~700 ms ("Saving…" → "Saved · Markdown"); save on close. Rename via the title field.

### Spreadsheets
- Stored as **`.xlsx`** (read/write with **SheetJS** or **ExcelJS** server-side).
- Grid with A1 refs, formula bar, add row/column.
- Formulas supported in the mockup: arithmetic with cell refs (`=B2-C2`), `SUM`, `AVERAGE`, `MIN`, `MAX`, `COUNT` over ranges; errors show `#ERR`. For production consider **HyperFormula** for a full engine.
- Thumbnail = evaluated first rows.

---

## 7. Suggested data model

```ts
type NodeType = 'folder' | 'text' | 'sheet' | 'image' | 'video' | 'audio' | 'pdf' | 'doc' | 'archive' | 'code';

interface Node {
  id: string;
  parentId: string | null;     // null = My Files root
  name: string;                // with extension on disk, shown without
  type: NodeType;
  size: number;                // bytes
  modifiedAt: string;          // ISO
  starred: boolean;
  trashedAt: string | null;
  durationSec?: number;        // audio/video
  thumbnailUrl?: string;
}

interface Share {
  nodeId: string;
  people: { userId: string; access: 'view' | 'edit' }[];
  link: null | { token: string; access: 'view' | 'edit'; expiresAt: string | null };
}

interface User { id: string; name: string; initials: string; color: string; }
```

## 8. Suggested API

```
GET    /api/nodes?parent=:id|root&sort=name|modified|size
GET    /api/nodes/:id                     → node + share + path (for breadcrumbs)
GET    /api/search?q=
GET    /api/recent | /api/starred | /api/shared | /api/trash
POST   /api/folders                       { parentId, name }
POST   /api/files                         { parentId, name, type: 'text'|'sheet' }
POST   /api/upload?parent=:id             multipart, resumable (tus) for big files
GET    /api/nodes/:id/content             raw file (Range support for video/audio)
PUT    /api/nodes/:id/content             save .md / .xlsx
PATCH  /api/nodes/:id                     { name?, parentId?, starred? }
DELETE /api/nodes/:id                     → trash   ( ?permanent=true to delete )
POST   /api/nodes/:id/restore
DELETE /api/trash                         empty trash
GET    /api/nodes/:id/thumbnail?size=sm|lg
GET|PUT /api/nodes/:id/share
GET    /s/:token                          public share link page
GET    /api/storage                       capacity by type, disks (SMART), largest files
GET    /api/server                        name, online, disk health
```

## 9. Suggested stack (lightweight)

- **Frontend:** React + Vite (or SvelteKit/Preact for an even smaller bundle), CSS variables from the tokens above, Heroicons, TipTap, SheetJS/HyperFormula. PWA for install on phones.
- **Backend:** Node (Fastify) or Go, serving files straight from a directory (e.g. `/srv/files`) with a small SQLite index for metadata, stars, shares and trash; file watcher (chokidar/inotify) to stay in sync with disk.
- **Thumbnails:** background worker (sharp, ffmpeg, pdftoppm), cached on disk.
- **Auth:** local accounts for household members; share links as signed tokens with optional expiry.
- **Deploy:** single Docker container behind Caddy (HTTPS on `nas.local`), as in the sample `docker-compose.yml`.

---

## 10. Accessibility checklist

- Real `<button>`/`<a>`/`<input>` everywhere; icon-only buttons have `aria-label`.
- Breadcrumbs use `<nav aria-label="Breadcrumb">` with `aria-current="page"`.
- Dialogs: `role="dialog"`, `aria-modal`, labelled titles; trap focus and close on Esc in production.
- Toasts are `role="status"`.
- Colours meet 4.5:1; never colour-only state (icons + labels).
- Respect `prefers-reduced-motion`.

## 11. Known mockup limitations

- Sample data only; uploads, downloads and links are simulated.
- WYSIWYG editing uses the browser's built-in `execCommand` (deprecated) — replace with a real editor.
- Spreadsheet engine is minimal; no cell formatting, no multiple sheets.
- Not tested across all browsers; animations rely on the Web Animations API.
