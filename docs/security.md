# Homedrive security

Homedrive is reachable from the internet and holds private documents. This file is the threat model, the list of controls, and the plan for the security check that gates the v1 release.

## Threat model

| Attacker | Wants | Main defences |
|---|---|---|
| Internet scanner / bot | Any unauthenticated access, known CVEs, exposed admin pages | Host check, no default credentials, no setup wizard open to the world, minimal unauthenticated surface, dependency scanning |
| Credential stuffer | Log in with leaked passwords | Second factor by email, rate limits per IP and account, common-password blocklist, no account enumeration |
| Logged-in user (other household member) | Read or change someone else's files | Per-request authorization through drive membership, IDOR tests for every endpoint |
| Malicious file (uploaded by a user, or shared to them) | Script execution in the app's origin, parser exploits | Sandboxed, non-inline serving; sanitized Markdown; parsers in limited subprocesses |
| Someone with a share link | More than the shared item | Link scope = one node subtree, view-only, expiry, revocation |
| Stolen / lost phone | Documents | No offline document cache, session revoke from the device list |
| Network position between Cloudflare and Homedrive | Spoof client IPs, bypass rate limits | `trusted_proxies`, no LAN port by default |

Out of scope: a compromised Home Assistant host or NAS, a compromised Cloudflare account, a compromised email account of a user (it is their second factor).

## Controls

### Authentication and sessions
- No self-registration; first admin through a one-time setup link in the log (URL fragment, 1 h validity).
- Argon2id password hashing with a concurrency cap; min 12 characters; blocklist.
- Email code second factor: 8 digits, 10 min, single use, hashed, 5 attempts, send-rate limit.
- `__Host-` session cookie, Secure, HttpOnly, SameSite=Lax; token hashed at rest; sliding 30 d / absolute 90 d; revocable device list; re-authentication for sensitive actions.
- Session ID rotated on login and privilege change; all sessions revoked on password change.
- Audit log: logins, failures, code sends, admin actions, device token use, share link creation.

### Requests
- Host header must match `public_url`. Origin must match on state-changing requests. JSON-only API bodies.
- Client IP from `CF-Connecting-IP` / `X-Forwarded-For` only when the peer is in `trusted_proxies`.
- Body size limits per route, header size limit, timeouts against slow clients.
- Response headers on every response:
  - `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; font-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`
  - `Strict-Transport-Security: max-age=31536000` (HTTPS only), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`, `Permissions-Policy` denying camera, microphone, geolocation, payment, USB and others.
  - No inline scripts or styles in the built frontend; no third-party origins at runtime (fonts are self-hosted).

### Files
- Paths on disk are built from validated random IDs only, inside an `os.Root`. Display names are data, never paths.
- File contents are served with `nosniff` and `Content-Security-Policy: sandbox`; anything outside the inline allowlist is `attachment`. SVG and HTML are never inline.
- Markdown is rendered to HTML and sanitized (allowlist) before display.
- Parsers (thumbnails, previews) run as subprocesses with timeouts, memory and pixel limits; archives are listed, never extracted. The npm `xlsx` package is not used (outdated, known CVEs).
- Disk-full guard and per-upload size limits; uploads verified by SHA-256.

### Share links (v2)
- 128-bit random tokens stored hashed; view/download only; expiry; optional password; revocable; `X-Robots-Tag: noindex`; rate-limited.

### Container and supply chain
- Static Go binary on Alpine; no shell needed at runtime.
- Docker: non-root, read-only root filesystem, `cap_drop: ALL`, `no-new-privileges`.
- HA add-on: no host network, no privileged mode, no Docker/HA API access, Ingress off. Custom AppArmor profile once tested on a real installation.
- CI: `go vet`, tests, `govulncheck`, `npm audit`; GitHub Actions pinned to commit SHAs; Dependabot for Go, npm, Docker and Actions.
- Images built in GitHub Actions with provenance and SBOM.

### Cloudflare (operator configuration, documented in the add-on docs)
- WAF rate-limiting rule on `/api/auth/*`.
- Optional: country restriction, Bot Fight Mode.
- Optional: Cloudflare Access in front, with a bypass for `/s/*` so share links keep working.

## v1 release gate: security check

v1 is not released until all of this is done and findings are fixed or explicitly accepted in `docs/security-review-v1.md`.

1. **Automated**
   - `govulncheck`, `gosec`, `staticcheck`, `npm audit` clean.
   - Image scan of the release image (OS packages + Go binary).
   - Go fuzz tests for name validation, chunked upload offsets, `Range` parsing, config parsing.
   - OWASP ZAP baseline + API scan against a running instance.
2. **Manual, against OWASP ASVS 5.0 Level 2**
   - Authentication and session management (brute force, code reuse, code race conditions, fixation, logout, revoke).
   - Access control: a two-user test matrix over every endpoint (IDOR), shared drive roles, device token scope.
   - Stored XSS through uploads: HTML, SVG, PDF with JavaScript, polyglots, Markdown payloads, file names.
   - CSRF on every state-changing route; Host/Origin bypass attempts; CORS absence.
   - Resource abuse: disk fill, decompression bombs, slow clients, many small chunks, email bombing.
   - Header and cookie review on every response type (API, static, file content, errors).
3. **Deployment review**
   - HA add-on security rating, container runtime settings, secrets in options/env, logs free of secrets and codes.
   - Cloudflare configuration as documented.
   - Backup files: permissions, marker checks, restore test with `homedrive export`.

## Reporting vulnerabilities

See [`SECURITY.md`](../SECURITY.md).
