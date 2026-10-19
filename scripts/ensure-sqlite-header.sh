#!/usr/bin/env bash
# Provide the SQLite development headers that sqlite-vec's cgo build needs.
#
# github.com/asg017/sqlite-vec-go-bindings/cgo compiles sqlite-vec.c with
# `#include "sqlite3.h"`, so the C compiler must be able to find that header.
# Linux CI images ship libsqlite3-dev and the header is simply present; a
# bare Windows or macOS toolchain usually has no SQLite development package at
# all, and the build then fails with
#
#     sqlite-vec.h:7:10: fatal error: sqlite3.h: No such file or directory
#
# mattn/go-sqlite3 — already a dependency, and the driver gorm uses — ships
# the SQLite amalgamation, whose header carries the same declarations as the
# system sqlite3.h. This script stages that header into a scratch include
# directory and points CGO_CFLAGS at it, so the build no longer depends on a
# system package being installed.
#
# Usage:
#   eval "$(scripts/ensure-sqlite-header.sh)"   # exports CGO_CFLAGS
#   . scripts/ensure-sqlite-header.sh           # same, in the current shell
#
# Exports CGO_CFLAGS with the include directory prepended, preserving any
# flags the caller already set. Prints a short note on stderr when it has to
# stage the header; stays silent when the system already provides one.

set -euo pipefail

# A header the compiler already finds. CGO_CFLAGS is what cgo adds on top of
# the compiler's own search path, so probe with it in place — a bare
# preprocess would miss a header that the real build does find.
#
# CC is unset on Windows toolchains that only ship `gcc`, so fall back through
# the names cgo itself accepts rather than assuming a POSIX `cc`.
probe_compiler="${CC:-}"
if [ -z "$probe_compiler" ]; then
    for candidate in cc gcc clang; do
        if command -v "$candidate" >/dev/null 2>&1; then
            probe_compiler="$candidate"
            break
        fi
    done
fi
if [ -n "$probe_compiler" ] \
    && printf '#include "sqlite3.h"\nint main(void){return 0;}\n' \
        | "$probe_compiler" ${CGO_CFLAGS:-} -E - >/dev/null 2>&1; then
    exit 0
fi

# The SQLite amalgamation vendored by the project's own SQLite driver. This is
# the same source gorm builds through mattn/go-sqlite3, so the declarations
# match what the rest of the binary is compiled against.
amalgamation_dir="${GOMODCACHE:-$(${GO:-go} env GOMODCACHE)}/github.com/mattn/go-sqlite3@v1.14.24"
if [ ! -f "$amalgamation_dir/sqlite3-binding.h" ]; then
    echo "ensure-sqlite-header: cannot find the SQLite amalgamation header." >&2
    echo "  looked in: $amalgamation_dir" >&2
    echo "  install a SQLite development package (Debian/Ubuntu: libsqlite3-dev," >&2
    echo "  Fedora/RHEL: sqlite-devel, macOS: the Xcode command line tools)" >&2
    echo "  or run 'go mod download github.com/mattn/go-sqlite3' first." >&2
    exit 1
fi

include_dir="${TMPDIR:-/tmp}/lingdoc-sqlite-include"
mkdir -p "$include_dir"
cp -f "$amalgamation_dir/sqlite3-binding.h" "$include_dir/sqlite3.h"
[ -f "$amalgamation_dir/sqlite3ext.h" ] && cp -f "$amalgamation_dir/sqlite3ext.h" "$include_dir/sqlite3ext.h"

# cgo passes CGO_CFLAGS to the compiler, so -I here is what makes
# `#include "sqlite3.h"` resolve. Keep the caller's existing flags.
export CGO_CFLAGS="-I$include_dir${CGO_CFLAGS:+ $CGO_CFLAGS}"
echo "ensure-sqlite-header: staged the SQLite amalgamation header in $include_dir" >&2
