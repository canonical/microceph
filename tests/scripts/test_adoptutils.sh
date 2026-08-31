#!/usr/bin/env bash
# Unit tests for the host-side cephadm adoption helpers.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
adoptutils="$repo_root/tests/scripts/adoptutils.sh"
readonly adoptutils

test_dir=""

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "$test_dir" ]]; then
    rm -rf "$test_dir"
  fi
}

setup_test_environment() {
  local osd_sequence=$1
  local health=${2:-HEALTH_OK}
  local fail_match=${3:-}

  test_dir=$(mktemp -d)

  mkdir -p "$test_dir/bin"
  export ADOPTUTILS_TEST_LXC_LOG="$test_dir/lxc.log"
  export ADOPTUTILS_TEST_OSD_INDEX="$test_dir/osd-index"
  export ADOPTUTILS_TEST_OSD_SEQUENCE="$osd_sequence"
  export ADOPTUTILS_TEST_SLEEP_LOG="$test_dir/sleep.log"
  export ADOPTUTILS_TEST_HEALTH="$health"
  export ADOPTUTILS_TEST_FAIL_MATCH="$fail_match"
  export ADOPTUTILS_TEST_CEPHX_KEY='test-admin-key-do-not-log'
  printf '0\n' > "$ADOPTUTILS_TEST_OSD_INDEX"

  cat > "$test_dir/bin/lxc" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >> "$ADOPTUTILS_TEST_LXC_LOG"

if [[ -n "${ADOPTUTILS_TEST_FAIL_MATCH:-}" && "$*" == *"${ADOPTUTILS_TEST_FAIL_MATCH}"* ]]; then
  printf 'simulated lxc failure: %s\n' "$ADOPTUTILS_TEST_FAIL_MATCH" >&2
  exit 1
fi

case "$*" in
  *"ceph.client.admin.keyring"*)
    printf 'key = %s\n' "$ADOPTUTILS_TEST_CEPHX_KEY"
    ;;
  *"ip -4 -j route"*)
    printf '%s\n' 'routes'
    ;;
  *"ceph health"*)
    printf '%s\n' "$ADOPTUTILS_TEST_HEALTH"
    ;;
  *"ceph osd stat --format json"*)
    IFS=, read -r -a osd_counts <<< "$ADOPTUTILS_TEST_OSD_SEQUENCE"
    index=$(< "$ADOPTUTILS_TEST_OSD_INDEX")
    last_index=$((${#osd_counts[@]} - 1))
    if [[ "$index" -gt "$last_index" ]]; then
      index=$last_index
    fi
    printf '%s\n' "$((index + 1))" > "$ADOPTUTILS_TEST_OSD_INDEX"
    printf '{"num_up_osds": %s}\n' "${osd_counts[$index]}"
    ;;
esac
EOF

  cat > "$test_dir/bin/jq" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "$*" == *'.[] | select(.dst | contains("default")) | .prefsrc'* ]]; then
  printf '%s\n' '10.0.0.1'
  exit 0
fi

if [[ "$*" == *'.num_up_osds // 0'* ]]; then
  input=$(< /dev/stdin)
  if [[ "$input" =~ \"num_up_osds\"[[:space:]]*:[[:space:]]*([0-9]+) ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
  else
    printf '%s\n' '0'
  fi
  exit 0
fi

cat
EOF

  cat > "$test_dir/bin/sleep" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >> "$ADOPTUTILS_TEST_SLEEP_LOG"
EOF

  chmod +x "$test_dir/bin/lxc" "$test_dir/bin/jq" "$test_dir/bin/sleep"
}

run_bootstrap() {
  local output

  if ! output=$(PATH="$test_dir/bin:$PATH" bash "$adoptutils" bootstrap_cephadm test 2>&1); then
    printf '%s\n' "$output" >&2
    fail 'bootstrap_cephadm returned a failure'
  fi
}

run_bootstrap_expect_failure() {
  local output

  if output=$(PATH="$test_dir/bin:$PATH" bash "$adoptutils" bootstrap_cephadm test 2>&1); then
    fail 'bootstrap_cephadm unexpectedly succeeded'
  fi
  printf '%s\n' "$output"
}

assert_osd_diagnostics() {
  local output=$1
  local reason=$2

  if [[ "$output" != *"$reason"* ]]; then
    fail "bootstrap failure did not report: $reason"
  fi

  if [[ "$output" != *'=== Cephadm OSD diagnostics for test ==='* ]]; then
    fail 'bootstrap failure did not collect OSD diagnostics'
  fi
}

test_bootstrap_waits_for_all_osds() (
  trap cleanup EXIT
  setup_test_environment '0,3'
  run_bootstrap

  stat_calls=$(grep -c -- 'ceph osd stat --format json' "$ADOPTUTILS_TEST_LXC_LOG" || true)
  if [[ "$stat_calls" -ne 2 ]]; then
    fail "expected two OSD status checks, got $stat_calls"
  fi

  sleep_calls=$(wc -l < "$ADOPTUTILS_TEST_SLEEP_LOG")
  if [[ "$sleep_calls" -ne 1 ]]; then
    fail "expected one OSD readiness wait, got $sleep_calls"
  fi
)

test_osd_timeout_dumps_diagnostics() (
  trap cleanup EXIT
  setup_test_environment '0'

  if output=$(PATH="$test_dir/bin:$PATH" bash "$adoptutils" wait_for_up_osds test 3 1 2>&1); then
    fail 'wait_for_up_osds unexpectedly succeeded without OSDs'
  fi

  if [[ "$output" != *'=== Cephadm OSD diagnostics for test ==='* ]]; then
    fail 'OSD timeout did not label its diagnostic dump'
  fi

  for expected in \
    'ceph health detail' \
    'ceph osd tree' \
    'ceph orch ps --daemon_type osd --format json-pretty' \
    'ceph orch device ls --wide' \
    'timeout 30s cephadm ls' \
    'lsblk --all --paths --output' \
    'dmsetup ls --tree' \
    'multipath -ll' \
    'systemctl is-active multipathd.service multipathd.socket' \
    'systemctl --no-pager --failed' \
    'podman ps --all --no-trunc' \
    'docker ps --all --no-trunc' \
    "journalctl --no-pager -b -u 'ceph-*@osd.*' -n 500"; do
    if ! grep -Fq -- "$expected" "$ADOPTUTILS_TEST_LXC_LOG"; then
      fail "OSD timeout omitted diagnostic command: $expected"
    fi
  done

  if grep -Fq -- 'ceph-volume inventory' "$ADOPTUTILS_TEST_LXC_LOG"; then
    fail 'OSD diagnostics used ceph-volume inventory instead of direct LVM state'
  fi
)

test_bootstrap_failure_dumps_diagnostics() (
  trap cleanup EXIT
  setup_test_environment '3' 'HEALTH_OK' 'cephadm --image'
  output=$(run_bootstrap_expect_failure)
  assert_osd_diagnostics "$output" 'cephadm bootstrap failed on test; collecting diagnostics'
)

test_osd_provisioning_failure_dumps_diagnostics() (
  trap cleanup EXIT
  setup_test_environment '3' 'HEALTH_OK' 'ceph orch apply osd'
  output=$(run_bootstrap_expect_failure)
  assert_osd_diagnostics "$output" 'OSD provisioning request failed on test; collecting diagnostics'
)

test_health_timeout_dumps_diagnostics() (
  trap cleanup EXIT
  setup_test_environment '3' 'HEALTH_WARN'
  output=$(run_bootstrap_expect_failure)
  assert_osd_diagnostics "$output" 'Cluster on test did not reach HEALTH_OK; collecting diagnostics'
)

test_adoption_does_not_log_admin_key() (
  trap cleanup EXIT
  setup_test_environment '3'

  if ! output=$(PATH="$test_dir/bin:$PATH" bash "$adoptutils" adopt_cephadm test /tmp/microceph.snap 2>&1); then
    fail 'adopt_cephadm returned a failure'
  fi

  if [[ "$output" == *"$ADOPTUTILS_TEST_CEPHX_KEY"* ]]; then
    fail 'adoption xtrace exposed the admin key'
  fi

  if grep -Fq -- "$ADOPTUTILS_TEST_CEPHX_KEY" "$ADOPTUTILS_TEST_LXC_LOG"; then
    fail 'adoption passed the admin key as an lxc command argument'
  fi
)

test_adoption_traces_non_secret_steps() (
  trap cleanup EXIT
  setup_test_environment '3'

  if ! output=$(PATH="$test_dir/bin:$PATH" bash "$adoptutils" adopt_cephadm test /tmp/microceph.snap 2>&1); then
    fail 'adopt_cephadm returned a failure'
  fi

  if [[ "$output" != *'snap connect microceph:block-devices'* ]]; then
    fail 'adoption stopped tracing non-secret setup steps'
  fi
)

test_bootstrap_pins_compatible_ceph_image() (
  trap cleanup EXIT
  setup_test_environment '3'
  run_bootstrap

  if ! grep -Fq -- 'cephadm --image quay.io/ceph/ceph:v19.2.5 bootstrap' "$ADOPTUTILS_TEST_LXC_LOG"; then
    fail 'bootstrap did not select the compatible Ceph image'
  fi
)

case "${1:-all}" in
  all)
    test_bootstrap_waits_for_all_osds
    test_osd_timeout_dumps_diagnostics
    test_bootstrap_failure_dumps_diagnostics
    test_osd_provisioning_failure_dumps_diagnostics
    test_health_timeout_dumps_diagnostics
    test_adoption_does_not_log_admin_key
    test_adoption_traces_non_secret_steps
    test_bootstrap_pins_compatible_ceph_image
    ;;
  waits-for-osds)
    test_bootstrap_waits_for_all_osds
    ;;
  dumps-osd-diagnostics)
    test_osd_timeout_dumps_diagnostics
    ;;
  bootstrap-failure-diagnostics)
    test_bootstrap_failure_dumps_diagnostics
    ;;
  osd-provisioning-failure-diagnostics)
    test_osd_provisioning_failure_dumps_diagnostics
    ;;
  health-timeout-diagnostics)
    test_health_timeout_dumps_diagnostics
    ;;
  hides-admin-key)
    test_adoption_does_not_log_admin_key
    ;;
  traces-non-secret-steps)
    test_adoption_traces_non_secret_steps
    ;;
  pins-ceph-image)
    test_bootstrap_pins_compatible_ceph_image
    ;;
  *)
    fail "unknown test: $1"
    ;;
esac

printf '%s\n' 'PASS: adoptutils shell tests'
