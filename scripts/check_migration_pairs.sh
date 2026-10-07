#!/usr/bin/env bash
set -euo pipefail

# A container built from an incomplete migration directory can start against a
# newer database and silently expose an older API (for example, without the
# LingDoc routes). Fail the image build before such an image can be published.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for migration_dir in "$root_dir/migrations/versioned" "$root_dir/migrations/sqlite"; do
    [[ -d "$migration_dir" ]] || {
        echo "migration directory is missing: $migration_dir" >&2
        exit 1
    }

    mapfile -t up_names < <(
        find "$migration_dir" -maxdepth 1 -type f -name '*.up.sql' -printf '%f\n' \
            | sed 's/\.up\.sql$//' | LC_ALL=C sort
    )
    mapfile -t down_names < <(
        find "$migration_dir" -maxdepth 1 -type f -name '*.down.sql' -printf '%f\n' \
            | sed 's/\.down\.sql$//' | LC_ALL=C sort
    )

    if (( ${#up_names[@]} == 0 )); then
        echo "no up migrations found in $migration_dir" >&2
        exit 1
    fi

    missing_down="$(comm -23 <(printf '%s\n' "${up_names[@]}") <(printf '%s\n' "${down_names[@]}"))"
    missing_up="$(comm -13 <(printf '%s\n' "${up_names[@]}") <(printf '%s\n' "${down_names[@]}"))"
    if [[ -n "$missing_down" || -n "$missing_up" ]]; then
        echo "migration up/down pairs are incomplete in $migration_dir" >&2
        [[ -z "$missing_down" ]] || printf 'missing down: %s\n' "$missing_down" >&2
        [[ -z "$missing_up" ]] || printf 'missing up: %s\n' "$missing_up" >&2
        exit 1
    fi

    printf 'validated migration pairs: %s (latest %s)\n' \
        "$migration_dir" "${up_names[${#up_names[@]}-1]}"
done
