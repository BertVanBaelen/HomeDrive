# Homedrive

A self-hosted online drive for your household's documents, reachable from anywhere through a Cloudflare Tunnel, with a phone app (PWA).

> **Early development.** This add-on doesn't do anything useful yet. Follow the project on GitHub for the first usable release.

## Before you start

You need:

- A domain on Cloudflare and the **Cloudflared** add-on, already working.
- An email account that can send mail over SMTP. Homedrive emails a login code every time someone signs in on a new device.
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
| SMTP settings | Your mail provider's SMTP server. Only encrypted connections (STARTTLS or TLS) are supported. |

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
- Optionally put Cloudflare Access in front, with a *Bypass* policy for `/s/*` so share links keep working.

## Your files and backups

- The data folder belongs to Homedrive. Files are stored by ID, not by name, so don't change anything in it by hand.
- If the data folder disappears or is replaced by an empty folder (for example when a USB disk or NAS share isn't mounted), Homedrive **refuses to start** instead of starting empty. Mount the disk or share and restart the add-on.
- Moving to another folder: stop the add-on, copy the whole folder including the hidden `.homedrive` file, change the option, start again.
- Home Assistant backups contain Homedrive's settings and database, not the file contents. Those are covered by Homedrive's own nightly backup to the backup folder.
- Large data folders under `/share` or `/media` also end up in full Home Assistant backups unless you leave those folders out of the backup.

## Phones

- **Android:** open the public URL in Chrome and choose *Install app*. Homedrive then appears in the share menu of other apps.
- **iPhone:** open the public URL in Safari, *Share → Add to Home Screen*. iOS doesn't let web apps appear in the share menu; an Apple Shortcut for that is planned.
