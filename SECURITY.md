# Security Policy

## Supported versions

This project is maintained on the `main` branch. Security fixes are applied
there.

## Reporting a vulnerability

Please do not open a public issue for a security vulnerability.

Use GitHub's **Security → Report a vulnerability** flow on this repository so
the report stays private. Include:

- A description of the issue and its impact
- Reproduction steps or a proof of concept
- The affected version or commit
- Any suggested mitigation

You should receive an acknowledgment within a few days. Please allow time for a
fix to be prepared before public disclosure.

## Deployment notes

- Keep `.env` and the admin API key private.
- Use HTTPS in front of the service; do not expose the origin directly.
- Treat all published content as public.
- Review uploaded HTML before publishing it. HTML runs in a sandbox without
  same-origin access, but scripts inside that sandbox are still executable.
- Keep Go, Mermaid, and other dependencies up to date.
