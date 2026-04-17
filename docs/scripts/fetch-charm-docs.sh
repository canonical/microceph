#!/bin/bash
# fetch-charm-docs.sh
#
# Syncs charm documentation changes from the charm-microceph repository
# into docs/charm/. On each run, only files that changed since the last
# sync are updated. The last-synced commit SHA is stored in
# docs/charm/.charm-docs-ref.
#
# Usage:
#   ./scripts/fetch-charm-docs.sh [branch] [charm-repo-url]
#
# Environment variables:
#   CHARM_REPO_URL  - Override the default charm repo URL
#   CHARM_BRANCH    - Override the default branch (main)
#   CHARM_DOCS_DIR  - Source directory within the charm repo (default: docs)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCS_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

CHARM_BRANCH="${1:-${CHARM_BRANCH:-main}}"
CHARM_REPO_URL="${2:-${CHARM_REPO_URL:-https://github.com/canonical/charm-microceph.git}}"
CHARM_DOCS_DIR="${CHARM_DOCS_DIR:-docs}"

# File that records the last charm-microceph commit SHA we synced from.
REF_FILE="${DOCS_DIR}/charm/.charm-docs-ref"

# Temporary directory for cloning
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

echo "Fetching charm documentation from ${CHARM_REPO_URL} (branch: ${CHARM_BRANCH})..."

# Clone with full history of the docs directory so we can diff against last ref.
git clone --branch "${CHARM_BRANCH}" --filter=blob:none --sparse \
    "${CHARM_REPO_URL}" "${TMPDIR}/charm-microceph"

cd "${TMPDIR}/charm-microceph"
git sparse-checkout set "${CHARM_DOCS_DIR}"

# Verify the source docs directory exists
if [ ! -d "${CHARM_DOCS_DIR}" ]; then
    echo "ERROR: ${CHARM_DOCS_DIR}/ directory not found in charm-microceph repository."
    exit 1
fi

CHARM_SRC="${TMPDIR}/charm-microceph/${CHARM_DOCS_DIR}"
CURRENT_SHA="$(git rev-parse HEAD)"

# Determine which files to sync:
# - If a previous ref exists, only sync files changed since that ref.
# - If no ref exists (first run), sync all docs files.
if [ -f "${REF_FILE}" ]; then
    LAST_SHA="$(cat "${REF_FILE}")"
    if [ "${LAST_SHA}" = "${CURRENT_SHA}" ]; then
        echo "Already up to date (${CURRENT_SHA}). Nothing to sync."
        exit 0
    fi
    echo "Syncing changes from ${LAST_SHA:0:7} to ${CURRENT_SHA:0:7}..."
    mapfile -t CHANGED_FILES < <(
        git diff --name-only "${LAST_SHA}" "${CURRENT_SHA}" -- "${CHARM_DOCS_DIR}/" \
        | sed "s|^${CHARM_DOCS_DIR}/||"
    )
else
    echo "No previous sync ref found. Performing initial sync of all docs..."
    mapfile -t CHANGED_FILES < <(
        find "${CHARM_DOCS_DIR}" -type f | sed "s|^${CHARM_DOCS_DIR}/||"
    )
fi

# Require pandoc for .md -> .rst conversion.
if ! command -v pandoc &>/dev/null; then
    echo "ERROR: pandoc is required but not installed."
    exit 1
fi

# Convert a markdown file to RST, writing to dest path.
convert_md_to_rst() {
    local src="$1"
    local dest="$2"
    pandoc --from=markdown --to=rst --wrap=none -o "${dest}" "${src}"
}

# Map source paths to destination paths and convert changed files.
# Source layout:            Destination layout:
#   how-to-guides/*.md  ->  charm/how-to/*.rst
#   tutorials/*.md      ->  charm/tutorial/*.rst  (getting-started -> get-started)
synced=0
for rel_path in "${CHANGED_FILES[@]}"; do
    src="${CHARM_SRC}/${rel_path}"
    [ -f "${src}" ] || continue  # skip deletions

    case "${rel_path}" in
        how-to-guides/*.md)
            filename="$(basename "${rel_path}" .md).rst"
            dest="${DOCS_DIR}/charm/how-to/${filename}"
            mkdir -p "${DOCS_DIR}/charm/how-to"
            convert_md_to_rst "${src}" "${dest}"
            echo "  Updated: charm/how-to/${filename}"
            synced=$((synced + 1))
            ;;
        tutorials/*.md)
            filename="$(basename "${rel_path}" .md).rst"
            [ "${filename}" = "getting-started.rst" ] && filename="get-started.rst"
            dest="${DOCS_DIR}/charm/tutorial/${filename}"
            mkdir -p "${DOCS_DIR}/charm/tutorial"
            convert_md_to_rst "${src}" "${dest}"
            echo "  Updated: charm/tutorial/${filename}"
            synced=$((synced + 1))
            ;;
    esac
done

# Save the current SHA for the next run.
echo "${CURRENT_SHA}" > "${REF_FILE}"

echo "Charm documentation sync complete: ${synced} file(s) updated (ref: ${CURRENT_SHA:0:7})."
