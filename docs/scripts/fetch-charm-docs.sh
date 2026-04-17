#!/bin/bash
# fetch-charm-docs.sh
#
# Fetches charm documentation from the charm-microceph repository
# and places it into the docs/charm/ directory for combined builds.
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

# Temporary directory for cloning
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

echo "Fetching charm documentation from ${CHARM_REPO_URL} (branch: ${CHARM_BRANCH})..."

# Sparse clone: only fetch the docs directory
git clone --depth 1 --branch "${CHARM_BRANCH}" --filter=blob:none --sparse \
    "${CHARM_REPO_URL}" "${TMPDIR}/charm-microceph"

cd "${TMPDIR}/charm-microceph"
git sparse-checkout set "${CHARM_DOCS_DIR}"

# Verify the source docs directory exists
if [ ! -d "${CHARM_DOCS_DIR}" ]; then
    echo "ERROR: ${CHARM_DOCS_DIR}/ directory not found in charm-microceph repository."
    exit 1
fi

# Clear existing charm docs (except any .gitkeep)
echo "Syncing charm documentation into ${DOCS_DIR}/charm/..."
mkdir -p "${DOCS_DIR}/charm"

# Use rsync if available, otherwise fall back to cp
if command -v rsync &>/dev/null; then
    rsync -a --delete --exclude='.gitkeep' \
        "${TMPDIR}/charm-microceph/${CHARM_DOCS_DIR}/charm/" \
        "${DOCS_DIR}/charm/"
else
    rm -rf "${DOCS_DIR}/charm/"
    mkdir -p "${DOCS_DIR}/charm"
    cp -a "${TMPDIR}/charm-microceph/${CHARM_DOCS_DIR}/charm/." \
        "${DOCS_DIR}/charm/"
fi

# Also import shared reuse assets if they exist in the charm repo
if [ -d "${TMPDIR}/charm-microceph/${CHARM_DOCS_DIR}/reuse" ]; then
    echo "Importing shared reuse assets from charm repo..."
    mkdir -p "${DOCS_DIR}/reuse/charm"
    if command -v rsync &>/dev/null; then
        rsync -a "${TMPDIR}/charm-microceph/${CHARM_DOCS_DIR}/reuse/" \
            "${DOCS_DIR}/reuse/charm/"
    else
        cp -a "${TMPDIR}/charm-microceph/${CHARM_DOCS_DIR}/reuse/." \
            "${DOCS_DIR}/reuse/charm/"
    fi
fi

echo "Charm documentation successfully imported."
