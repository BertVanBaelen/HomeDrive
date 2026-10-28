# Homedrive

A self-hosted online drive for your household's documents, reachable from anywhere through a Cloudflare Tunnel, with a phone app (PWA).

> **Pre-release.** The v1 features are in place; the release security check (see `docs/security.md` in the repository) is still open. Use it with test data until v1 is released.

## Before you start

You need:

- A domain on Cloudflare and the **Cloudflared** add-on, already working.
- An email account that can send mail over SMTP. Homedrive emails a code every time someone signs in, and sends invitations.
- A place for the files: a new or empty folder under `/share` or `/media`, used only by Homedrive. That can be:
  - **Home Assistant's own disk** (simplest): keep the default, `/share/homedrive`.
  - **A USB disk**: Home Assistant OS mounts it under `/media`. Use a folder on it, for example `/media/usb/homedrive`.
  - **A NAS**: add the share first under *Settings → System → Storage → Add network storage* (usage *Share* or *Media*). It then appears as `/share/<name>` or `/media/<name>`.

## Configuration

| Option | What to fill in |
|---|---|
| Public URL | The address you'll use, for example `https://drive.example.com`. |
| Data folder | `/share/homedrive` (Home Assistant's own disk), `/media/usb/homedrive` (USB disk) or `/share/nas/homedrive` (NAS). The folder must be empty or not exist yet; its parent must exist. |
| Backup folder | Optional, for example `/media/usb/homedrive-backup`. Use a different disk than the data folder: a backup on the same disk doesn't survive that disk failing. |
| Backup time, retention | When the nightly backup runs (`03:00`), and how many days deleted or changed files stay in the backup (`30`). |
| SMTP settings | Your mail provider's SMTP server. Only encrypted connections (STARTTLS or TLS) are supported. |
| Admin recovery | Leave off. See [Locked out](#locked-out). |

### Connecting the Cloudflared add-on

Homedrive doesn't need a port on your network: Cloudflared connects to it over Home Assistant's internal network.

1. Open Homedrive's add-on page and note its **hostname** (something like `abcd1234-homedrive`).
2. In the Cloudflared add-on configuration, add Homedrive as an additional host:

   ```yaml
   additional_hosts:
     - hostname: drive.example.com
       service: http://abcd1234-homedrive:8080
   ```

3. Restart Cloudflared and open `https://drive.example.com`.

Always use the public URL, also at home. Homedrive refuses requests addressed to its IP address, and sign-in and the phone app need HTTPS.

### Recommended Cloudflare settings

- A WAF rate-limiting rule on `/api/auth/` (for example 20 requests per minute per IP).
- Optionally restrict access to the countries you live in.
- Optionally put Cloudflare Access in front, with a *Bypass* policy for `/s/*` (share links, from v2) and `/api/inbox` (the iPhone Shortcut).

## First start

1. Start the add-on and open its **Log** tab. It shows a one-time link: `https://drive.example.com/setup#…`, valid for one hour. A restart gives a new one.
2. Open the link, fill in your name, email address and a password. You get a code by email; entering it creates the administrator account. That also proves the mail settings work.
3. Invite the rest of the household: avatar menu → *Manage Users* → *Invite Someone*. They get an email with a link to choose their password. There is no sign-up page.

Everyone has their own **My Files**, which only they see, and everyone shares **Family**.

## Phones

- **Android:** open the public URL in Chrome and choose *Install app*. Homedrive then appears in the share menu of other apps: shared files go to a *Save to Homedrive* screen where you pick the folder.
- **iPhone and iPad:** open the public URL in Safari, *Share → Add to Home Screen*. iOS doesn't let web apps appear in the share menu, so use an Apple Shortcut instead: *Account Settings → iPhone and iPad → Set Up a Shortcut* creates a key for that device and shows how to build the Shortcut. Shared files land in the *Inbox* folder of your My Files. A key can only add files to your Inbox, and you can remove it there at any time.

## Your files and backups

- The data folder belongs to Homedrive. Files are stored by ID, not by name, so don't change anything in it by hand.
- If the data folder disappears or is replaced by an empty folder (for example when a USB disk or NAS share isn't mounted), Homedrive **refuses to start** instead of starting empty. Mount the disk or share and restart the add-on.
- Moving to another folder: stop the add-on, copy the whole folder including the hidden `.homedrive` file, change the option, start again.
- Home Assistant backups contain Homedrive's settings and database, not the file contents. Those are covered by Homedrive's own nightly backup to the backup folder.
- Large data folders under `/share` or `/media` also end up in full Home Assistant backups unless you leave those folders out of the backup.

### The nightly backup

With a backup folder set, Homedrive copies itself there every night: a snapshot of the database (the last 14 days, plus one per month for a year) and the files, of which only new ones are copied. Deleted and changed files stay in the backup for the retention period. Administrators see the status, and can start a backup, under avatar menu → *Backup*.

The backup folder gets the same `.homedrive` marker as the data folder. A backup folder that belongs to another installation, or a disk that isn't mounted, stops the backup with an error on that page instead of mixing things up.

### Restoring without Homedrive

The backup is not a readable folder tree, because files are stored by ID. To get your files back as normal folders, also when Homedrive itself is gone:

```
homedrive export /media/usb/homedrive-backup /media/usb/restored
```

The target folder must be empty or not exist. It gets one folder per drive (`My Files - Anna`, `Family`, …) with the folders and files as they were at the newest snapshot; add a snapshot file name from `db/` as third argument for an older one. In the add-on, run it from the add-on's terminal, or with Docker: `docker run --rm -v …:/backup -v …:/restore ghcr.io/bertvanbaelen/homedrive export /backup /restore`.

## Locked out

- **Forgot your password?** Ask an administrator: on *Manage Users* they can send you a link to choose a new one. There is no "forgot password" link on the sign-in page on purpose: your email already receives the second factor, so a reset by email would make sign-in depend on email alone.
- **The only administrator forgot their password:** turn on *Admin recovery* in the add-on options and restart. The log shows a one-time link, valid for one hour. On that page, enter the administrator's email address and a new password, and confirm with the code sent to that address. Turn *Admin recovery* off again afterwards.
