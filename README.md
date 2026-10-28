# Homedrive

A self-hosted online drive for your household's documents. Runs as a Home Assistant OS add-on or as a standalone Docker container, is built to be exposed on the internet through a Cloudflare Tunnel, and installs as a PWA on phones so you can share files straight into it.

> **Status: pre-release.** The v1 features are built; the [v1 release security check](docs/security-review-v1.md) is still open. See the [roadmap](docs/roadmap.md).

## Features (v1 target)

- Personal "My Files" for each user plus a shared family drive
- Grid and list browsing, folders, upload, download, rename, move, trash, search, starred, recent
- Viewers for images, video, audio, PDF and text
- Phone app (PWA): on Android, share files from any app into Homedrive; on iPhone via an Apple Shortcut
- Login with password + a code by email, no public sign-up
- Nightly backup to a second location, restorable without the app
- English and Dutch

## Install

### Home Assistant OS

1. *Settings → Add-ons → Add-on store → ⋮ → Repositories*, add `https://github.com/BertVanBaelen/HomeDrive`.
2. Install **Homedrive**, fill in the options, start it.
3. Point your Cloudflared add-on at it. See the [add-on documentation](homedrive/DOCS.md).

### Docker

See [`docker-compose.example.yml`](docker-compose.example.yml).

## Documentation

- [Architecture](docs/architecture.md): deployment, configuration, storage model, auth, PWA, backup
- [Security](docs/security.md): threat model, controls, release security check
- [Roadmap](docs/roadmap.md)
- [Design handoff](homedrive-mockup/README.md): visual design, interactions, mockup

## Development

Requirements: Go 1.27+, Node 24+.

```bash
cd web && npm ci && npm run build
cd server && go test ./... && go run ./cmd/homedrive
```

For local runs, set at least `HOMEDRIVE_PUBLIC_URL=http://localhost:8080`, `HOMEDRIVE_STATE_DIR`, `HOMEDRIVE_DATA_PATH` and `HOMEDRIVE_WEB_DIR=../web/dist`. With a `localhost` public URL, SMTP is optional. Then the setup link, sign-in codes and invitation emails are written to the log instead of being sent.

Other commands:

```bash
go run ./cmd/homedrive export <backup folder> <empty target folder>
```

rebuilds a normal folder tree from a backup, without a running server. Fuzz targets (`go test -fuzz`) cover name validation, sorting, chunked upload offsets, export names and config parsing.

## Security

Please report vulnerabilities privately, see [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
