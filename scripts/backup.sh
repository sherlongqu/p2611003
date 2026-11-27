#!/usr/bin/env bash
# Back up the PubDocs data volume. Run as root (or via a systemd timer).
# Usage: sudo BACKUP_DIR=/var/backups/pubdocs ./scripts/backup.sh
set -euo pipefail

DATA_DIR="${DATA_DIR:-/var/lib/docker/volumes/pubdocs_pubdocs-data/_data}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/pubdocs}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
STAMP="$(date +%Y%m%d-%H%M%S)"
ARCHIVE="$BACKUP_DIR/pubdocs-$STAMP.tgz"

install -d -m 700 "$BACKUP_DIR"
tar --numeric-owner -czf "$ARCHIVE" -C "$DATA_DIR" .
sha256sum "$ARCHIVE" > "$ARCHIVE.sha256"

# Optional off-site copy: set RCLONE_REMOTE, e.g. "b2:document-backups".
if [ -n "${RCLONE_REMOTE:-}" ] && command -v rclone >/dev/null 2>&1; then
  rclone copy "$ARCHIVE" "$RCLONE_REMOTE/pubdocs/"
  rclone copy "$ARCHIVE.sha256" "$RCLONE_REMOTE/pubdocs/"
fi

# Keep a rolling local window. Older archives may still exist off-site.
find "$BACKUP_DIR" -maxdepth 1 -type f \
  \( -name 'pubdocs-*.tgz' -o -name 'pubdocs-*.tgz.sha256' \) \
  -mtime "+$RETENTION_DAYS" -delete

echo "backup written: $ARCHIVE"
