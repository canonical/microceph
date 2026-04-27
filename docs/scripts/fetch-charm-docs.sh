#!/bin/bash
# fetch-charm-docs.sh
#
# Fetches charm documentation from the charm-microceph repository and
# converts it to RST format under docs/charm/. Run automatically as part
# of the docs build (make html, make run, etc.).
#
# The charm/ directory is not committed to this repository; it is
# populated on demand at build time from the canonical source in
# charm-microceph.
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

# Sparse clone — only the docs directory.
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

# Ensure pandoc is available for .md -> .rst conversion.
# If not installed, download a static binary to a temp location.
if ! command -v pandoc &>/dev/null; then
    echo "pandoc not found, downloading static binary..."
    PANDOC_VERSION="3.1.9"
    PANDOC_DIR="${TMPDIR}/pandoc-bin"
    mkdir -p "${PANDOC_DIR}"
    curl -fsSL "https://github.com/jgm/pandoc/releases/download/${PANDOC_VERSION}/pandoc-${PANDOC_VERSION}-linux-amd64.tar.gz" \
        | tar -xz -C "${PANDOC_DIR}" --strip-components=2 "pandoc-${PANDOC_VERSION}/bin/pandoc"
    export PATH="${PANDOC_DIR}:${PATH}"
    echo "pandoc $(pandoc --version | head -1) installed to ${PANDOC_DIR}."
fi

# Convert a markdown file to RST, writing to dest path.
convert_md_to_rst() {
    local src="$1"
    local dest="$2"
    pandoc --from=markdown --to=rst --wrap=none -o "${dest}" "${src}"
}

# Prepend a Sphinx :ref: label to an rST file if it is not already present.
# This ensures cross-references in the parent docs remain stable even though
# the file is regenerated on every build.
ensure_ref_label() {
    local rst_file="$1"
    local label="$2"
    if grep -qF ".. _${label}:" "${rst_file}"; then
        return
    fi
    local tmp
    tmp="$(mktemp)"
    { printf '.. _%s:\n\n' "${label}"; cat "${rst_file}"; } > "${tmp}"
    mv "${tmp}" "${rst_file}"
}

# Ensure an RST file has at least one heading so Sphinx can link to it
# from a toctree. Some upstream markdown files omit a top-level H1;
# when that happens, derive a title from the filename and prepend it.
ensure_rst_title() {
    local rst_file="$1"
    # A top-level RST heading uses '=' as the underline. If none exists,
    # the document has no title Sphinx can link to.
    if grep -qE '^={3,}$' "${rst_file}"; then
        return
    fi
    local base title underline tmp
    base="$(basename "${rst_file}" .rst)"
    # Hyphens to spaces, then sentence-case (capitalise first letter only).
    title="$(echo "${base}" | tr '-' ' ' | sed 's/./\u&/')"
    underline="$(printf '%*s' "${#title}" '' | tr ' ' '=')"
    tmp="$(mktemp)"
    { printf '%s\n%s\n\n' "${title}" "${underline}"; cat "${rst_file}"; } > "${tmp}"
    mv "${tmp}" "${rst_file}"
}

# Map source paths to destination paths and convert all docs files.
# Source layout (charm-microceph repo):  Destination layout (this repo):
#   how-to-guides/*.md               ->  charm/how-to/*.rst
#   tutorials/*.md                   ->  charm/tutorial/*.rst  (getting-started -> get-started)
#   explanation/*.md                 ->  charm/explanation/*.rst  (when available)
#   reference/*.md                   ->  charm/reference/*.rst    (when available)
synced=0
while IFS= read -r -d '' src; do
    rel_path="${src#${CHARM_SRC}/}"
    case "${rel_path}" in
        how-to-guides/*.md)
            filename="$(basename "${rel_path}" .md).rst"
            dest="${DOCS_DIR}/charm/how-to/${filename}"
            mkdir -p "${DOCS_DIR}/charm/how-to"
            convert_md_to_rst "${src}" "${dest}"
            ensure_rst_title "${dest}"
            echo "  Fetched: charm/how-to/${filename}"
            synced=$((synced + 1))
            ;;
        tutorials/*.md)
            filename="$(basename "${rel_path}" .md).rst"
            [ "${filename}" = "getting-started.rst" ] && filename="get-started.rst"
            dest="${DOCS_DIR}/charm/tutorial/${filename}"
            mkdir -p "${DOCS_DIR}/charm/tutorial"
            convert_md_to_rst "${src}" "${dest}"
            ensure_rst_title "${dest}"
            [ "${filename}" = "get-started.rst" ] && ensure_ref_label "${dest}" "charm-get-started"
            echo "  Fetched: charm/tutorial/${filename}"
            synced=$((synced + 1))
            ;;
        explanation/*.md)
            filename="$(basename "${rel_path}" .md).rst"
            dest="${DOCS_DIR}/charm/explanation/${filename}"
            mkdir -p "${DOCS_DIR}/charm/explanation"
            convert_md_to_rst "${src}" "${dest}"
            ensure_rst_title "${dest}"
            echo "  Fetched: charm/explanation/${filename}"
            synced=$((synced + 1))
            ;;
        reference/*.md)
            filename="$(basename "${rel_path}" .md).rst"
            dest="${DOCS_DIR}/charm/reference/${filename}"
            mkdir -p "${DOCS_DIR}/charm/reference"
            convert_md_to_rst "${src}" "${dest}"
            ensure_rst_title "${dest}"
            echo "  Fetched: charm/reference/${filename}"
            synced=$((synced + 1))
            ;;
    esac
done < <(find "${CHARM_SRC}" -type f -name "*.md" -print0)

echo "Charm documentation fetch complete: ${synced} file(s) fetched."
