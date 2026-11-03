# Changelog

From v2 on, the first number is the roadmap version (v2, v3, …), the second goes up with each feature and the third with each set of fixes. Home Assistant offers an update as soon as a new version is released.

## 2.6.0

**Log in with Home Assistant** (add-on only, off by default)

- Turn on `homeassistant_login` in the add-on options. People whose Homedrive account is linked to a Home Assistant user can then log in with that username and password, followed by the usual email code.
- Admins link usernames on *Manage Users*; people can link their own in *Account Settings*. Email and Homedrive password keep working.
- The add-on now asks for Home Assistant's `auth_api` permission for this.

## 2.5.0

**Thumbnails**

- Files show their contents instead of an icon: photos, video posters with their length, audio covers, the first page of PDFs, the start of text and code files, spreadsheet tables, the contents of `.zip` files, and folders as their first four items.
- iPhone photos (HEIC) and TIFF images now open in the viewer.
- Saved links' video pictures are resized like other thumbnails.
- The image now includes libvips, ffmpeg and poppler (about 150 MB larger).

## 2.4.0

**Descriptions and saved links**

- Files and folders can have a description, written in the info panel by anyone who can edit them. Search finds words in descriptions too, and `homedrive export` puts each description next to its item as a `.description.txt` file.
- Text shared together with files from the Android share menu becomes their description instead of being dropped. The iPhone Shortcut can send a description too.
- Links shared to Homedrive are saved as `.url` files and shown as cards. YouTube and TikTok links get their picture, title and channel; other sites show their name. Tapping the card opens the video in its app or the site in the browser.
- Homedrive asks only YouTube's and TikTok's own preview services, never the linked site, and your browser never loads anything from them.
- The iPhone Shortcut instructions now also cover links. Existing Shortcuts keep working for files.

## 2.3.0

**Storage page**

- A new Storage page shows what My Files takes, split by type, and your largest files, which you can open or move to the trash right there.
- *Free up space*: empty the trash, or delete old versions of files on the drives you can edit. Both ask for your password.
- A meter at the bottom of the sidebar shows how much you use and opens the page. On a phone, Storage is in the account menu.
- Admins also see the data disk (used, free, and how much of it is Homedrive), the usage of every person and shared drive (sizes only, no file names) and the backup. The Backup page moved here; old links to it open Storage.

## 2.2.0

**Sharing and public links**

- Share files and folders with people in the household as *Can view* or *Can edit*. Only people who can edit an item can share it. Shared items appear under *Shared with me*, and in search, Recent and Starred.
- One view-only public link per file or folder, expiring never, after 7 days or after 1 day, with an optional password. Visitors browse shared folders, open files in the viewer and download them one by one or all together as a `.zip`.
- Download any folder you can open as a `.zip` from its info panel.
- Items that are shared or have a link show a people icon.
- Dropdowns are now the same on every device: a menu on computers, a sheet on phones.

## 2.1.0

**Shared drives**

- Anyone can create a shared drive and becomes its manager. Managers add people as viewer, editor or manager, rename the drive, and delete it once it is empty.
- The sidebar has a *Shared drives* group and *Shared with me*. On phones the tabs are Files, Shared, Recent, Starred and Trash.
- Inviting someone, you pick the drives they join. Admins manage the members of every drive from the Users page, without seeing its files.
- Updating from 0.1.0: Family becomes a normal shared drive. Admins become its managers and everyone else stays an editor. If the first admin uses Dutch, it is renamed Familie. New users no longer join it automatically, but the invite form ticks it.

## 0.1.0

The v1 pre-release.

- My Files for everyone and a shared Family drive: browse in a grid or a list, folders, upload (resumable, drag and drop on computers), download, rename, move, trash, search, starred and recent.
- Viewers for images, video, audio, PDF, and text and code.
- Phone app (PWA): share files into Homedrive from any app on Android, and through an Apple Shortcut on iPhone.
- Sign in with a password and a code by email. Admins invite people, manage users and see their devices; no public sign-up.
- Nightly backup to a second location, restorable without Homedrive with `homedrive export`.
- English and Dutch.
