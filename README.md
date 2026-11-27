# p2611003

[![tests](https://github.com/sherlongqu/p2611003/actions/workflows/test.yml/badge.svg)](https://github.com/sherlongqu/p2611003/actions/workflows/test.yml)

A small, self-hosted public document shelf written in Go. Publish PDF, HTML,
Markdown, plain text and images, then make them readable by anyone.

The interface, API errors and documentation are in US English. Content can be
in any language.

## Features

- **Five public content types**: PDF, HTML, Markdown, plain text and images.
- **Private studio and public API**: upload and manage documents with one
  admin key. Public readers never need an account.
- **Self-contained HTML**: on upload, external stylesheets and scripts are
  fetched from public HTTPS hosts and inlined. The upload response reports what
  was inlined, what failed, and what remains external. HTML is served in a
  sandboxed iframe without same-origin access.
- **GitHub-style Markdown**: GFM tables, task lists, strikethrough, autolinks,
  footnotes, sanitized raw HTML, and Mermaid diagrams. Mermaid loads lazily and
  follows the active theme.
- **Markdown image localization**: external and inline base64 images are
  downloaded, validated, and rewritten to local `/media/...` URLs. Private,
  loopback and SVG sources are rejected.
- **Multilingual anchors**: heading IDs and the generated table of contents
  support Unicode scripts, not just ASCII.
- **Fast search**: search titles, descriptions, tags and body text. Use
  `#tag` or `tag:tag` for a tag-only search. Press `F` on the home page to focus
  search.
- **Sharing metadata**: RSS, sitemap, JSON-LD, Open Graph and per-document
  generated card images.
- **Light and dark themes**, with the choice persisted in the browser.
- **Storage discipline**: per-file limits, a total quota, version history, and
  a quota that includes localized media.
- **Hardened delivery**: CSP with nonces, sandboxed HTML, security headers,
  HTTPS redirects, SSRF-filtered outbound fetches, and a minimal static Docker
  image.

## Requirements

- Go **1.22** or newer
- Docker and Docker Compose (optional, for deployment)
- Linux is recommended for production

## Quick start

```bash
git clone git@github.com:sherlongqu/p2611003.git
cd p2611003

cp .env.example .env
# Edit .env and set API_KEY and SESSION_SECRET.
# Generate values with: openssl rand -hex 32

go run .
```

Open <http://localhost:8080>. The public home page is at `/`; the private
studio is at `/studio` and is intentionally not linked from the public UI.

### Docker

```bash
cp .env.example .env
# Edit API_KEY and SESSION_SECRET first.

# Build the static binary on the host. The image uses a scratch base.
CGO_ENABLED=0 go build -buildmode=exe -tags timetzdata \
  -trimpath -ldflags="-s -w" -o p2611003 .

docker compose up -d --build
```

The container listens on `127.0.0.1:8080` by default. Put a reverse proxy or
tunnel in front of it for public HTTPS. The included Compose file binds to
loopback on purpose.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | HTTP listen port. |
| `DATA_DIR` | `./data` | Document, version and media storage. |
| `API_KEY` | generated at boot | Admin key for the studio and write API. Set it explicitly in production. |
| `SESSION_SECRET` | derived from `API_KEY` | HMAC key for studio sessions. Set a separate random value in production. |
| `SITE_NAME` | `Report for public` | Site name shown in metadata and RSS. |
| `SITE_TAGLINE` | `Public documents` | Site description used in metadata. |
| `PUBLIC_BASE_URL` | request host | Canonical public origin, for example `https://docs.example.com`. Set this behind a proxy. |
| `MAX_UPLOAD_MB` | `50` | Maximum size of one uploaded document. |
| `MAX_TOTAL_MB` | `2048` | Total quota for documents, versions and media. `0` disables the quota. |

The process exits cleanly on SIGINT/SIGTERM and supports a `healthcheck`
subcommand for container health checks.

## API

Reading endpoints are public. Write endpoints accept the admin key in either
header:

```text
X-API-Key: $API_KEY
Authorization: Bearer $API_KEY
```

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/documents` | List, filter, search and paginate documents. |
| `GET` | `/api/documents/{id}` | Document metadata. |
| `GET` | `/api/documents/{id}/versions` | Version history. |
| `POST` | `/api/upload` | Upload multipart or raw bytes. |
| `PUT` | `/api/documents/{id}` | Replace a document and archive the old version. |
| `DELETE` | `/api/documents/{id}` | Delete a document and its media. |
| `GET` | `/api` | Human-readable API guide. |

Multipart upload:

```bash
curl --fail-with-body -sS \
  -X POST "$BASE_URL/api/upload" \
  -H "X-API-Key: $API_KEY" \
  -F "file=@report.pdf" \
  -F "title=Quarterly report" \
  -F "tags=report,public"
```

Raw upload:

```bash
curl --fail-with-body -sS \
  -X POST "$BASE_URL/api/upload?filename=report.pdf" \
  -H "X-API-Key: $API_KEY" \
  --data-binary @report.pdf
```

Optional fields are `title`, `description`, and `tags` (comma separated).
The API returns HTTP `201` on create, `200` on replace, `401` without a valid
key, `413` when a file is too large, `415` for an unsupported extension, and
`507` when the storage quota is exhausted.

## Security model

- Public documents are public. Do not upload confidential material.
- The studio and write API are protected by a single admin key. Treat that key
  as a password and keep `.env` out of version control.
- Sessions are HMAC-signed, `HttpOnly`, `SameSite=Lax`, and `Secure` on HTTPS.
- Uploaded HTML runs in an iframe with `sandbox="allow-scripts"` and no
  same-origin access. It cannot read the site session or parent DOM.
- Markdown raw HTML is sanitized with an allowlist.
- Server-side fetches for HTML inlining and image localization are restricted
  to public hosts, allowed ports, and byte/time budgets. Redirects are
  revalidated and DNS dialing is checked against public IP ranges.
- The Docker image is `scratch`-based, has no shell, and runs with dropped
  capabilities and `no-new-privileges`.
- Put the app behind a TLS-terminating reverse proxy or tunnel. The app trusts
  loopback proxy headers for scheme detection, so do not expose the origin
  directly.

For reporting security issues, see [SECURITY.md](SECURITY.md).

## Development

```bash
./scripts/test.sh
```

This runs formatting checks, `go vet`, and the full test suite. Tests cover
uploads, storage, Markdown rendering and sanitization, media localization,
image validation, auth, API behavior, inline HTML, and static feature guards.

Project layout:

```text
.
├── main.go        HTTP server, config, embedded assets
├── handlers.go    public pages, metadata, image routes
├── auth.go        API key and studio session auth
├── store.go       document/version/media storage and search
├── render.go      Goldmark, sanitization, TOC and anchors
├── media.go       Markdown image localization and media serving
├── inline.go      HTML external-resource inlining
├── api.go         JSON API and API guide
├── studio.go      private upload/admin UI
├── seo.go         RSS, sitemap, robots and JSON-LD
├── og.go          Open Graph and card image generation
├── templates/     server-rendered HTML
├── static/        CSS, JS, fonts and images
├── assets/fonts/  Noto Sans SC fonts used for generated images
└── scripts/       test, backup and monitoring helpers
```

## Contributors

- **sherlongqu** — project owner, product direction, deployment and operations.
- **DeepSeek Harness** — AI coding assistant: implementation, debugging,
  refactoring, tests, security review and documentation.

AI assistance is credited for transparency. See [CONTRIBUTORS.md](CONTRIBUTORS.md).

## License

MIT. See [LICENSE](LICENSE).

Third-party components and their licenses are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
