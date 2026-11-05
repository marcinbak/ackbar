#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

CHECK_MODE=false
FORCE=false
BASE_REF="origin/main"

while [ $# -gt 0 ]; do
    case "$1" in
        --check)
            CHECK_MODE=true
            shift
            ;;
        --force)
            FORCE=true
            shift
            ;;
        --base)
            if [ -z "${2:-}" ]; then
                echo "ERROR: --base requires a git ref argument" >&2
                exit 1
            fi
            BASE_REF="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [--check] [--force] [--base <ref>]"
            exit 0
            ;;
        *)
            echo "ERROR: Unknown option: $1" >&2
            echo "Usage: $0 [--check] [--force] [--base <ref>]" >&2
            exit 1
            ;;
    esac
done

# Compare two versions: returns 0 if vA > vB, 1 otherwise.
version_gt() {
    local vA="${1#v}"
    local vB="${2#v}"

    if [ "$vA" = "$vB" ]; then
        return 1
    fi

    local dateA="${vA%%.*}"
    local revA="${vA#*.}"
    local dateB="${vB%%.*}"
    local revB="${vB#*.}"

    if [ "$((10#$dateA))" -gt "$((10#$dateB))" ]; then
        return 0
    elif [ "$((10#$dateA))" -lt "$((10#$dateB))" ]; then
        return 1
    fi

    if [ "$((10#$revA))" -gt "$((10#$revB))" ]; then
        return 0
    fi
    return 1
}

# Determine next version string based on target date and highest revision found
compute_next_version() {
    local base_v="$1"
    local curr_v="$2"

    local today="${ACKBAR_BUMP_DATE:-$(date +%Y%m%d)}"
    local base_date="${base_v%%.*}"
    local curr_date="${curr_v%%.*}"

    local target_date="$today"
    if [[ "$base_date" =~ ^[0-9]{8}$ ]] && [ "$((10#$base_date))" -gt "$((10#$target_date))" ]; then
        target_date="$base_date"
    fi
    if [[ "$curr_date" =~ ^[0-9]{8}$ ]] && [ "$((10#$curr_date))" -gt "$((10#$target_date))" ]; then
        target_date="$curr_date"
    fi

    local max_rev=0
    if [ "$base_date" = "$target_date" ]; then
        local base_rev="${base_v#*.}"
        if [[ "$base_rev" =~ ^[0-9]+$ ]]; then
            local rev=$((10#$base_rev))
            if [ "$rev" -gt "$max_rev" ]; then
                max_rev="$rev"
            fi
        fi
    fi
    if [ "$curr_date" = "$target_date" ]; then
        local curr_rev="${curr_v#*.}"
        if [[ "$curr_rev" =~ ^[0-9]+$ ]]; then
            local rev=$((10#$curr_rev))
            if [ "$rev" -gt "$max_rev" ]; then
                max_rev="$rev"
            fi
        fi
    fi

    while IFS= read -r tag; do
        [ -z "$tag" ] && continue
        local tag_rev="${tag#v${target_date}.}"
        if [[ "$tag_rev" =~ ^[0-9]+$ ]]; then
            local rev=$((10#$tag_rev))
            if [ "$rev" -gt "$max_rev" ]; then
                max_rev="$rev"
            fi
        fi
    done < <(git tag -l "v${target_date}.*" 2>/dev/null || true)

    local next_rev=$((max_rev + 1))
    printf "%s.%02d" "$target_date" "$next_rev"
}

if [ "$CHECK_MODE" = true ]; then
    if [ ! -f "internal/version/VERSION" ]; then
        echo "ERROR: internal/version/VERSION file not found." >&2
        exit 1
    fi

    CURRENT_VER="$(tr -d '[:space:]' < "internal/version/VERSION")"
    if [[ ! "$CURRENT_VER" =~ ^[0-9]{8}\.[0-9]{2,}$ ]]; then
        echo "ERROR: Invalid version format in internal/version/VERSION: '$CURRENT_VER' (expected YYYYMMDD.NN)" >&2
        exit 1
    fi

    BRANCH="${GITHUB_HEAD_REF:-$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")}"
    if [[ "$BRANCH" == homebrew-formula-* ]] || { [ "${GITHUB_EVENT_NAME:-}" != "pull_request" ] && { [ "$BRANCH" = "main" ] || [ "$BRANCH" = "origin/main" ]; }; }; then
        echo "Version bump check skipped for branch '$BRANCH'."
        exit 0
    fi

    BASE_VER="$(git show "${BASE_REF}:internal/version/VERSION" 2>/dev/null | tr -d '[:space:]' || true)"
    if [ -z "$BASE_VER" ]; then
        echo "ERROR: Could not read ${BASE_REF}:internal/version/VERSION" >&2
        exit 1
    fi

    if ! version_gt "$CURRENT_VER" "$BASE_VER"; then
        NEXT_VER="$(compute_next_version "$BASE_VER" "$CURRENT_VER")"
        echo "ERROR: internal/version/VERSION ($CURRENT_VER) must be bumped above $BASE_REF ($BASE_VER). Run './scripts/bump-version.sh' to bump to $NEXT_VER." >&2
        exit 1
    fi

    if git rev-parse -q --verify "refs/tags/v${CURRENT_VER}" >/dev/null 2>&1; then
        TAG_COMMIT="$(git rev-parse -q --verify "refs/tags/v${CURRENT_VER}^{commit}" 2>/dev/null || git rev-parse -q --verify "refs/tags/v${CURRENT_VER}" 2>/dev/null || true)"
        HEAD_COMMIT="$(git rev-parse -q --verify HEAD 2>/dev/null || true)"
        if [ -n "$TAG_COMMIT" ] && [ "$TAG_COMMIT" != "$HEAD_COMMIT" ]; then
            echo "ERROR: Tag v${CURRENT_VER} already exists. Run './scripts/bump-version.sh --force' to advance to the next free version." >&2
            exit 1
        fi
    fi

    echo "Version check passed: $CURRENT_VER (> $BASE_REF $BASE_VER)"
    exit 0
fi

# Default bump mode
if [ "${ACKBAR_BUMP_NO_FETCH:-0}" != "1" ]; then
    git fetch origin main --tags --quiet 2>/dev/null || true
fi

CURRENT_VER=""
if [ -f "internal/version/VERSION" ]; then
    CURRENT_VER="$(tr -d '[:space:]' < "internal/version/VERSION")"
fi

BASE_VER="$(git show "${BASE_REF}:internal/version/VERSION" 2>/dev/null | tr -d '[:space:]' || echo "00000000.00")"
if [ -z "$BASE_VER" ]; then
    BASE_VER="00000000.00"
fi

if [ "$FORCE" = false ] && [ -n "$CURRENT_VER" ] && [[ "$CURRENT_VER" =~ ^[0-9]{8}\.[0-9]{2,}$ ]] && version_gt "$CURRENT_VER" "$BASE_VER" && ! git rev-parse -q --verify "refs/tags/v${CURRENT_VER}" >/dev/null 2>&1; then
    echo "Version already bumped to $CURRENT_VER (> $BASE_VER)"
    exit 0
fi

NEXT_VER="$(compute_next_version "$BASE_VER" "$CURRENT_VER")"
mkdir -p internal/version
printf "%s\n" "$NEXT_VER" > internal/version/VERSION
echo "Bumped version: $CURRENT_VER -> $NEXT_VER"
exit 0
