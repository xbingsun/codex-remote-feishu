#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TARGET_SCRIPT="${ROOT_DIR}/scripts/install/repo-install-target.sh"

usage() {
  cat <<'EOF'
usage: scripts/install/repo-target-request.sh [--instance <id>] [--base-dir <dir>] <admin|tool> <path> [curl args...]

Resolve an explicit install target or the current repository's bound install
target, then issue a localhost HTTP request to that instance.

examples:
  scripts/install/repo-target-request.sh admin /v1/status
  scripts/install/repo-target-request.sh admin /api/admin/bootstrap-state
  scripts/install/repo-target-request.sh --instance beta admin /v1/status
  scripts/install/repo-target-request.sh tool /healthz
  scripts/install/repo-target-request.sh admin /v1/status | jq .

options:
  --instance <id>   override the repo install target instance
  --base-dir <dir>  override the install base dir resolved for that instance
EOF
}

INSTANCE=""
BASE_DIR=""
BASE_DIR_SET=0

while [[ $# -gt 0 ]]; do
  case "${1:-}" in
    --instance)
      [[ $# -ge 2 ]] || { echo "missing value for --instance" >&2; exit 1; }
      INSTANCE="$2"
      shift 2
      ;;
    --base-dir)
      [[ $# -ge 2 ]] || { echo "missing value for --base-dir" >&2; exit 1; }
      BASE_DIR="$2"
      BASE_DIR_SET=1
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      break
      ;;
  esac
done

if [[ $# -lt 2 ]]; then
  usage >&2
  exit 1
fi

target_kind="$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')"
path="${2:-}"
shift 2

case "${target_kind}" in
  admin|tool) ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    echo "unsupported target kind: ${target_kind}" >&2
    usage >&2
    exit 1
    ;;
esac

if [[ -z "${path}" ]]; then
  echo "missing request path" >&2
  usage >&2
  exit 1
fi
if [[ "${path}" != /* ]]; then
  path="/${path}"
fi

resolver_args=()
if [[ -n "${INSTANCE}" ]]; then
  resolver_args+=("--instance" "${INSTANCE}")
fi
if [[ "${BASE_DIR_SET}" == "1" ]]; then
  resolver_args+=("--base-dir" "${BASE_DIR}")
fi

if [[ ${#resolver_args[@]} -gt 0 ]]; then
  eval "$("${TARGET_SCRIPT}" --format shell "${resolver_args[@]}")"
else
  eval "$("${TARGET_SCRIPT}" --format shell)"
fi

case "${target_kind}" in
  admin)
    base_url="${CODEX_REMOTE_TARGET_ADMIN_URL:-}"
    ;;
  tool)
    base_url="${CODEX_REMOTE_TARGET_TOOL_URL:-}"
    ;;
esac

if [[ -z "${base_url}" ]]; then
  echo "resolved install target has no ${target_kind} URL; resolve with scripts/install/repo-install-target.sh first" >&2
  exit 1
fi

unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY all_proxy
exec curl --noproxy '*' -fsS "$@" "${base_url%/}${path}"
