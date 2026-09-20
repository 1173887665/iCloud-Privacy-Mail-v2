#!/usr/bin/env bash
set -Eeuo pipefail

# Update a checked-out deployment without overwriting config.json or data/.
# Run as root (or with sudo) on the server after reviewing the target commit.

APP_DIR="${IPM_APP_DIR:-/opt/iCloud-Privacy-Mail-v2}"
BRANCH="${IPM_BRANCH:-main}"
MODE="${IPM_MODE:-docker}"
COMPOSE_FILE="${IPM_COMPOSE_FILE:-deploy/docker-compose.yml}"
SERVICE="${IPM_SERVICE:-icloud-privacy-mail}"
HEALTH_URL="${IPM_HEALTH_URL:-http://127.0.0.1:8788/api/health}"
HEALTH_TIMEOUT="${IPM_HEALTH_TIMEOUT:-90}"

log() { printf '[ipm-update] %s\n' "$*"; }
die() { printf '[ipm-update] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: sudo deploy/update-server.sh [--mode docker|systemd] [--branch main]

Environment overrides: IPM_APP_DIR, IPM_BRANCH, IPM_MODE, IPM_COMPOSE_FILE,
IPM_SERVICE, IPM_HEALTH_URL, IPM_HEALTH_TIMEOUT.
EOF
}

while (($#)); do
  case "$1" in
    --mode)
      (($# >= 2)) || die "--mode requires docker or systemd"
      MODE="$2"
      shift 2
      ;;
    --branch)
      (($# >= 2)) || die "--branch requires a branch name"
      BRANCH="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ "$MODE" == "docker" || "$MODE" == "systemd" ]] || die "mode must be docker or systemd"
[[ $EUID -eq 0 ]] || die "run as root, for example: sudo $0 --mode docker"
[[ -d "$APP_DIR/.git" ]] || die "not a Git checkout: $APP_DIR"
command -v git >/dev/null || die "git is required"
command -v curl >/dev/null || die "curl is required"

cd "$APP_DIR"
git diff --quiet && git diff --cached --quiet || die "working tree is not clean; backup local changes before updating"

PREVIOUS_SHA="$(git rev-parse HEAD)"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_DIR="$APP_DIR/data/backups/deploy-$TIMESTAMP-$PREVIOUS_SHA"
mkdir -p "$BACKUP_DIR"
if [[ -f config.json ]]; then
  install -m 600 config.json "$BACKUP_DIR/config.json"
fi
for data_file in data/app.db data/app.db.key; do
  if [[ -f "$data_file" ]]; then
    install -m 600 "$data_file" "$BACKUP_DIR/$(basename "$data_file")"
  fi
done
log "backup saved to $BACKUP_DIR"

rollback_docker() {
  log "rolling back source to $PREVIOUS_SHA"
  git switch --detach "$PREVIOUS_SHA"
  docker compose -f "$COMPOSE_FILE" up -d --build
  git branch -f "$BRANCH" "$PREVIOUS_SHA"
  git switch "$BRANCH"
}

rollback_systemd() {
  [[ -f bin/ipm-server.previous ]] || die "rollback binary is missing"
  systemctl stop "$SERVICE"
  install -m 755 bin/ipm-server.previous bin/ipm-server
  systemctl start "$SERVICE"
  git switch --detach "$PREVIOUS_SHA"
  git branch -f "$BRANCH" "$PREVIOUS_SHA"
  git switch "$BRANCH"
}

health_check() {
  local deadline=$((SECONDS + HEALTH_TIMEOUT))
  while ((SECONDS < deadline)); do
    if curl --fail --silent --show-error --max-time 5 "$HEALTH_URL" | grep -q '"status":"ok"'; then
      return 0
    fi
    sleep 2
  done
  return 1
}

git fetch --prune origin "$BRANCH"
REMOTE_SHA="$(git rev-parse "origin/$BRANCH")"
[[ "$REMOTE_SHA" != "$PREVIOUS_SHA" ]] || { log "already up to date at $PREVIOUS_SHA"; exit 0; }
git merge --ff-only "origin/$BRANCH"

if [[ "$MODE" == "docker" ]]; then
  command -v docker >/dev/null || die "docker is required for --mode docker"
  export IPM_COMMIT="$REMOTE_SHA"
  docker compose -f "$COMPOSE_FILE" up -d --build --remove-orphans
  if ! health_check; then
    log "health check failed after update"
    rollback_docker
    health_check || die "rollback completed but health check still fails"
    die "update rolled back to $PREVIOUS_SHA"
  fi
else
  command -v go >/dev/null || die "go is required for --mode systemd"
  [[ -x "$(command -v systemctl)" ]] || die "systemctl is required for --mode systemd"
  mkdir -p bin
  if [[ -f bin/ipm-server ]]; then
    install -m 755 bin/ipm-server bin/ipm-server.previous
  fi
  CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X icloud-privacy-mail-v2/internal/buildinfo.Commit=$REMOTE_SHA -X icloud-privacy-mail-v2/internal/buildinfo.BuiltAt=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o bin/ipm-server.new .
  systemctl stop "$SERVICE"
  install -m 755 bin/ipm-server.new bin/ipm-server
  rm -f bin/ipm-server.new
  systemctl start "$SERVICE"
  if ! health_check; then
    log "health check failed after update"
    rollback_systemd
    health_check || die "rollback completed but health check still fails"
    die "update rolled back to $PREVIOUS_SHA"
  fi
fi

log "updated $PREVIOUS_SHA -> $REMOTE_SHA"
log "health check passed: $HEALTH_URL"
