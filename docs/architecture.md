# Homedrive architecture

Status: agreed design. v1 is built; the parts marked *v2* are refined (2026-10-29) and built on the `v2/development` branch. Decisions here are binding until changed in this file.
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
| `backup_retention_days` | `30` | How long deleted and overwritten files stay in the backup. From v2 also how long old versions stay in the data folder (§4), with or without a backup. |
| `smtp_host`, `smtp_port`, `smtp_security` (`starttls`/`tls`), `smtp_username`, `smtp_password`, `smtp_from` | port `587`, `starttls` | Required: email is the second login factor. Plain-text SMTP is not supported. `HOMEDRIVE_SMTP_PASSWORD_FILE` reads the password from a Docker secret. |
| `trusted_proxies` | loopback + private ranges | Only requests from these addresses may set `CF-Connecting-IP` / `X-Forwarded-For`. |
| `log_level` | `info` | `debug`, `info`, `warn`, `error`. |
| `homeassistant_login` | `false` | v2, add-on only: allow logging in with a Home Assistant username and password (§5). |
| `app_name` | empty (= "Homedrive") | v3: the name shown to users instead of "Homedrive" (below). |
| `listen` (env only) | `:8080` | |
| `web_dir` (env only) | `/app/web` | Built frontend. |

### App name (v3)

`app_name` gives the installation its own name, for example "Van Baelen Drive". It is called app name and not drive name because "drive" already means My Files and the shared drives.

- At most 40 characters, one line, no control characters; leading and trailing spaces are dropped. Not translated. Empty or missing means "Homedrive", and then nothing below shows twice.
- **With the logo** (the sidebar and phone header, the login, setup, invite and recovery pages, the Home Assistant login page, link pages for visitors, the email header): the name in the logo's place of the wordmark, with "Homedrive" under it as a small gray line (`--text-muted`, about 11 px). A long name is cut off with "…" in the header and wraps elsewhere.
- **Instead of "Homedrive"** in text: browser tab titles ("Photos – Van Baelen Drive"), the installed app's name and the Android share sheet (manifest `name` and `short_name`), page texts such as "Sign in to Van Baelen Drive", email subjects and texts ("4829 1735 is your Van Baelen Drive sign-in code"), the email sender's display name ("Van Baelen Drive", "Anna via Van Baelen Drive"), push notification titles, and the name the iPhone Shortcut instructions suggest ("Save to Van Baelen Drive").
- **"Homedrive" stays** where it names the software: the gray line under the name, email footers ("Van Baelen Drive · runs on Homedrive at drive.example.com"), the invitation's explanation ("… runs on Homedrive, on a server at home"), the add-on in Home Assistant, logs, the CLI, and the `.homedrive` markers.
- The server fills the name into `index.html` (`<title>`, `application-name` and `apple-mobile-web-app-title`) and serves `manifest.webmanifest` with it, HTML- and JSON-escaped, so the first paint already shows it. The app reads it from the `application-name` meta tag, so no extra request is needed; the emails take it from the config. The service worker's cached app shell picks up a new name with the next update. In email headers it is quoted and encoded like any display name.
- Changing it takes a restart. Android updates an installed app's name by itself after a while; on an iPhone the home screen keeps the name it was added with until it is added again.

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
meta            (key, value)                      instance_id, schema info, link_grant_key (v2)
users           (id, email UNIQUE, name, password_hash, role admin|member, locale, created_at, disabled_at,
                 ha_username UNIQUE NULL)                                   ha_username: v2
sessions        (id_hash, user_id, created_at, last_seen_at, expires_at, recent_auth_at, user_agent, ip)
email_codes     (id, user_id, purpose, code_hash, expires_at, attempts, used_at)
invitations     (token_hash, user_id, purpose invite|reset, created_by, created_at, expires_at, used_at)
device_tokens   (id, user_id, name, token_hash, scope, created_at, last_used_at, revoked_at)   iOS Shortcut uploads
drives          (id, kind personal|shared, name, owner_user_id NULL for shared, root_id, created_at,
                 join_by_default)                                           join_by_default: v2
drive_members   (drive_id, user_id, role viewer|editor|manager)
nodes           (id, drive_id, parent_id, kind folder|file, name, current_version_id, size, mime,
                 created_by, created_at, modified_at, trashed_at, trashed_by,
                 description, description_key)                              description: v2
                 UNIQUE (drive_id, parent_id, lower(name)) WHERE trashed_at IS NULL
versions        (id, node_id, object_id, size, sha256, created_by, created_at, superseded_at,
                 edit_lock_id NULL)                                         edit_lock_id: v2
uploads         (id, user_id, parent_id, name, size, received, created_at, expires_at,
                 replace_node_id NULL)                                      replace_node_id: v2
media           (version_id, duration_ms)                                   v2, video and audio length
edit_locks      (node_id, lock_id, user_id, acquired_at, expires_at)        v2
link_previews   (node_id, url, site, author, fetched_at)                    v2, saved links
stars           (user_id, node_id)
recents         (user_id, node_id, at)
people_shares   (node_id, user_id, access view|edit, created_by, created_at)                  v2
links           (node_id UNIQUE, token, token_hash UNIQUE, expires_at, password_hash,
                 created_by, created_at, updated_at)                        v2, view-only
audit_log       (id, at, user_id, ip, event, detail)
notification_prefs (user_id, kind, email on|off, push on|off)               v3, only rows that differ from the default
push_subscriptions (id, user_id, session_hash, endpoint, p256dh, auth, user_agent, created_at, last_ok_at)   v3
sign_in_devices (user_id, device_key, first_seen_at, last_seen_at)          v3, for new sign-in alerts
```

- **Drives from day one.** Every user has a personal drive ("My Files"). v1 ships one shared drive, "Family", with every user as editor. From v2 anyone can create shared drives with members and roles (below).
- IDs are random (UUIDv7 for rows, 128-bit random for objects). Every API call that takes an ID checks access through the drive membership or a share — never "the ID was valid".

### Shared drives and roles (v2)

- Anyone can create a shared drive with *New shared drive* (desktop sidebar, phone *Shared* tab) and becomes its manager. Drive names follow the file name rules, are at most 60 characters, and don't have to be unique.
- Roles:

  | | Viewer | Editor | Manager |
  |---|:-:|:-:|:-:|
  | Browse, open, download, download as `.zip`, star | ✓ | ✓ | ✓ |
  | Add, upload, edit, rename, move, trash and restore items | | ✓ | ✓ |
  | Delete forever and empty the drive's trash (password again) | | ✓ | ✓ |
  | Share items with people, make public links | | ✓ | ✓ |
  | Rename the drive, add and remove members, change roles | | | ✓ |
  | Delete the drive when it is empty, trash included | | | ✓ |

- The owner of a personal drive has a manager's rights on it. A personal drive has no members and can't be renamed or deleted.
- Every member can see who is in the drive (*Members* in the drive's header). Managers add people from every account that isn't disabled (invited people who haven't signed in yet included), change roles and remove members. Any member can leave. A drive always keeps a manager: the last one can't leave, be removed or be given another role.
- A manager can delete the drive only when nothing is left in it, not even in its trash.
- **Family.** v1's "Family" drive becomes a normal shared drive. The migration makes admins its managers; everyone else stays an editor. v1 stored the name in English and translated it on screen; now the stored name is shown, so the migration renames it "Familie" when the first admin uses Dutch. A new installation creates the first shared drive during setup, named "Family" or "Familie" after the admin's language, with the admin as manager.
- **New users** are no longer added to a drive automatically. The invite form lists every shared drive with a checkbox. Drives marked *join by default* (`drives.join_by_default`: Family after the migration and at setup) are ticked. New members join as editors.
- **Admins** see every shared drive on the Users page (*Shared drives* tab): name, size, members with their roles, and whether new users get it ticked. They can add and remove members and change roles on any shared drive, including adding themselves. They can't open a drive's files unless they are a member. This is how an admin fixes a drive whose last manager was disabled, and how someone joins drives after their invitation.
- A disabled user keeps their memberships, so enabling them again restores their access. A disabled manager still counts as the drive's manager.
- Moving items between drives works as in v1 (edit rights on both sides). Shares and the public link move with the item.
- Audit: `drive.create`, `drive.rename`, `drive.delete`, `drive.member` (add, role, remove, leave; with `admin` when done through the admin list), `drive.default` (an admin turns *join by default* on or off).

**Navigation (v2).**

- Desktop sidebar: *+ New*, My Files, a *Shared drives* group with each drive by name and *New shared drive*, Shared with me, Recent, Starred, Trash. A storage meter at the bottom opens the Storage page.
- Phone tab bar: Files, Shared, Recent, Starred, Trash. *Shared* lists the shared drives with *New shared drive*, then the items shared with you. Storage is in the account menu.
- Routes: `/d/<drive-id>` is a shared drive's top folder, `/shared` is Shared with me (on a phone also the drives), `/storage` the Storage page. `/family` and `/backup` redirect to the first shared drive and to Storage, so old bookmarks keep working.
- The drive's own name replaces the fixed "Family" label everywhere: sidebar, breadcrumbs, the move dialog, and item locations in search, Recent, Starred and Trash.

### Sharing with people (v2)

- Anyone who can edit an item on its drive can share it: the owner of My Files, and editors and managers of a shared drive. People it is shared with can't share it further.
- The Share dialog (mockup) lists who has access: the drive's members as one line ("Members of Holiday house"), then each person it is shared with, set to *Can view* or *Can edit*, with *Remove*. *Add people* picks from the same list as drive members (`/api/people`), leaving out people who already have that much access through the drive.
- `people_shares` holds one row per item and person. A user's access to an item is the strongest of their drive role and every share on the item or a folder above it.
- **Can view** works like a viewer, for that item and everything in it.
- **Can edit** on a folder: add, upload, edit, rename and move items within the shared folder, and trash them. Not allowed: moving anything out of it, deleting forever, sharing or making links, and renaming, moving or trashing the shared folder itself. **Can edit** on a file: change its contents and restore its versions, but not rename, move or trash it.
- Items a recipient trashed show up in their Trash and in the drive's trash. Both they and the drive's editors can restore them.
- Recipients find shared items under *Shared with me* (name, who shared it, when) and in search, Recent and Starred. Breadcrumbs start at the shared item ("Shared with me › Holiday › Day 1"): the names of the folders above it stay private.
- Shares belong to the item, not to the person who made them. They follow it through renames and moves (also to another drive), stay when that person leaves the drive, are hidden while the item is in the trash, and end when it is deleted forever or the share is removed. A disabled user's shares grant nothing until they are enabled again.
- Cards and rows show a people icon on items that are shared or have a public link, to people who can manage sharing. Recipients see "Shared by <name>" in the info panel.
- Audit: `share.add`, `share.change`, `share.remove`.

### Public links (v2)

- One link per file or folder, managed in the Share dialog by anyone who can share the item. Links are view-only: visitors can look and download, never change anything.
- Settings: expiry *Never*, *7 days* or *1 day* (counted from when it is set) and an optional password (at least 8 characters, Argon2id like account passwords). Changing the settings keeps the address. Turning the link off deletes it; turning it on again makes a new address, so the old one stays dead.
- The address is `https://drive.example.com/s/<token>` with a 128-bit random token (22 characters, base64url). The full token is kept in `links.token`, so *Copy link* always works (decided 2026-10-29). Requests find the link through its SHA-256 in `links.token_hash`. The trade-off: anyone who has the database or a backup of it can use the live links (see [`security.md`](security.md#share-links-v2)).
- **Visitor page** `/s/<token>`, served by the PWA: the Homedrive logo and the item's name, never the names of people or descriptions. A file is shown as in the viewer (same inline rules, HEIC through its preview) with *Download*. A folder can be browsed (breadcrumbs start at the shared folder) with thumbnails; files open in the viewer and download one by one, and *Download all* gives a `.zip`. No search, stars or info panel.
- With a password the page asks for it first. The right password sets a cookie `__Host-hd_link_<link-id>` (Secure, HttpOnly, SameSite=Lax, 12 hours) holding an HMAC over the link and its password hash, signed with `meta.link_grant_key`. Changing or removing the password ends the grants given before. A cookie rather than a header is needed so `<img>`, `<video>` and `Range` downloads work.
- An unknown, expired or switched-off link and a link to an item in the trash all get the same "This link doesn't work" page. A link to a restored item works again unless it expired. Every node ID in a link request must be inside the linked item and not in the trash.
- Links belong to the item like shares: they move with it and survive the person who made them leaving the drive.
- Limits per client address: 20 unknown tokens per 10 minutes; 5 wrong passwords per link per 15 minutes, and 50 per link per hour over all addresses; one `.zip` at a time, and two over all links. Link pages and their API answers carry `X-Robots-Tag: noindex, nofollow` and `Cache-Control: no-store` (file contents keep `private, no-cache`).
- Request logs show the token as `…`. Audit: `link.create`, `link.change`, `link.remove`. Visits aren't logged.

**Download as `.zip`.** A folder downloads as `<folder name>.zip`: for signed-in users from its info panel (any folder they can open), and on link pages (*Download all*). The zip is streamed with Go's `archive/zip` without compression (*Store*): documents and photos are mostly compressed already, and a Raspberry Pi stays responsive. UTF-8 names, Zip64 when large, modification times kept, empty folders included, items in the trash left out. The size isn't known in advance. At most 2 zips per user at a time.

### Descriptions and saved links (v2)

**Descriptions.** Every file and folder can have a description, for example the caption of a saved video.

- Stored in `nodes.description` (`TEXT NOT NULL DEFAULT ''`). It belongs to the item, not to a version: it stays when a new version is uploaded and follows the item through rename, move, trash and restore.
- Plain text, at most 5,000 characters, trimmed, line breaks kept. The web app renders it as text only, never as HTML or Markdown.
- Edited in the info panel by anyone who may edit the item. Changing it does not change `modified_at`, which tracks the content.
- Search matches names and descriptions. `description_key` is the lower-cased copy, like `name_key`, because SQLite's `lower()` and `LIKE` only fold ASCII. If `LIKE` gets slow, switch to FTS5.
- Backups include it through the database snapshot. `homedrive export` writes a non-empty description next to its file as `<name>.description.txt`, so it survives a restore without the app.
- Sharing: when a share holds files *and* text, the Save page prefills each file's description with the text (title and text) instead of dropping it. The iPhone Shortcut can send a description with `POST /api/inbox` in the `X-Homedrive-Description` header (percent-encoded), so it stays out of URLs and logs.

**Saved links.** A share that holds a URL and no files becomes a saved link instead of a `.txt`.

- A link is a normal file `<title>.url` in the Windows internet-shortcut format (`[InternetShortcut]` / `URL=https://…`). Move, trash, search, backup and export work unchanged, and an exported link still opens on a PC. The title is the shared title, else the oEmbed title, else the host, and can be edited on the Save page. The shared text or caption becomes the description.
- Only `http` and `https` URLs, at most 2,048 characters, without user info. `.url` files get the type `link` and are never served inline. The viewer shows a link card instead of the file's text. The card opens the URL with `rel="noopener noreferrer"`, so on a phone YouTube and TikTok links open in their apps.
- `link_previews` keeps the parsed URL, site and author for listing. It is written whenever a `.url` version is saved, including one uploaded from a PC. The node name is the title.
- **oEmbed lookups** run on the server, only against a fixed list of providers: `https://www.youtube.com/oembed` for `youtube.com` and `youtu.be` (including `/shorts/`), and `https://www.tiktok.com/oembed` for `tiktok.com` and `vm.tiktok.com`. The user's URL only ever travels as a query parameter: Homedrive never connects to a host taken from user input. Limits: 5 s timeout, 64 KB response, no redirects, 30 lookups per user per minute. The Save page calls `GET /api/link-preview?url=…` to prefill the title and caption. Saving never depends on the lookup: if it fails, the card shows the site's icon.
- **Thumbnails:** the `thumbnail_url` from the oEmbed answer is fetched only when its host is on that provider's list (`i.ytimg.com`; `*.tiktokcdn.com`, `*.tiktokcdn-us.com`). It must be an image of at most 2 MB, goes through the normal thumbnail pipeline and lands in the thumbnail cache. On a cache miss the server repeats the oEmbed lookup, because TikTok's thumbnail URLs expire. The browser never loads anything from the providers, so they don't learn when a card is viewed and the Content-Security-Policy stays as it is.
- Instagram's oEmbed needs a Facebook developer app, so Instagram and all other sites get a card from the shared title and the host only. Homedrive never fetches arbitrary pages for previews.
- **iPhone:** the Shortcut sends links to `POST /api/inbox/link` (JSON `{url, title, text}`, same device token scope `upload`), which saves the link to the Inbox in the same way.
- Not in v2: playing videos inside Homedrive. That would need `frame-src` exceptions for the providers' embedded players.

### Thumbnails and previews (v2)

Thumbnails show the file's contents, not an icon (mockup §4). The server makes and caches them; the browser only lays out text snippets.

| Type | Thumbnail |
|---|---|
| Images: JPEG, PNG, WebP, GIF, AVIF, HEIC/HEIF, TIFF, BMP | libvips (`vips thumbnail`), turned upright from EXIF |
| Video | a frame at 10 % of the length (at most 30 s in) through ffmpeg, then libvips; plus the length |
| Audio | the embedded cover art through ffmpeg, else the mockup's title-and-waveform card; plus the length |
| PDF | the first page through `pdftoppm` (poppler), then libvips |
| Text, Markdown, code | *snippet*: the first 12 lines (at most 2 KB), drawn as a page, or as a dark code block for code |
| Spreadsheets (`.xlsx`, `.csv`) | *snippet*: the first 6 rows × 4 columns as Excel shows them (`.xlsx` through excelize, from the cached results) |
| Zip archives | *snippet*: the first 8 entry names and the total, read from the archive's directory only |
| Saved links | the oEmbed thumbnail (above), else the site's icon |
| Folders | 2×2 of the first four items' thumbnails (v1's minis, now with thumbnails) |
| SVG, Word, `.xls`, `.ods`, other archives, anything else | the type's placeholder (Word previews are *Later*) |

- Images are WebP in two sizes, `grid` (400 px on the long side) and `list` (80 px): `GET /api/nodes/{id}/thumb?size=grid|list&v=<version>`. Snippets are JSON: `GET /api/nodes/{id}/snippet?v=<version>`. Each node in a listing says which kind it has (`thumb: "image" | "snippet" | ""`) and carries its current `version`, so a new version gets a new URL. Answers use `Cache-Control: private, no-cache` like file contents, with the version as `ETag`.
- Cache: `<state dir>/cache/thumbs/<first 2 characters>/<version-id>-<size>.webp` (and `.json`). It is not in any backup (Home Assistant backups exclude `*/cache/*`) and can be deleted at any time. The hourly cleanup removes entries whose version is gone.
- A background worker makes them after each upload or save. A request for a missing one makes it on the spot and waits up to 15 s. At most 2 tools run at a time. A file that fails gets a `.failed` marker for that version and shows the placeholder; it isn't tried again until a new version.
- **Viewer previews:** HEIC/HEIF and TIFF, which most browsers can't show, open in the viewer as a WebP of at most 2048 px from `GET /api/nodes/{id}/preview`, made and cached the same way. *Download* still gives the original.
- **Lengths** of video and audio go into `media` and show on cards and in the info panel.
- Tools: Alpine's `vips-tools` (with `vips-heif`), `ffmpeg` and `poppler-utils`, about 150 MB on the image. Without them (a development machine) every file shows its placeholder; one log line at startup says which tools are missing.
- Each tool runs as a sandboxed subprocess: time, CPU and memory limits, a pixel limit, no shell, nothing from the user on its command line, and ffmpeg can't open other files or the network ([`security.md`](security.md#files)).

### Storage page (v2)

`/storage`, opened from the storage meter at the bottom of the desktop sidebar or the account menu on a phone. The v1 Backup page moves into it.

For everyone:

- **Your files:** the size of My Files, split by type (documents, spreadsheets, PDFs, images, video, audio, archives, other) as one bar with a legend (mockup), plus its trash and old versions on their own lines.
- **Largest files:** the 10 largest files on the drives you can edit (My Files and shared drives where you are editor or manager), with where they are. Each can be opened or moved to the trash.
- **Free up space:** *Empty trash* (with the size it frees, for the drives you can edit, like the Trash page) and *Delete old versions* (versions replaced by newer ones on the drives you can edit, with the size; asks for your password). Otherwise old versions go by themselves after `backup_retention_days`.

For admins also:

- **Data disk:** used, free and total of the disk under `data_path`, with the 512 MB that uploads keep free.
- **Usage per person** (My Files with its trash and old versions) and **per shared drive** (with its number of members). Sizes only: admins see no file names this way.
- **Backup:** the v1 Backup page (last run, duration, size, errors, *Back up now*).

The storage meter shows "<size> used" (My Files) to members. Admins see a bar of the data disk instead.

Left out from the mockup: disk health and RAID (a container can't read them reliably), *old backups* (backup retention is automatic) and *large videos* (the largest files list covers it). Quotas are not in v2.

### Editors, versions and edit locks (v2)

Editors open for people who can edit the file: the owner of My Files, editors and managers of its drive, and people it is shared with to edit. Everyone else gets the read-only view.

**New files.** *+ New* → *Text document* makes `Untitled document.md` ("Naamloos document.md") and *Spreadsheet* `Untitled spreadsheet.xlsx` ("Naamloos rekenblad.xlsx") in the current folder (`POST /api/files`), with `(2)` when the name is taken, and opens the editor.

**Markdown (`.md`).**

- TipTap with `tiptap-markdown`: a WYSIWYG page with the mockup's toolbar (Undo, Redo, H1, H2, Normal, Bold, Italic, bullets, numbers, quote) plus *Link*, and the *.md* toggle to edit the Markdown source. The file name is edited in the title field.
- Supported: headings 1–3, paragraphs, bold, italic, inline code, code blocks, bullet and numbered lists (nested), quotes, links (`http`, `https`, `mailto`), horizontal rules and line breaks. Raw HTML is never rendered; it stays visible as text.
- The editor never rewrites what it doesn't understand: on opening, the file is read and written back in memory. If that changes it (tables, footnotes, front matter, other spacing — line endings and the final newline aside), the file opens in source mode with a note, and stays there.
- People who can't edit see the same rendering, read-only.

**Text and code** (`.txt`, `.log`, `.csv` and the code types): a plain text editor, the source mode without the toolbar, in a monospace font. Only UTF-8 files up to 1 MB; others open in the read-only text viewer. Windows line endings stay Windows line endings.

**Spreadsheets (`.xlsx`).**

- A grid like the mockup: A1 references, a formula bar (cell and its raw value or formula), *Add row* and *Add column*.
- Formulas: arithmetic (`+ - * /`, brackets) with numbers and cell references, and `SUM`, `AVERAGE`, `MIN`, `MAX` and `COUNT` over cells and ranges. Errors and circular references show `#ERR`. The engine is a small TypeScript module that recalculates as you type.
- The server converts with excelize: `GET /api/nodes/{id}/sheet` gives the grid as JSON (values as Excel shows them, raw values, formulas). `PUT` writes the changed cells into the original workbook, so styles, column widths and number formats stay. It stores each formula with its result as calculated by the browser, so previews and other apps show values, and marks the workbook to be recalculated when Excel opens it.
- Editable only with one sheet, no merged cells, at most 1,000 rows and 52 columns, and only the formulas above (the server checks with the same grammar). Anything else opens read-only with a note: the values of each sheet with sheet tabs, and *Download*.
- `.xls`, `.ods` and `.csv` aren't opened in the grid (`.csv` opens as text).

**Edit locks.** One person edits a file at a time.

- Opening the editor takes a lock: `POST /api/nodes/{id}/lock` returns a `lockId`. The editor renews it every 30 s (`PUT`) and gives it back on close (`DELETE`, sent with `keepalive`). A lock expires 2 minutes after its last renewal.
- When someone else holds it, the file opens read-only with "Anna is editing" and checks every 30 s whether the lock is free. When you hold it yourself in another window or device, *Edit here* takes it over; the other window turns read-only with a note.
- A lock only blocks content changes: saves from other editors and *Replace* uploads. Rename, move and trash still work.

**Saving and conflicts.**

- Autosave 700 ms after the last change, one request at a time, "Saving…" → "Saved"; also on close. Text and Markdown go to `PUT /api/nodes/{id}/content`, spreadsheets to `PUT /api/nodes/{id}/sheet`. Each save carries the lock ID and the version it started from (`If-Match`).
- When the file changed in the meantime (the lock lapsed while offline, or someone uploaded with *Replace* before the lock was taken), the save is refused with `edit_conflict`. The editor says who changed it and when, and offers *Reload* (drop your changes) or *Save as a copy* (`<name> (copy).md` next to it, or in My Files if the folder is gone).

**Versions.**

- Every saved content is a version: an upload with *Replace*, an editing session, a restore. Autosaves within one editing session (one lock) update that session's version (`versions.edit_lock_id`): each save writes a new object, and the one it replaces is deleted.
- A replaced version gets `superseded_at`. It stays for `backup_retention_days`, then the hourly cleanup deletes it with its object. *Delete old versions* on the Storage page does this early.
- The info panel of a file has *Versions*: the current one first, then older ones with date, who saved it and size. Anyone who can open the file can open an old version in the viewer (`GET /api/nodes/{id}/versions/{version-id}/content`) or download it. Editors can *Restore*: the old contents are copied into a new current version, so nothing is lost and objects are never shared between versions.

**Replace or keep both.** When an upload has the name of a file already in the folder (ignoring case), the app asks *Replace* or *Keep both*, with *Do this for all* when several files collide.

- *Replace* makes the upload a new version of that file: it keeps its name, ID, description, shares, link and stars. It needs edit rights and is refused while someone else holds the edit lock.
- *Keep both* adds `name (2)` as in v1. A folder with that name only allows *Keep both*.
- `POST /api/uploads` takes `conflict: "ask" | "replace" | "keep"`. With `ask`, a taken name answers `name_taken` with the existing item before any bytes are sent. *Replace* records `uploads.replace_node_id` and is checked again on completion.
- The Save page (Android share target) asks the same. iPhone Shortcut uploads always keep both.

### Email and push notifications (v3)

The mockups are in [`homedrive-mockup/emails.html`](../homedrive-mockup/emails.html) (open it in a browser). Their content is an example: they don't add features beyond what is described here.

**Building the emails.**

- Every email is HTML with a plain-text part, in the recipient's language: `multipart/alternative` with the text first, then `multipart/related` with the HTML and its images.
- Shared parts, as in the mockups: the logo header, a white card with a heading and text, one main button, the link written out below it for when the button doesn't work, a grid of details (device, network, time), an amber notice box, and a footer with the `public_url` host and a link to the notification settings.
- Mail clients ignore most CSS, so the HTML uses tables and inline styles only. Gmail and Outlook don't show SVG, so the logo, icons and thumbnails are PNGs attached inline (`cid:`). That also works when the mail provider can't reach `public_url` and doesn't ask to "load images". Inter is named first; most clients fall back to the system font.
- One Go template per email (`html/template` and `text/template`, embedded in the binary), with the English and Dutch strings next to it. With a `localhost` `public_url`, `/dev/emails` shows every email with sample data, for review.
- Times are written in the server's time zone (the add-on gets Home Assistant's).
- The sender is `smtp_from`. Emails caused by a person show "<person> via <app name>" as its display name, the rest the app name (§2, *App name*). Subjects, texts and the header use the app name; the footer says it runs on Homedrive.

**Emails.**

| Email | To | Content |
|---|---|---|
| Sign-in code (login, setup, recovery) | the user | The code in the subject and the first sentence, so phones offer to fill it in; in monospace boxes grouped 4 + 4, selected with one tap where the client supports it. Expiry time, the device, network and time of the request, and "Didn't try to sign in?" with a link to Account Settings. |
| Invitation | new user | What the app name stands for in plain words (a private drive on a server at home, running on Homedrive), who invited them, *Set up my account*, the expiry date, and the shared drives picked on the invite form. |
| Password reset | the user | Started by an admin (§5): "<admin> asked for a new password for you", *Choose new password*, valid 24 hours, the old password keeps working until then. |
| Shared with you | the recipient | Who shared what and with which access, the optional note, the item's thumbnail (v2 thumbnails, folders as 2×2) with its type, count and size, and *Open*, which needs a login. |
| Added to a shared drive, role changed | the member | Drive name, role, who did it, *Open drive*. |
| New sign-in | the user | See below. |
| New device key, password changed, email changed | the user (email change: the old and the new address) | What changed, when, from which device, and what to do if it wasn't them. |
| Storage almost full | admins | The data disk at 85% and at 95%: used of total, usage by type in the Storage page's colours, and the Storage page's *Empty trash* and *Delete old versions* with what they free. Buttons open `/storage`; nothing is done from the email. At most once a week per level, again after it went below. |
| Backup failed | admins | A failed nightly backup, or a missing `backup_path` or marker mismatch: what failed and when, the last good backup, *Open storage*. Once a day while it keeps failing. |

- **Share note.** The Share dialog gets an optional note (at most 500 characters) when adding people. It goes into the email and the push notification and isn't stored. It is shown as a quote, apart from Homedrive's own text.
- **New sign-in.** Sent when a sign-in comes from a device the account hasn't used in the last 90 days: browser and operating system from the user agent (`sign_in_devices.device_key`). The email says "if this was you, you don't need to do anything", and flags *Outside your home network* when the client address (§6) isn't private (RFC 1918, ULA, loopback). No location: that would need a GeoIP database or an outside service. *Secure my account* opens a page with a confirm button (mail scanners open links by themselves); confirming signs that session out and asks the user to sign in and choose a new password. Its link is single use and valid 7 days.

**Push.**

- Web Push with VAPID (RFC 8030, 8291, 8292). The key pair is made at first start and kept in `meta`.
- Turned on per device in Account Settings → Notifications (*Notify me on this device*), which asks the browser for permission. A subscription belongs to the session that made it and ends with it, so a signed-out or revoked phone gets nothing. It is also dropped when the push service answers 404 or 410.
- iPhone: only when Homedrive is on the home screen (iOS 16.4 and later); the settings page says so.
- The payload is encrypted for the device: a title, a short text and the URL to open. The service worker shows it and opens the URL on tap. Sign-in codes are never sent by push.

**Settings.** Account Settings → *Notifications*: one row per kind with an *Email* and a *Push* switch. There is no daily summary: every email goes out when it happens. The security emails (sign-in code, new sign-in, new device key, password and email changed) can't be turned off; their push can. Admin kinds show only for admins. Footer links in the emails open this page after login.

**Sending.** A failed send is retried 3 times over an hour and then logged. Sign-in codes are not retried: the user asks for a new one.

## 5. Authentication

- **No self-registration.** The admin creates accounts; users receive an email invitation.
- **First admin:** on first start with no users, Homedrive logs a one-time setup link `https://…/setup#<token>` (valid 1 h, token in the URL fragment so it never reaches proxies or Cloudflare logs). The setup page creates the admin and verifies the email address with a code, which also proves SMTP works.
- **Login:** email + password, then a code sent by email (second factor). Codes are 8 digits, valid 10 minutes, single use, stored hashed, 5 attempts.
- **Passwords:** Argon2id (64 MiB, t=3, p=1), minimum 12 characters, a blocklist of common passwords, at most 2 hashes computed concurrently so login can't exhaust memory.
- **Sessions:** 256-bit random token in a `__Host-hd_session` cookie (Secure, HttpOnly, SameSite=Lax, Path=/), only its SHA-256 stored. Sliding 30-day expiry, absolute 90 days. Device list with revoke. Sensitive actions (password, email, user admin, permanent delete, device tokens) require the password again within 15 minutes.
- **Password reset:** by the admin only (email is already the second factor, so an email reset link would collapse login to one factor). Admin lockout: set the add-on option / env `admin_recovery=true` and restart to get a new one-time link in the log. A self-service *Forgot password?* is planned for VX; it needs a design that keeps two factors, for example an admin approving the request.
- **Approve link (VX):** the email with a code also gets an *Approve sign-in* button, for login, first setup and admin recovery. The link (`/approve#<token>`, in the fragment like the setup link) opens a page with the device, network and time of the waiting sign-in and an *Approve* button; opening the link alone does nothing, because mail scanners open links by themselves. Approving fills in the code for the page that is waiting: it checks every 2 seconds and continues by itself. The device that taps the link is not signed in by it, so a forwarded email or someone else reading the mailbox gets no session; if it is the same browser, the waiting tab continues as usual. The token is part of the code's row in `email_codes`: single use, the same 10 minutes, and spent together with the code. The typed code keeps working.
- **Rate limits:** per client IP and per account, progressive delays, plus a cap on codes sent per account per hour (no email bombing). Login responses don't reveal whether an account exists.
- **iPhone uploads:** an Apple Shortcut authenticates with a per-device token (scope: upload to own inbox only), created by the user after re-authentication and revocable in the device list.
- **Home Assistant login (v2, add-on only):**
  - Off by default. The add-on option `homeassistant_login: true` turns it on, and `config.yaml` sets `auth_api: true` so the add-on may ask the Supervisor to check credentials. Standalone Docker has no Supervisor and never shows it.
  - The login page then shows a "Log in with Home Assistant" button under the email form. It opens `/login/homeassistant`: the Home Assistant logo and the Homedrive logo joined by a chain link at the top, then username and password, in the mockup's visual style. Using the Home Assistant logo has to stay within the Open Home Foundation's brand guidelines.
  - The server sends the credentials to `POST http://supervisor/auth` with the add-on's `SUPERVISOR_TOKEN`. Homedrive never stores the Home Assistant password, and never gets a Home Assistant access token.
  - The username is matched against `users.ha_username` (lower case, unique, nullable). Admins set it in user management. Users link their own account in Account Settings after re-authentication. There is no automatic account creation.
  - After a correct password, the email code follows exactly as for a normal login. The Supervisor check skips Home Assistant's own MFA, and Homedrive is public.
  - The same rate limits as the normal login apply (per IP and per account). An unknown, unlinked or disabled user gets the same "wrong username or password" answer as a wrong password. Events go to the audit log as `login.homeassistant`.
  - Email and Homedrive password keep working for linked users, for example while Home Assistant restarts.

## 6. HTTP surface

- `/api/…` JSON API (shape as sketched in the mockup README), `/s/<token>` share pages (v2), `/healthz`, static PWA.
- **v2 additions** (access rules in §4):

  ```
  POST   /api/drives                            {name} → new shared drive, caller is manager
  PATCH  /api/drives/{id}                       {name}                                  manager
  DELETE /api/drives/{id}                       only when empty                         manager
  GET    /api/drives/{id}/members                                                       members
  PUT    /api/drives/{id}/members/{user-id}     {role}                                  manager
  DELETE /api/drives/{id}/members/{user-id}     remove (manager) or leave (yourself)
  GET    /api/people                            accounts that aren't disabled: id, name, email
  GET    /api/admin/drives                      every shared drive: members, size, joinByDefault
  PATCH  /api/admin/drives/{id}                 {joinByDefault}
  PUT    /api/admin/drives/{id}/members/{user-id}, DELETE …                             admin
  POST   /api/admin/users                       v1, plus driveIds (absent: the join-by-default drives)
  GET    /api/shared                            shared with me: each item, who shared it and when
  GET    /api/nodes/{id}/shares                 drive, people, inherited shares and link  can share
  PUT    /api/nodes/{id}/shares/{user-id}       {access: view|edit}, DELETE …           can share
  GET    /api/nodes/{id}/link, PUT …, DELETE …  public link {expiry, password}          can share
  GET    /api/nodes/{id}/zip                    folder as .zip                          can open
  GET    /api/nodes/{id}/thumb, /snippet, /preview
  GET    /api/nodes/{id}/versions, GET …/versions/{version-id}/content
  POST   /api/nodes/{id}/versions/{version-id}/restore
  POST   /api/nodes/{id}/lock, PUT …, DELETE …  edit lock
  PUT    /api/nodes/{id}/content                text and Markdown (If-Match, lock)
  GET    /api/nodes/{id}/sheet, PUT …           spreadsheet grid (If-Match, lock)
  POST   /api/files                             {parentId, type: text|sheet}
  GET    /api/storage, GET /api/admin/storage, DELETE /api/old-versions
  GET    /api/link-preview?url=…, POST /api/inbox/link                                   saved links
  GET    /api/s/{token}                         link page: the item (folder: its contents)
  GET    /api/s/{token}/nodes/{id}              an item inside it, with its path from the linked item
  GET    /api/s/{token}/nodes/{id}/content, /zip (with thumbnails also /thumb, /snippet, /preview)
  POST   /api/s/{token}/unlock                  {password} → grant cookie
  ```
- **Host check:** requests whose `Host` isn't the `public_url` host are rejected (stops DNS rebinding and direct LAN access with spoofed headers). `/healthz` is exempt.
- **CSRF:** SameSite cookies + `Origin` must equal `public_url` for every state-changing request + API only accepts `application/json` (or the upload content type on upload routes).
- **Uploads:** chunked and resumable, chunks ≤ 32 MiB because Cloudflare rejects request bodies over 100 MB on Free/Pro plans. Each chunk must start where the last one ended; the SHA-256 is computed on completion and stored with the version. Uploads that would leave less than 512 MB free on the data disk are refused.
- **Device uploads:** `POST /api/inbox?name=…` with `Authorization: Bearer hd_…` (a device token, scope `upload`) stores one file of at most 95 MB in the owner's *Inbox* folder. No cookie is involved, so the CSRF rules don't apply; cross-site browser requests are still refused by the Origin check. Only wrong tokens count against the client address's rate limit.
- **Downloads:** `Range` support; files are served `Content-Disposition: attachment` unless the type is on an inline allowlist (images except SVG, PDF, audio, video, plain text), always with `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`. Thumbnails, snippets and previews (v2) follow the same rules.
- **Logs:** request logs leave out query strings, and from v2 show public link tokens as `…`.
- **Timeouts:** header 10 s, idle 120 s, per-route read/write deadlines for uploads and downloads.
- **Outbound requests:** only to the SMTP server, from v2 to the fixed oEmbed providers for saved links (§4), and from v3 to Web Push services (§4). A push subscription's endpoint must be HTTPS on a known push service (`fcm.googleapis.com`, `*.push.apple.com`, `updates.push.services.mozilla.com`, `*.notify.windows.com`), so a subscription can't make the server call other addresses. Nothing else is fetched on a user's behalf.

## 7. PWA

- Manifest + service worker, `display: standalone`, scope `/`.
- The service worker caches only the **app shell** (hashed static assets, index for offline start). It never caches `/api/*` or file contents, so a lost phone holds no documents.
- **Android share target:** the manifest's `share_target` POSTs to `/share-target`. The service worker intercepts it, stores the files in IndexedDB, and redirects to `/save`: pick a folder (last used preselected), rename, upload with progress. files survive an expired session or a lost connection and are deleted from IndexedDB after upload. If the service worker isn't active, the server answers `/share-target` with a redirect to an explanation page. From v2, a shared link without files becomes a saved link, text shared with files becomes their description (§4), and a name that is already taken asks *Replace* or *Keep both*.
- **Push (v3):** the service worker receives Web Push messages and shows them as notifications (§4).
- **iPhone:** iOS has no Web Share Target support, so the PWA can't be in the share sheet. Homedrive provides an Apple Shortcut ("Save to Homedrive") that uploads to the user's inbox with a device token.
- **Desktop:** normal website, drag-and-drop upload. The app never shows an install prompt on desktop (browsers may still offer install themselves).
- **HTTPS only:** secure cookies and service workers require it, so Homedrive is always used through its public URL, also at home.

## 8. Backup

- Nightly at `backup_time` when `backup_path` is set:
  1. Consistent database snapshot (`VACUUM INTO`) → `backup_path/db/homedrive-<timestamp>.db`, keep 14 + monthly.
  2. Copy new objects to `backup_path/objects/` (objects are immutable, so this is incremental by nature).
  3. Objects no longer referenced by any version stay until `backup_retention_days` have passed.
- `backup_path` carries the same instance marker as `data_path`; a mismatch disables backups with a visible error instead of mixing installations.
- Status (last run, duration, size, errors) is shown to admins and logged. From v2 it is part of the Storage page (§4).
- From v2 files have old versions: they stay in the data folder for `backup_retention_days` after being replaced, and the backup copies them like any other object.
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

Stack: Go (standard library HTTP, `modernc.org/sqlite` — pure Go, so cross-compiling needs no C toolchain), React + TypeScript + Vite, self-hosted Inter font, Heroicons, pdf.js. v2 adds TipTap with `tiptap-markdown` (Markdown editor), excelize on the server for `.xlsx` with a small formula engine in TypeScript, and libvips, ffmpeg and poppler as sandboxed subprocesses for thumbnails.

UI languages: English and Dutch, chosen from the browser language, switchable per user.
