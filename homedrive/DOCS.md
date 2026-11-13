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
| Log in with Home Assistant | Off by default. See [Logging in with Home Assistant](#logging-in-with-home-assistant). |

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
- Optionally put Cloudflare Access in front, with a *Bypass* policy for `/s/*` (share links, from v2) and `/api/inbox` and `/api/inbox/link` (the iPhone Shortcut).

## First start

1. Start the add-on and open its **Log** tab. It shows a one-time link: `https://drive.example.com/setup#…`, valid for one hour. A restart gives a new one.
2. Open the link, fill in your name, email address and a password. You get a code by email; entering it creates the administrator account. That also proves the mail settings work.
3. Invite the rest of the household: avatar menu → *Manage Users* → *Invite Someone*. They get an email with a link to choose their password. There is no sign-up page. With Home Assistant login on, you can also add them as [Home Assistant users](#home-assistant-users), without a Homedrive password.

Everyone has their own **My Files**, which only they see. Shared drives, such as **Family**, hold what the household shares: anyone can create one and choose who joins it.

## Logging in with Home Assistant

With *Log in with Home Assistant* turned on, the sign-in page has a *Log In with Home Assistant* button under the email form. People whose account is linked to a Home Assistant user can then log in with that user's username and password. The code by email still follows, because Homedrive is on the internet and this check skips Home Assistant's own two-factor authentication. Email address and Homedrive password keep working, for example while Home Assistant restarts.

- **Linking.** An administrator sets the Home Assistant username on *Manage Users* (*Home Assistant* next to the person). People can also link themselves in *Account Settings*, with their Home Assistant username and password. Nobody gets an account just by having a Home Assistant user.
- **What Homedrive keeps.** Only the username. The password is checked by the Supervisor each time (that is what the add-on's `auth_api` permission is for); Homedrive never stores it and never gets access to Home Assistant itself.
- Only Home Assistant's own users (username and password) work. Someone who hasn't accepted their invitation yet, or whose account is disabled, can't log in this way.
- Wrong passwords count against the same limits as the normal sign-in, and every attempt is in the audit log as `login.homeassistant`.

Home Assistant and its logo are trademarks of the Open Home Foundation. Homedrive is not made or endorsed by the Open Home Foundation; the logo is only there to show which username and password the page asks for.

### Home Assistant users

Instead of inviting someone, an administrator can add them as a Home Assistant user: *Manage Users* → *Add Home Assistant User*, with their name, email address, Home Assistant username and the shared drives they join. No email is sent and they don't need a Homedrive password: they log in on the *Log In with Home Assistant* page with their Home Assistant username and password, and the code still goes to their email address. So that address must be theirs: someone else who knows the Home Assistant password still can't get in without that mailbox.

- Type the username exactly as it is in Home Assistant (*Settings → People*, the person's *Username*, not their display name). Homedrive can't list Home Assistant's users: that needs far more access to the Supervisor than the add-on asks for.
- They are members. To make one an administrator, they first choose a Homedrive password.
- Actions that ask for your password again, such as creating an iPhone key or deleting for good, ask them for their Home Assistant password, checked for their own linked user.
- They can't be unlinked from Home Assistant while they have no Homedrive password: it is their only way in.
- **A Homedrive password later.** In *Account Settings → Password*, *Email Me a Link* sends them a link to choose one; an administrator can also send it with *Send Password Link*. After that they also log in with their email address and password, and Home Assistant login keeps working.
- **If you turn Home Assistant login off,** Home Assistant users can't log in. Send each of them a password link from *Manage Users* so they can choose a Homedrive password.
- In the audit log: `homeassistant_user_added`, and `reauth.homeassistant` for each confirmation with a Home Assistant password.

## Phones

- **Android:** open the public URL in Chrome and choose *Install app*. Homedrive then appears in the share menu of other apps: shared files go to a *Save to Homedrive* screen where you pick the folder. Text shared with files becomes their description, and a shared link becomes a saved link, shown as a card (with the picture, title and channel for YouTube and TikTok) that opens the video in its app.
- **iPhone and iPad:** open the public URL in Safari, *Share → Add to Home Screen*. iOS doesn't let web apps appear in the share menu, so use an Apple Shortcut instead: *Account Settings → iPhone and iPad → Set Up a Shortcut* creates a key for that device and shows how to build the Shortcut. Shared files and links land in the *Inbox* folder of your My Files. A key can only add to your Inbox, and you can remove it there at any time.

## Your files and backups

- The data folder belongs to Homedrive. Files are stored by ID, not by name, so don't change anything in it by hand.
- If the data folder disappears or is replaced by an empty folder (for example when a USB disk or NAS share isn't mounted), Homedrive **refuses to start** instead of starting empty. Mount the disk or share and restart the add-on.
- Moving to another folder: stop the add-on, copy the whole folder including the hidden `.homedrive` file, change the option, start again.
- Home Assistant backups contain Homedrive's settings and database, not the file contents. Those are covered by Homedrive's own nightly backup to the backup folder.
- Large data folders under `/share` or `/media` also end up in full Home Assistant backups unless you leave those folders out of the backup.

### The nightly backup

With a backup folder set, Homedrive copies itself there every night: a snapshot of the database (the last 14 days, plus one per month for a year) and the files, of which only new ones are copied. Deleted and changed files stay in the backup for the retention period. Administrators see the status, and can start a backup, on the *Storage* page (avatar menu → *Storage*).

The backup folder gets the same `.homedrive` marker as the data folder. A backup folder that belongs to another installation, or a disk that isn't mounted, stops the backup with an error on that page instead of mixing things up.

### Restoring without Homedrive

The backup is not a readable folder tree, because files are stored by ID. To get your files back as normal folders, also when Homedrive itself is gone:

```
homedrive export /media/usb/homedrive-backup /media/usb/restored
```

The target folder must be empty or not exist. It gets one folder per drive (`My Files - Anna`, `Family`, …) with the folders and files as they were at the newest snapshot; add a snapshot file name from `db/` as third argument for an older one. In the add-on, run it from the add-on's terminal, or with Docker: `docker run --rm -v …:/backup -v …:/restore ghcr.io/bertvanbaelen/homedrive export /backup /restore`.

## Updating

Home Assistant offers an update when a new version is released; the add-on's *Changelog* tab lists what changed.

- Keep the backup before updating turned on in the update dialog. The new version upgrades the database when it starts, and an older version can't use an upgraded database: to go back, restore that backup. It holds the database and the settings; the data folder isn't touched.
- An update that fails right after a release usually means the new version is still being built. Try again a few minutes later.
- Phones and browsers get the new version the next time Homedrive is opened.

## Locked out

- **Forgot your password?** Ask an administrator: on *Manage Users* they can send you a link to choose a new one. There is no "forgot password" link on the sign-in page on purpose: your email already receives the second factor, so a reset by email would make sign-in depend on email alone. The same link lets a Home Assistant user choose a first Homedrive password, for example when Home Assistant login is off.
- **The only administrator forgot their password:** turn on *Admin recovery* in the add-on options and restart. The log shows a one-time link, valid for one hour. On that page, enter the administrator's email address and a new password, and confirm with the code sent to that address. Turn *Admin recovery* off again afterwards.
