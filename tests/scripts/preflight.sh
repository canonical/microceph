#!/usr/bin/env bash
#
# CI preflight probes: detect a gross Snap Store or Ubuntu archive outage
# before the test matrix starts, so the run records one clearly named infra
# failure instead of one failure per suite.
#
# Usage: preflight.sh <probe> [args...]
#
#   probe_endpoints [name=url ...]  Probe the Snap Store and the Ubuntu
#                                   archive, plus any extra endpoints given.
#   snap_canary                     Download and verify a small snap.
#   apt_canary [package ...]        Refresh indexes and download packages
#                                   (default: jq s3cmd) into a temp dir.
#
# The probes install nothing and only write inside temporary directories.
#
# PREFLIGHT_ORIGIN, when set, names where probe_endpoints ran (for example an
# LXD instance) in its failure message.

set -eu

# Report an infra failure as a GitHub error annotation and step summary.
# The "kind=preflight" prefix is what failure classification keys on.
preflight_fail() {
    local message="kind=preflight PREFLIGHT: $1"
    echo "::error title=Infra::${message}"
    echo "${message}" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
    exit 1
}

PREFLIGHT_FAILURES=""

# Temporary directory for the canaries. It is script-level, not function-local,
# because the EXIT trap runs after the probe function has returned.
PREFLIGHT_WORK_DIR=""

cleanup() {
    if [ -n "$PREFLIGHT_WORK_DIR" ]; then
        rm -rf "$PREFLIGHT_WORK_DIR"
    fi
}
trap cleanup EXIT

# Probe one URL; extra arguments are passed to curl. Failures accumulate in
# PREFLIGHT_FAILURES so one run reports every unreachable endpoint.
check_endpoint() {
    local name="$1" url="$2"
    shift 2
    # Keep curl's exponential backoff (1s, 2s) and honor Retry-After.
    if curl -sSf --max-time 10 --retry 2 --retry-connrefused \
        --retry-max-time 30 "$@" "$url" >/dev/null 2>&1; then
        echo "$name reachable: $url"
    else
        PREFLIGHT_FAILURES="${PREFLIGHT_FAILURES:+$PREFLIGHT_FAILURES, }$name ($url)"
    fi
}

probe_endpoints() {
    local extra
    # api.snapcraft.io answers 400 to info queries without the
    # device-series header, so the probe would never pass without it.
    check_endpoint snap-store "https://api.snapcraft.io/v2/snaps/info/lxd" -H "Snap-Device-Series: 16"
    check_endpoint ubuntu-archive "http://archive.ubuntu.com/ubuntu/dists/"
    for extra in "$@"; do
        check_endpoint "${extra%%=*}" "${extra#*=}"
    done

    if [ -n "$PREFLIGHT_FAILURES" ]; then
        preflight_fail "endpoint checks failed${PREFLIGHT_ORIGIN:+ from $PREFLIGHT_ORIGIN}: $PREFLIGHT_FAILURES"
    fi
}

snap_canary() {
    local status
    PREFLIGHT_WORK_DIR=$(mktemp -d)
    cd "$PREFLIGHT_WORK_DIR"

    # Download and verify a small snap and its assertions; install nothing.
    if timeout --kill-after=5s 60s snap download hello-world --channel=latest/stable; then
        echo "Snap Store payload and assertions verified"
    else
        status=$?
        preflight_fail "Snap Store payload/assertion download failed (exit $status)"
    fi
}

# Run one apt-get subcommand against the temporary configuration.
check_apt() {
    local status
    if timeout --kill-after=5s 90s apt-get -c "$APT_CANARY_CONF" "$@"; then
        return 0
    else
        status=$?
        preflight_fail "APT $1 failed (exit $status)"
    fi
}

apt_canary() {
    local work_dir sources
    PREFLIGHT_WORK_DIR=$(mktemp -d)
    work_dir="$PREFLIGHT_WORK_DIR"
    mkdir -p "$work_dir/lists/partial" "$work_dir/cache/archives/partial" \
        "$work_dir/log" "$work_dir/downloads"

    # Keep the runner's Ubuntu mirrors and keys, not unrelated third-party sources.
    sources=/etc/apt/sources.list
    if [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
        sources=/etc/apt/sources.list.d/ubuntu.sources
    fi

    # Fresh indexes and downloads prevent cache hits from hiding an outage.
    # Ignore installed versions when selecting packages from those indexes.
    # Disable host update hooks so this check only writes in its temporary directory.
    APT_CANARY_CONF="$work_dir/apt.conf"
    cat > "$APT_CANARY_CONF" <<EOF
#clear APT::Update::Pre-Invoke; // Disable host hooks that run before index updates.
#clear APT::Update::Post-Invoke; // Disable host hooks that run after index updates.
#clear APT::Update::Post-Invoke-Success; // Disable host hooks that run after successful updates.
Dir::Etc::sourcelist "$sources"; // Use the runner's Ubuntu repository definitions.
Dir::Etc::sourceparts "-"; // Do not load additional repositories from sources.list.d.
Dir::State::lists "$work_dir/lists"; // Fetch indexes into a fresh temporary directory.
Dir::State::status "/dev/null"; // Ignore installed versions when selecting download candidates.
Dir::Cache "$work_dir/cache"; // Keep APT cache files separate from the host cache.
Dir::Log "$work_dir/log"; // Keep any APT logs in the temporary directory.
Acquire::Languages "none"; // Skip package-description translation indexes.
Acquire::Retries "2"; // Retry failed file downloads up to twice.
Acquire::http::Timeout "10"; // Limit HTTP connection and data inactivity waits to 10 seconds.
Acquire::https::Timeout "10"; // Limit HTTPS connection and data inactivity waits to 10 seconds.
Acquire::IndexTargets::deb::DEP-11::DefaultEnabled "false"; // Skip AppStream application metadata.
Acquire::IndexTargets::deb::CNF::DefaultEnabled "false"; // Skip command-not-found lookup indexes.
EOF

    if [ "$#" -eq 0 ]; then
        set -- jq s3cmd
    fi

    cd "$work_dir/downloads"
    check_apt update --error-on=any
    check_apt download "$@"
}

run="${1:?usage: preflight.sh <probe_endpoints|snap_canary|apt_canary> [args...]}"
shift

$run "$@"
