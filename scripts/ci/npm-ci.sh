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
#   3. optionally retry once through a mirror registry.
#
# It deliberately never falls back to `npm install`. Resolving fresh versions
# would stop testing the committed lockfile, so a flaky network must not turn
# into a silently weaker check.
#
# Environment:
#   LINGDOC_NPM_ATTEMPTS             whole-install attempts (default 4)
#   LINGDOC_NPM_FALLBACK_REGISTRY    mirror used for the last attempt only;
#                                    empty (the default) disables the mirror so
#                                    CI never silently trusts a third party

set -euo pipefail

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
FALLBACK_REGISTRY="${LINGDOC_NPM_FALLBACK_REGISTRY:-}"

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
install_once() {
  npm ci \
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
  echo "::group::npm ci attempt $attempt/$ATTEMPTS in $PWD (registry: $(npm config get registry))"

  status=0
  install_once "$@" || status=$?

  if [[ $status -eq 0 ]]; then
    echo "::endgroup::"
    echo "npm ci succeeded on attempt $attempt"
    exit 0
  fi

  echo "::endgroup::"
  echo "npm ci failed on attempt $attempt (exit $status)" >&2

  # The mirror, when configured, is a separate last attempt rather than a
  # silent per-request fallback, so the log always shows which registry the
  # successful install came from.
  if [[ -n "$FALLBACK_REGISTRY" && $attempt -eq $ATTEMPTS ]]; then
    echo "::group::npm ci via fallback registry $FALLBACK_REGISTRY"
    status=0
    npm ci \
      --registry="$FALLBACK_REGISTRY" \
      --no-audit \
      --no-fund \
      --fetch-retries=5 \
      --fetch-retry-factor=3 \
      --fetch-retry-mintimeout=20000 \
      --fetch-retry-maxtimeout=180000 \
      --fetch-timeout=600000 \
      "$@" || status=$?
    echo "::endgroup::"
    if [[ $status -eq 0 ]]; then
      echo "npm ci succeeded via fallback registry $FALLBACK_REGISTRY"
      exit 0
    fi
    echo "fallback registry also failed (exit $status)" >&2
  fi

  if [[ $attempt -ge $ATTEMPTS ]]; then
    echo "npm ci exhausted $ATTEMPTS attempts in $PWD" >&2
    exit "$status"
  fi

  reset_tree
  attempt=$((attempt + 1))
  sleep $((attempt * 10))
done
