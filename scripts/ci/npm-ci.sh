#!/usr/bin/env bash
# Shared dependency install step for every workflow that needs node_modules.
#
# Why this file exists: the GitHub-hosted runners repeatedly abort `npm ci`
# with `npm error code ECONNRESET` (errno -104) while streaming tarballs from
# the public registry. Those aborts are transport-level, not code-level, and
# npm's default retry budget (2 attempts, 60s ceiling) is too small to ride
# them out. Because every call site ran a bare `npm ci`, each workflow failed
# on its own, which made the flakiness look like a code regression.
#
# This script keeps one install policy in one place:
#   1. widen npm's per-request retries and timeouts;
#   2. retry the whole install, because a reset mid-download leaves a partial
#      node_modules that makes the next attempt fail for an unrelated reason;
#   3. retry once through a mirror registry.
#
# It deliberately never falls back to `npm install`. Resolving fresh versions
# would stop testing the committed lockfile, so a flaky network must not turn
# into a silently weaker check.
#
# Environment:
#   LINGDOC_NPM_ATTEMPTS             whole-install attempts (default 4)
#   LINGDOC_NPM_FALLBACK_REGISTRY    mirror used for the last attempt only.
#                                    Defaults to https://registry.npmmirror.com.
#                                    Set to "none" to disable, so a fork that
#                                    must never reach a third party can turn it
#                                    off without editing this script.
#
# Why trusting a mirror is acceptable here: every resolved dependency in this
# repository's lockfiles carries an `integrity` hash (492 of 492 entries in
# frontend/package-lock.json). npm verifies that hash against the downloaded
# tarball regardless of which registry served it, so a mirror can serve bytes
# but cannot change which package is installed. A mirror outage or a tampered
# response shows up as a checksum failure, not as a different dependency tree.

set -euo pipefail

DEFAULT_FALLBACK_REGISTRY="https://registry.npmmirror.com"

usage() {
  echo "usage: npm-ci.sh [install-dir] [extra npm ci args...]" >&2
}

case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
esac

ATTEMPTS="${LINGDOC_NPM_ATTEMPTS:-4}"
FALLBACK_REGISTRY="${LINGDOC_NPM_FALLBACK_REGISTRY:-$DEFAULT_FALLBACK_REGISTRY}"
if [[ "${FALLBACK_REGISTRY,,}" == "none" || "${FALLBACK_REGISTRY,,}" == "off" ]]; then
  FALLBACK_REGISTRY=""
fi

DIR="."
if [[ $# -gt 0 ]]; then
  DIR="$1"
  shift
fi

if ! [[ "$ATTEMPTS" =~ ^[0-9]+$ ]] || [[ "$ATTEMPTS" -lt 1 ]]; then
  echo "LINGDOC_NPM_ATTEMPTS must be a positive integer, got: $ATTEMPTS" >&2
  exit 2
fi

cd "$DIR"
if [[ ! -f package-lock.json ]]; then
  echo "no package-lock.json in $PWD; npm ci cannot run" >&2
  exit 2
fi
# A committed lockfile without a manifest makes npm fail with a bare ENOENT
# on package.json, which reads like a network or npm bug instead of a repo
# mistake. The repo root currently carries exactly that stray lockfile.
if [[ ! -f package.json ]]; then
  echo "package-lock.json without package.json in $PWD; npm ci cannot run" >&2
  exit 2
fi

# Transport retries handle a dropped socket. The outer loop handles the case
# where npm spent its budget and left the cache and node_modules mid-write.
#
# install_once [registry]
#   With no argument it uses whatever `npm config get registry` resolves to.
#   With a registry argument it overrides that for this attempt only, so the
#   mirror never persists into the rest of the job.
#
#   `--replace-registry-host=always` is mandatory on the mirror attempt, not
#   decoration. `npm ci` downloads straight from each lockfile entry's
#   `resolved` URL and ignores `--registry` by default: a lockfile entry
#   pointing at registry.npmjs.org keeps hitting registry.npmjs.org no matter
#   what registry is configured. Verified on this repository's own lockfile,
#   whose 492 resolved URLs already span three hosts:
#     mirrors.tencent.com      328
#     registry.npmjs.org       113
#     registry.npmmirror.com    50
#     cdn.sheetjs.com           1
#   Without that flag the mirror attempt silently re-requests the host that
#   already failed. With it, npm rewrites the host and the mirror is used.
install_once() {
  local registry=()
  if [[ $# -gt 0 && $1 != "--" ]]; then
    registry=(--registry="$1" --replace-registry-host=always)
    shift
  fi
  npm ci \
    "${registry[@]}" \
    --no-audit \
    --no-fund \
    --fetch-retries=5 \
    --fetch-retry-factor=3 \
    --fetch-retry-mintimeout=20000 \
    --fetch-retry-maxtimeout=180000 \
    --fetch-timeout=600000 \
    "$@"
}

reset_tree() {
  rm -rf node_modules
  # npm keeps a partially written _cacache entry on ECONNRESET; verify it
  # before spending another attempt on the same corrupt tarball.
  npm cache verify >/dev/null 2>&1 || true
}

attempt=1
while :; do
  # The first attempt always uses the configured registry. Every later attempt
  # switches to the mirror when one is available: the observed failure mode is
  # a persistent ECONNRESET from this runner pool to registry.npmjs.org, so
  # repeating the same endpoint three more times only burns the job's timeout.
  registry=""
  if [[ $attempt -gt 1 && -n "$FALLBACK_REGISTRY" ]]; then
    registry="$FALLBACK_REGISTRY"
  fi
  if [[ -n "$registry" ]]; then
    label="npm ci attempt $attempt/$ATTEMPTS in $PWD via $registry"
  else
    label="npm ci attempt $attempt/$ATTEMPTS in $PWD via $(npm config get registry)"
  fi
  echo "::group::$label"

  status=0
  if [[ -n "$registry" ]]; then
    install_once "$registry" "$@" || status=$?
  else
    install_once "$@" || status=$?
  fi

  if [[ $status -eq 0 ]]; then
    echo "::endgroup::"
    echo "npm ci succeeded on attempt $attempt"
    exit 0
  fi

  echo "::endgroup::"
  echo "npm ci failed on attempt $attempt (exit $status)" >&2

  if [[ $attempt -ge $ATTEMPTS ]]; then
    if [[ -n "$FALLBACK_REGISTRY" ]] && [[ -z "$registry" ]]; then
      echo "primary registry still unreachable and no attempt was left for $FALLBACK_REGISTRY" >&2
    fi
    echo "npm ci exhausted $ATTEMPTS attempts in $PWD" >&2
    exit "$status"
  fi

  reset_tree
  attempt=$((attempt + 1))
  sleep $((attempt * 10))
done
