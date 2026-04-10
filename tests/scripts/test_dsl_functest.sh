#!/bin/bash
# Functional test harness for DSL-based device matching in MicroCeph.
#
# Available suite families:
# - baseline OSD DSL coverage
# - WAL/DB validation coverage
# - WAL/DB dry-run planning coverage
# - WAL/DB provisioning, cleanup, and consistency coverage

set -euo pipefail

# Configuration
VM_NAME="${VM_NAME:-microceph-dsl-test}"
PROFILE="${PROFILE:-default}"
CORES="${CORES:-2}"
MEM="${MEM:-4GiB}"
STORAGE_POOL="${STORAGE_POOL:-default}"
SNAP_PATH="${SNAP_PATH:-}"  # Path to local snap file, empty means use store
SNAP_CHANNEL="${SNAP_CHANNEL:-latest/edge}"
LXD_VM_IMAGE="${LXD_VM_IMAGE:-}"
NO_CLEANUP="${NO_CLEANUP:-0}"
REQUESTED_FUNCTION="${REQUESTED_FUNCTION:-}"

# Disk topology used by the harness.
OSD1_NAME="${OSD1_NAME:-${VM_NAME}-osd1}"
OSD1_SIZE="${OSD1_SIZE:-10GiB}"
OSD2_NAME="${OSD2_NAME:-${VM_NAME}-osd2}"
OSD2_SIZE="${OSD2_SIZE:-11GiB}"
OSD3_NAME="${OSD3_NAME:-${VM_NAME}-osd3}"
OSD3_SIZE="${OSD3_SIZE:-12GiB}"
WAL1_NAME="${WAL1_NAME:-${VM_NAME}-wal1}"
WAL1_SIZE="${WAL1_SIZE:-20GiB}"
WAL2_NAME="${WAL2_NAME:-${VM_NAME}-wal2}"
WAL2_SIZE="${WAL2_SIZE:-21GiB}"
DB1_NAME="${DB1_NAME:-${VM_NAME}-db1}"
DB1_SIZE="${DB1_SIZE:-30GiB}"
DB2_NAME="${DB2_NAME:-${VM_NAME}-db2}"
DB2_SIZE="${DB2_SIZE:-31GiB}"
RO1_NAME="${RO1_NAME:-${VM_NAME}-ro1}"
RO1_SIZE="${RO1_SIZE:-9GiB}"

# Test/result tracking.
DSL_RESULT_FILE="${DSL_RESULT_FILE:-}"
DSL_SUPPRESS_TEST_SUMMARY="${DSL_SUPPRESS_TEST_SUMMARY:-0}"
CURRENT_SUITE_NAME=""
CURRENT_TEST_KEY=""
CURRENT_TEST_LABEL=""
CURRENT_TEST_SKIP_REASON=""
declare -a DSL_PLANNED_TEST_KEYS=()
declare -a DSL_PLANNED_TEST_LABELS=()
declare -a DSL_RESULT_KEYS=()
declare -a DSL_RESULT_LABELS=()
declare -a DSL_RESULT_STATUSES=()
declare -a DSL_RESULT_DETAILS=()

function log() {
    echo "[dsl-functest] $*"
}

function dsl_reset_test_tracking() {
    CURRENT_SUITE_NAME=""
    CURRENT_TEST_KEY=""
    CURRENT_TEST_LABEL=""
    CURRENT_TEST_SKIP_REASON=""
    DSL_PLANNED_TEST_KEYS=()
    DSL_PLANNED_TEST_LABELS=()
    DSL_RESULT_KEYS=()
    DSL_RESULT_LABELS=()
    DSL_RESULT_STATUSES=()
    DSL_RESULT_DETAILS=()
}

function dsl_set_suite_context() {
    CURRENT_SUITE_NAME="$1"
}

function dsl_plan_test() {
    local key="$1"
    local label="${2:-$1}"

    DSL_PLANNED_TEST_KEYS+=("$key")
    DSL_PLANNED_TEST_LABELS+=("$label")
}

function dsl_sanitize_detail() {
    local detail="${1:-}"
    detail="${detail//$'\n'/ }"
    detail="${detail//$'\t'/ }"
    printf '%s' "$detail"
}

function dsl_result_index_for_key() {
    local key="$1"
    local i

    for i in "${!DSL_RESULT_KEYS[@]}"; do
        if [[ "${DSL_RESULT_KEYS[$i]}" == "$key" ]]; then
            echo "$i"
            return 0
        fi
    done

    echo "-1"
}

function dsl_add_test_result() {
    local key="$1"
    local label="$2"
    local status="$3"
    local detail
    local index

    detail=$(dsl_sanitize_detail "${4:-}")
    index=$(dsl_result_index_for_key "$key")

    if [[ "$index" != "-1" ]]; then
        DSL_RESULT_LABELS[$index]="$label"
        DSL_RESULT_STATUSES[$index]="$status"
        DSL_RESULT_DETAILS[$index]="$detail"
        return 0
    fi

    DSL_RESULT_KEYS+=("$key")
    DSL_RESULT_LABELS+=("$label")
    DSL_RESULT_STATUSES+=("$status")
    DSL_RESULT_DETAILS+=("$detail")
}

function dsl_emit_test_result() {
    local status="$1"
    local label="$2"
    local detail

    detail=$(dsl_sanitize_detail "${3:-}")

    if [[ -n "$detail" ]]; then
        log "RESULT: $status $label -- $detail"
    else
        log "RESULT: $status $label"
    fi

    if [[ -n "$DSL_RESULT_FILE" && -n "$CURRENT_TEST_KEY" ]]; then
        printf '%s\t%s\t%s\t%s\n' "$status" "$CURRENT_TEST_KEY" "$label" "$detail" >> "$DSL_RESULT_FILE"
    fi
}

function dsl_record_test_result() {
    local key="$1"
    local label="$2"
    local status="$3"
    local detail="${4:-}"

    dsl_add_test_result "$key" "$label" "$status" "$detail"
    dsl_emit_test_result "$status" "$label" "$detail"

    if [[ "$CURRENT_TEST_KEY" == "$key" ]]; then
        CURRENT_TEST_KEY=""
        CURRENT_TEST_LABEL=""
        CURRENT_TEST_SKIP_REASON=""
    fi
}

function dsl_start_test() {
    CURRENT_TEST_KEY="$1"
    CURRENT_TEST_LABEL="${2:-$1}"
    CURRENT_TEST_SKIP_REASON=""
    log "=== BEGIN TEST: $CURRENT_TEST_LABEL ==="
}

function dsl_note_skip() {
    if [[ -n "$CURRENT_TEST_KEY" ]]; then
        CURRENT_TEST_SKIP_REASON=$(dsl_sanitize_detail "$1")
    fi
}

function dsl_finish_current_test_success() {
    local status="PASS"
    local detail=""

    if [[ -n "$CURRENT_TEST_SKIP_REASON" ]]; then
        status="SKIP"
        detail="$CURRENT_TEST_SKIP_REASON"
    fi

    if [[ -n "$CURRENT_TEST_KEY" ]]; then
        dsl_record_test_result "$CURRENT_TEST_KEY" "$CURRENT_TEST_LABEL" "$status" "$detail"
    fi
}

function dsl_record_current_failure() {
    local detail
    detail=$(dsl_sanitize_detail "${1:-unknown failure}")

    if [[ -n "$CURRENT_TEST_KEY" ]]; then
        dsl_record_test_result "$CURRENT_TEST_KEY" "$CURRENT_TEST_LABEL" "FAIL" "$detail"
        return 0
    fi

    dsl_add_test_result "__harness__" "harness" "FAIL" "$detail"
    if [[ -n "$detail" ]]; then
        log "RESULT: FAIL harness -- $detail"
    else
        log "RESULT: FAIL harness"
    fi
}

function dsl_count_results_by_status() {
    local wanted_status="$1"
    local count=0
    local i

    for i in "${!DSL_RESULT_STATUSES[@]}"; do
        if [[ "${DSL_RESULT_STATUSES[$i]}" == "$wanted_status" ]]; then
            count=$((count + 1))
        fi
    done

    echo "$count"
}

function dsl_key_recorded() {
    local key="$1"
    local i

    for i in "${!DSL_RESULT_KEYS[@]}"; do
        if [[ "${DSL_RESULT_KEYS[$i]}" == "$key" ]]; then
            return 0
        fi
    done

    return 1
}

function dsl_count_not_run_tests() {
    local count=0
    local i

    for i in "${!DSL_PLANNED_TEST_KEYS[@]}"; do
        if ! dsl_key_recorded "${DSL_PLANNED_TEST_KEYS[$i]}"; then
            count=$((count + 1))
        fi
    done

    echo "$count"
}

function dsl_print_results_group() {
    local status="$1"
    local i
    local printed=0

    for i in "${!DSL_RESULT_STATUSES[@]}"; do
        if [[ "${DSL_RESULT_STATUSES[$i]}" != "$status" ]]; then
            continue
        fi

        if [[ $printed -eq 0 ]]; then
            log "$status:"
            printed=1
        fi

        if [[ -n "${DSL_RESULT_DETAILS[$i]}" ]]; then
            log "  - ${DSL_RESULT_LABELS[$i]} -- ${DSL_RESULT_DETAILS[$i]}"
        else
            log "  - ${DSL_RESULT_LABELS[$i]}"
        fi
    done
}

function dsl_print_not_run_group() {
    local i
    local printed=0

    for i in "${!DSL_PLANNED_TEST_KEYS[@]}"; do
        if dsl_key_recorded "${DSL_PLANNED_TEST_KEYS[$i]}"; then
            continue
        fi

        if [[ $printed -eq 0 ]]; then
            log "NOT RUN:"
            printed=1
        fi

        log "  - ${DSL_PLANNED_TEST_LABELS[$i]}"
    done
}

function dsl_print_test_summary() {
    local pass_count skip_count fail_count not_run_count

    pass_count=$(dsl_count_results_by_status "PASS")
    skip_count=$(dsl_count_results_by_status "SKIP")
    fail_count=$(dsl_count_results_by_status "FAIL")
    not_run_count=$(dsl_count_not_run_tests)

    log "=== Test Summary ==="
    if [[ -n "$CURRENT_SUITE_NAME" ]]; then
        log "Context: $CURRENT_SUITE_NAME"
    fi
    log "Passed: $pass_count  Skipped: $skip_count  Failed: $fail_count  Not run: $not_run_count"

    dsl_print_results_group "PASS"
    dsl_print_results_group "SKIP"
    dsl_print_results_group "FAIL"
    dsl_print_not_run_group
}

function dsl_run_test() {
    local test_function="$1"
    local test_label="${2:-$1}"

    dsl_start_test "$test_function" "$test_label"
    "$test_function"
    dsl_finish_current_test_success
}

function skip_test() {
    dsl_note_skip "$*"
    log "SKIP: $*"
}

function fail() {
    dsl_record_current_failure "$*"
    echo "[dsl-functest] FAIL: $*" >&2
    exit 1
}

function run_and_capture() {
    local __statusvar="$1"
    local __outputvar="$2"
    shift 2

    local __captured_status __captured_output
    set +e
    __captured_output=$("$@" 2>&1)
    __captured_status=$?
    set -e

    printf -v "$__statusvar" '%s' "$__captured_status"
    printf -v "$__outputvar" '%s' "$__captured_output"
}

function expect_command_status() {
    local expected_status="$1"
    local description="$2"
    shift 2

    local status output
    run_and_capture status output "$@"
    echo "$output" >&2

    if [[ "$status" != "$expected_status" ]]; then
        fail "$description (expected exit $expected_status, got $status)"
    fi

    printf '%s\n' "$output"
}

function vm_exec_expect_success() {
    local description="$1"
    shift
    expect_command_status 0 "$description" vm_exec "$@"
}

function vm_exec_expect_failure() {
    local description="$1"
    shift

    local status output
    run_and_capture status output vm_exec "$@"
    echo "$output" >&2
    if [[ "$status" == "0" ]]; then
        fail "$description (expected non-zero exit status)"
    fi
    printf '%s\n' "$output"
}

function assert_output_contains() {
    local output="$1"
    local pattern="$2"
    local context="${3:-expected output to contain '$pattern'}"

    if ! grep -Eqi "$pattern" <<<"$output"; then
        echo "$output"
        fail "$context"
    fi
}

function assert_output_not_contains() {
    local output="$1"
    local pattern="$2"
    local context="${3:-expected output to not contain '$pattern'}"

    if grep -Eqi "$pattern" <<<"$output"; then
        echo "$output"
        fail "$context"
    fi
}

function assert_eq() {
    local actual="$1"
    local expected="$2"
    local context="${3:-values differ}"

    if [[ "$actual" != "$expected" ]]; then
        fail "$context (expected '$expected', got '$actual')"
    fi
}

function assert_ge() {
    local actual="$1"
    local expected="$2"
    local context="${3:-value is smaller than expected}"

    if (( actual < expected )); then
        fail "$context (expected >= $expected, got $actual)"
    fi
}

function human_gib_string() {
    local gib="${1%GiB}"
    printf "%s.00GiB" "$gib"
}

function dsl_volume_names() {
    cat <<EOF
$OSD1_NAME
$OSD2_NAME
$OSD3_NAME
$WAL1_NAME
$WAL2_NAME
$DB1_NAME
$DB2_NAME
$RO1_NAME
EOF
}

function rw_volume_names() {
    cat <<EOF
$OSD1_NAME
$OSD2_NAME
$OSD3_NAME
$WAL1_NAME
$WAL2_NAME
$DB1_NAME
$DB2_NAME
EOF
}

function create_dsl_volumes() {
    log "Creating DSL test volumes on storage pool '$STORAGE_POOL'"

    local pair name size
    while IFS='=' read -r name size; do
        if lxc storage volume show "$STORAGE_POOL" "$name" </dev/null &>/dev/null; then
            log "Deleting pre-existing storage volume '$name'"
            lxc storage volume delete "$STORAGE_POOL" "$name" </dev/null 2>/dev/null || true
        fi
        lxc storage volume create "$STORAGE_POOL" "$name" --type=block "size=$size" </dev/null
    done <<EOF
$OSD1_NAME=$OSD1_SIZE
$OSD2_NAME=$OSD2_SIZE
$OSD3_NAME=$OSD3_SIZE
$WAL1_NAME=$WAL1_SIZE
$WAL2_NAME=$WAL2_SIZE
$DB1_NAME=$DB1_SIZE
$DB2_NAME=$DB2_SIZE
$RO1_NAME=$RO1_SIZE
EOF
}

function attach_dsl_volumes() {
    log "Attaching RW block volumes to VM '$VM_NAME'"

    local volume
    while read -r volume; do
        [[ -n "$volume" ]] || continue
        lxc storage volume attach "$STORAGE_POOL" "$volume" "$VM_NAME" </dev/null
    done < <(rw_volume_names)
}

function attach_readonly_volume() {
    log "Attaching read-only block volume '$RO1_NAME' to VM '$VM_NAME'"
    lxc config device add "$VM_NAME" "$RO1_NAME" disk pool="$STORAGE_POOL" source="$RO1_NAME" readonly=true
}

# Cleanup function
function cleanup_dsl_test() {
    local exit_code=$?

    if [[ $exit_code -ne 0 ]]; then
        show_vm_debug_on_failure || true
    fi

    if [[ "$NO_CLEANUP" == "1" ]]; then
        log "NO_CLEANUP=1, keeping VM and storage volumes"
    else
        log "Cleaning up DSL test resources..."

        lxc stop "$VM_NAME" --force 2>/dev/null || true
        lxc delete "$VM_NAME" --force 2>/dev/null || true

        local volume
        while read -r volume; do
            [[ -n "$volume" ]] || continue
            lxc storage volume delete "$STORAGE_POOL" "$volume" </dev/null 2>/dev/null || true
        done < <(dsl_volume_names)
    fi

    if [[ "$DSL_SUPPRESS_TEST_SUMMARY" != "1" ]]; then
        dsl_print_test_summary
    fi

    if [[ $exit_code -eq 0 ]]; then
        log "Test completed successfully"
    else
        log "Test failed with exit code $exit_code"
    fi

    exit $exit_code
}

function show_vm_debug_on_failure() {
    if ! lxc info "$VM_NAME" &>/dev/null; then
        return 0
    fi

    log "Collecting failure diagnostics from VM '$VM_NAME'"
    vm_exec microceph disk list --json || true
    vm_exec microceph status || true
    vm_exec microceph.ceph -s || true
    vm_exec microceph.ceph osd status || true
    vm_exec lsblk -o NAME,PATH,PKNAME,TYPE,SIZE,FSTYPE,RO,MOUNTPOINTS || true
    vm_shell '
        disk_json=$(microceph disk list --json 2>/dev/null || printf "{}")
        printf "%s\n" "$disk_json"
        printf "%s\n" "$disk_json" \
          | jq -r "[((.ConfiguredDisks // [])[]? | .path), ((.AvailableDisks // [])[]? | .Path)] | unique[]?" 2>/dev/null \
          | while read -r path; do
                [ -n "$path" ] || continue
                echo "=== debug for $path ==="
                resolved=$(readlink -f "$path" 2>/dev/null || printf "%s" "$path")
                echo "resolved=$resolved"
                lsblk -o NAME,PATH,PKNAME,TYPE,SIZE,FSTYPE,RO,MOUNTPOINTS "$resolved" 2>/dev/null || true
                sfdisk -d "$resolved" 2>/dev/null || true
            done
    ' || true
    vm_exec snap logs microceph -n 300 || true
}

function wait_for_vm_command() {
    local description="$1"
    local timeout="$2"
    shift 2

    local elapsed=0
    while [[ $elapsed -lt $timeout ]]; do
        if "$@" >/dev/null 2>&1; then
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done

    fail "Timed out waiting for ${description}"
}

# Wait for VM to be ready
function wait_for_dsl_vm() {
    local name=$1
    local timeout=${2:-300}

    log "Waiting for VM '$name' to be ready (timeout: ${timeout}s)..."
    wait_for_vm_command "VM '$name' to be ready" "$timeout" bash -lc "lxc exec '$name' -- cloud-init status 2>/dev/null | grep -q done"
    log "VM '$name' is ready"
}

# Run command in VM and return output
function vm_exec() {
    lxc exec "$VM_NAME" -- "$@"
}

# Run shell command in VM
function vm_shell() {
    lxc exec "$VM_NAME" -- sh -lc "$*"
}

function wait_for_vm_disk_count_ge() {
    local expected="$1"
    local timeout="${2:-120}"

    wait_for_vm_command "at least ${expected} visible block disks in the VM" "$timeout" vm_shell "[ \$(lsblk -dn -o TYPE | grep -c '^disk$') -ge ${expected} ]"
}

function microceph_ready() {
    vm_shell "microceph status >/dev/null 2>&1 && microceph disk list --json >/dev/null 2>&1"
}

function wait_for_microceph_ready() {
    local timeout="${1:-180}"

    wait_for_vm_command "MicroCeph daemon readiness" "$timeout" microceph_ready
}

function wait_for_microceph_ready_nonfatal() {
    local timeout="${1:-180}"
    local elapsed=0

    while [[ $elapsed -lt $timeout ]]; do
        if microceph_ready >/dev/null 2>&1; then
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done

    return 1
}

function bootstrap_microceph_cluster_in_vm() {
    local attempt=1
    local max_attempts=3
    local status output

    while (( attempt <= max_attempts )); do
        log "Bootstrapping MicroCeph cluster (attempt ${attempt}/${max_attempts})..."
        log "Running in VM: microceph cluster bootstrap"

        run_and_capture status output vm_exec microceph cluster bootstrap
        echo "$output" >&2

        if [[ "$status" == "0" ]]; then
            wait_for_microceph_ready 180
            return 0
        fi

        if grep -Eq 'Database is online|cluster already exists|already bootstrapped' <<<"$output"; then
            log "Bootstrap reported an already-initialized cluster; continuing"
            wait_for_microceph_ready 180
            return 0
        fi

        if wait_for_microceph_ready_nonfatal 90; then
            log "Bootstrap command exited with status $status, but the cluster became ready; continuing"
            return 0
        fi

        if (( attempt >= max_attempts )); then
            fail "microceph cluster bootstrap (expected exit 0, got $status)"
        fi

        log "Bootstrap attempt ${attempt} exited with status $status; retrying after 10s..."
        sleep 10
        attempt=$((attempt + 1))
    done
}

# Run command in VM and check exit code
function vm_exec_check() {
    local description=$1
    shift

    log "Running in VM: $*"
    vm_exec_expect_success "$description" "$@" >/dev/null
}

function get_disk_list_json() {
    vm_exec microceph disk list --json
}

function normalize_disk_size_display() {
    local size_spec="$1"
    local compact="${size_spec// /}"

    if [[ "$compact" =~ ^[0-9]+GiB$ ]]; then
        human_gib_string "$compact"
        return 0
    fi

    echo "$compact"
}

function get_available_disks_json() {
    get_disk_list_json | jq -c '.AvailableDisks // []'
}

function get_configured_disks_json() {
    get_disk_list_json | jq -c '.ConfiguredDisks // []'
}

function json_available_count() {
    get_disk_list_json | jq -r '(.AvailableDisks // []) | length'
}

function json_configured_count() {
    get_disk_list_json | jq -r '(.ConfiguredDisks // []) | length'
}

function json_first_available_path() {
    get_disk_list_json | jq -r '[.AvailableDisks[]? | .Path][0] // ""'
}

function json_first_available_type() {
    get_disk_list_json | jq -r '[.AvailableDisks[]? | .Type][0] // ""'
}

function json_first_available_path_for_type() {
    local dtype="$1"
    get_disk_list_json | jq -r --arg dtype "$dtype" '[.AvailableDisks[]? | select((.Type // "") == $dtype) | .Path][0] // ""'
}

function json_available_count_for_display_size() {
    local size_display
    size_display=$(normalize_disk_size_display "$1")

    get_disk_list_json | jq -r --arg size "$size_display" '
        [.AvailableDisks[]? | select(((.Size // "") | gsub(" "; "")) == $size)] | length
    '
}

function find_available_disk_record_by_size() {
    local size_display
    size_display=$(normalize_disk_size_display "$1")

    get_disk_list_json | jq -r --arg size "$size_display" '
        [
            .AvailableDisks[]?
            | select(((.Size // "") | gsub(" "; "")) == $size)
            | [.Path, (.Type // "unknown"), (.Size // "unknown")]
            | @tsv
        ][0] // ""
    '
}

function get_available_disk_path_by_size() {
    local size_display match path dtype actual_size
    size_display=$(normalize_disk_size_display "$1")
    match=$(find_available_disk_record_by_size "$size_display")

    if [[ -z "$match" ]]; then
        log "Resolved available disk match size=$size_display -> <none>" >&2
        fail "Could not resolve available disk with size $size_display"
    fi

    IFS=$'\t' read -r path dtype actual_size <<<"$match"
    log "Resolved available disk match size=$size_display -> $path (type=$dtype, size=$actual_size)" >&2
    echo "$path"
}

function format_tsv_table() {
    awk -F'\t' '
        {
            rows[NR] = $0
            if (NF > max_nf) {
                max_nf = NF
            }
            for (i = 1; i <= NF; i++) {
                if (length($i) > widths[i]) {
                    widths[i] = length($i)
                }
            }
        }
        END {
            for (row = 1; row <= NR; row++) {
                split(rows[row], fields, FS)
                line = ""
                for (i = 1; i <= max_nf; i++) {
                    field = fields[i]
                    if (i < max_nf) {
                        line = line sprintf("%-*s  ", widths[i], field)
                    } else {
                        line = line field
                    }
                }
                print line

                if (row == 1 && NR > 1) {
                    separator = ""
                    for (i = 1; i <= max_nf; i++) {
                        dashes = sprintf("%*s", widths[i], "")
                        gsub(/ /, "-", dashes)
                        if (i < max_nf) {
                            separator = separator dashes "  "
                        } else {
                            separator = separator dashes
                        }
                    }
                    print separator
                }
            }
        }
    '
}

function log_tsv_table() {
    local title="$1"
    local table_tsv="${2:-}"
    local formatted

    log "$title:"

    if [[ -z "$table_tsv" ]]; then
        log "  <none>"
        return 0
    fi

    formatted=$(format_tsv_table <<<"$table_tsv")
    while IFS= read -r line; do
        [[ -n "$line" ]] || continue
        log "  $line"
    done <<<"$formatted"
}

function log_available_disks_snapshot() {
    local table
    table=$(get_disk_list_json | jq -r '
        if (.AvailableDisks // [] | length) == 0 then
            empty
        else
            (["PATH", "TYPE", "SIZE"] | @tsv),
            (.AvailableDisks[]? | [(.Path // "unknown"), (.Type // "unknown"), (.Size // "unknown")] | @tsv)
        end
    ')

    log_tsv_table "Current available disks" "$table"
}

function log_configured_disks_snapshot() {
    local table
    table=$(get_disk_list_json | jq -r '
        if (.ConfiguredDisks // [] | length) == 0 then
            empty
        else
            (["OSD", "PATH"] | @tsv),
            (.ConfiguredDisks[]? | [((.osd // "unknown") | tostring), (.path // "unknown")] | @tsv)
        end
    ')

    log_tsv_table "Current configured disks" "$table"
}

function log_available_disk_matches_by_sizes() {
    local label="$1"
    shift

    local disk_list_json size_spec size_display table
    disk_list_json=$(get_disk_list_json)

    for size_spec in "$@"; do
        size_display=$(normalize_disk_size_display "$size_spec")
        table=$(jq -r --arg size "$size_display" '
            if ([.AvailableDisks[]? | select(((.Size // "") | gsub(" "; "")) == $size)] | length) == 0 then
                empty
            else
                (["PATH", "TYPE", "SIZE"] | @tsv),
                (.AvailableDisks[]?
                 | select(((.Size // "") | gsub(" "; "")) == $size)
                 | [(.Path // "unknown"), (.Type // "unknown"), (.Size // "unknown")]
                 | @tsv)
            end
        ' <<<"$disk_list_json")

        log_tsv_table "$label matches for size=$size_display" "$table"
    done
}

function log_available_disk_matches_by_type() {
    local label="$1"
    local dtype="$2"
    local table

    table=$(get_disk_list_json | jq -r --arg dtype "$dtype" '
        if ([.AvailableDisks[]? | select((.Type // "") == $dtype)] | length) == 0 then
            empty
        else
            (["PATH", "TYPE", "SIZE"] | @tsv),
            (.AvailableDisks[]?
             | select((.Type // "") == $dtype)
             | [(.Path // "unknown"), (.Type // "unknown"), (.Size // "unknown")]
             | @tsv)
        end
    ')

    log_tsv_table "$label matches for type=$dtype" "$table"
}

function wait_for_configured_disk_count_ge() {
    local expected=$1
    local timeout=${2:-120}
    local elapsed=0
    local current=0

    while [[ $elapsed -lt $timeout ]]; do
        current=$(json_configured_count)
        if (( current >= expected )); then
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done

    fail "Timed out waiting for configured disk count >= $expected (last=$current)"
}

function wait_for_configured_disk_count_eq() {
    local expected=$1
    local timeout=${2:-120}
    local elapsed=0
    local current=0

    while [[ $elapsed -lt $timeout ]]; do
        current=$(json_configured_count)
        if (( current == expected )); then
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done

    fail "Timed out waiting for configured disk count == $expected (last=$current)"
}

function get_osd_id_for_path() {
    local path="$1"
    get_disk_list_json | jq -r --arg path "$path" '
        [.ConfiguredDisks[]? | select(.path == $path) | (.osd | tostring)][0] // ""
    '
}

function get_osd_data_dir() {
    local osd_id="$1"
    echo "/var/snap/microceph/common/data/osd/ceph-${osd_id}"
}

function assert_path_exists_in_vm() {
    local path="$1"
    vm_shell "test -e '$path'" || fail "Expected path to exist in VM: $path"
}

function assert_path_missing_in_vm() {
    local path="$1"
    if vm_shell "test ! -e '$path'"; then
        return 0
    fi
    fail "Expected path to be absent in VM: $path"
}

function wait_for_path_exists_in_vm() {
    local path="$1"
    local timeout=${2:-120}

    wait_for_vm_command "path to appear in VM: $path" "$timeout" vm_shell "test -e '$path'"
}

function wait_for_path_missing_in_vm() {
    local path="$1"
    local timeout=${2:-120}
    local elapsed=0

    while [[ $elapsed -lt $timeout ]]; do
        if vm_shell "test ! -e '$path'"; then
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done

    fail "Timed out waiting for path to disappear in VM: $path"
}

function get_partition_count() {
    local path="$1"
    vm_shell "lsblk -nr -o TYPE '$path' | grep -c '^part$' || true"
}

function create_partition_on_disk() {
    local path="$1"
    local size_mib="${2:-512}"
    vm_shell "printf 'label: gpt\n,+${size_mib}MiB\n' | sfdisk '$path' >/dev/null && partx -u '$path' >/dev/null 2>&1 || true"
}

function mark_disk_non_pristine() {
    local path="$1"
    vm_shell "printf 'dirty' | dd of='$path' bs=1 conv=notrunc status=none && sync"
}

function partition_number_from_path() {
    local path="$1"

    if [[ "$path" =~ (-part|p)?([0-9]+)$ ]]; then
        echo "${BASH_REMATCH[2]}"
    else
        echo ""
    fi
}

function get_symlink_target() {
    local path="$1"
    vm_shell "readlink -f '$path'"
}

function get_symlink_value() {
    local path="$1"
    vm_shell "readlink '$path'"
}

function ensure_dm_crypt_ready() {
    log "Ensuring dm-crypt support is enabled in VM"
    vm_exec snap connect microceph:dm-crypt || true

    if ! vm_shell "snap connections microceph | awk '\$2 == \"microceph:dm-crypt\" && \$3 != \"-\" { found=1 } END { exit found ? 0 : 1 }'"; then
        skip_test "dm-crypt interface is not connected in the test VM"
        return 1
    fi

    if ! vm_shell "test -e /dev/mapper/control"; then
        skip_test "/dev/mapper/control is unavailable in the test VM"
        return 1
    fi

    vm_exec snap restart microceph.daemon || true
    wait_for_microceph_ready 180
    return 0
}

function disk_add_help() {
    vm_exec microceph disk add --help 2>&1 || true
}

function disk_add_supports_flag() {
    local flag="$1"
    disk_add_help | grep -q -- "$flag"
}

function disk_add_dry_run_json() {
    if ! disk_add_supports_flag '--json'; then
        skip_test "--json not available yet"
        return 1
    fi

    vm_exec_expect_success "dry-run json command should succeed" microceph disk add "$@" --dry-run --json
}

function resolve_lxd_vm_image() {
    if [[ -n "$LXD_VM_IMAGE" ]]; then
        echo "$LXD_VM_IMAGE"
        return 0
    fi

    local fingerprint
    fingerprint=$(lxc image list --format csv -c fptd | awk -F, '$2 == "no" && $3 == "VIRTUAL-MACHINE" && $4 ~ /^ubuntu 24\.04 LTS amd64/ { print $1; exit }')
    if [[ -n "$fingerprint" ]]; then
        echo "$fingerprint"
        return 0
    fi

    echo "ubuntu:24.04"
}

function launch_dsl_vm() {
    local attempt=1
    local max_attempts=3
    local status output image
    image=$(resolve_lxd_vm_image)

    log "Using LXD VM image '$image'"

    while (( attempt <= max_attempts )); do
        log "Launching VM '$VM_NAME' (attempt ${attempt}/${max_attempts})..."

        run_and_capture status output \
            lxc --quiet launch "$image" "$VM_NAME" \
                -p "$PROFILE" \
                -c limits.cpu="$CORES" \
                -c limits.memory="$MEM" \
                --vm
        echo "$output" >&2

        if [[ "$status" == "0" ]]; then
            return 0
        fi

        lxc delete "$VM_NAME" --force 2>/dev/null || true

        if (( attempt >= max_attempts )); then
            fail "failed to launch VM '$VM_NAME' after ${attempt} attempts"
        fi

        log "Launch attempt ${attempt} failed (status $status); retrying after 10s..."
        sleep 10
        attempt=$((attempt + 1))
    done
}

# Setup DSL test environment
function setup_dsl_test() {
    log "=== MicroCeph DSL Functional Test ==="
    log "VM Name: $VM_NAME"
    log "Storage pool: $STORAGE_POOL"
    log "Disk topology: osd(10/11/12GiB) wal(20/21GiB) db(30/31GiB) ro(9GiB)"

    if lxc info "$VM_NAME" &>/dev/null; then
        log "VM '$VM_NAME' already exists, deleting..."
        lxc stop "$VM_NAME" --force 2>/dev/null || true
        lxc delete "$VM_NAME" --force 2>/dev/null || true
    fi

    create_dsl_volumes
    launch_dsl_vm

    attach_dsl_volumes
    attach_readonly_volume

    wait_for_dsl_vm "$VM_NAME"
    wait_for_vm_disk_count_ge 9 180
}

# Resolve snap path glob pattern to actual file
function resolve_snap_path() {
    local pattern="$1"
    local resolved

    # shellcheck disable=SC2086
    resolved=$(compgen -G "$pattern" | head -n1)

    if [[ -n "$resolved" && -f "$resolved" ]]; then
        echo "$resolved"
        return 0
    fi

    return 1
}

# Install MicroCeph snap in VM
function install_microceph_in_vm() {
    log "Installing MicroCeph snap..."

    local snap_file=""
    if [[ -n "$SNAP_PATH" ]]; then
        snap_file=$(resolve_snap_path "$SNAP_PATH") || true
    fi

    if [[ -n "$snap_file" && -f "$snap_file" ]]; then
        log "Installing from local snap: $snap_file"
        lxc --quiet file push "$snap_file" "$VM_NAME/tmp/microceph.snap"

        local attempt=1
        until vm_exec snap install /tmp/microceph.snap --dangerous; do
            if (( attempt >= 3 )); then
                fail "failed to install local snap after ${attempt} attempts"
            fi
            log "Local snap install failed, retrying (${attempt}/3)..."
            attempt=$((attempt + 1))
            sleep 5
        done

        # Dangerous installs do not auto-connect interfaces.
        # Keep this list aligned with the other test harnesses in tests/scripts/.
        log "Connecting snap interfaces for dangerous install..."
        vm_exec snap connect microceph:block-devices || true
        vm_exec snap connect microceph:hardware-observe || true
        vm_exec snap connect microceph:mount-observe || true
        vm_exec snap connect microceph:dm-crypt || true
        vm_exec snap connect microceph:load-rbd || true
        vm_exec snap connect microceph:microceph-support || true
        vm_exec snap connect microceph:network-bind || true
        vm_exec snap connect microceph:process-control || true
    else
        if [[ -n "$SNAP_PATH" ]]; then
            log "Warning: no snap file found matching '$SNAP_PATH', falling back to snap store"
        fi
        log "Installing from snap store: $SNAP_CHANNEL"
        vm_exec snap install microceph --channel="$SNAP_CHANNEL"
    fi

    bootstrap_microceph_cluster_in_vm
}

function install_hurl_in_vm() {
    log "Installing hurl in VM..."

    vm_shell '
        if command -v hurl >/dev/null 2>&1; then
            exit 0
        fi

        if ! command -v curl >/dev/null 2>&1; then
            sudo apt-get update -qq
            sudo apt-get install -y curl
        fi

        VERSION=5.0.1
        DEB="/tmp/hurl_${VERSION}_amd64.deb"
        URL="https://github.com/Orange-OpenSource/hurl/releases/download/${VERSION}/hurl_${VERSION}_amd64.deb"

        curl --location --silent --show-error --output "$DEB" "$URL"
        sudo apt-get update -qq
        sudo apt-get install -y "$DEB"
    '
}

function get_test_disk_type() {
    local dtype
    dtype=$(json_first_available_type)
    if [[ -z "$dtype" ]]; then
        fail "Could not determine test disk type from available disks"
    fi
    echo "$dtype"
}

function test_dsl_api_disk_hurl() {
    local osd_path wal_path db_path
    local osd_devnode wal_devnode db_devnode
    local hurl_file
    local missing_devnode="/dev/nonexistent-dsl-test"

    osd_path=$(get_available_disk_path_by_size "10GiB")
    wal_path=$(get_available_disk_path_by_size "20GiB")
    db_path=$(get_available_disk_path_by_size "30GiB")

    osd_devnode=$(get_symlink_target "$osd_path")
    wal_devnode=$(get_symlink_target "$wal_path")
    db_devnode=$(get_symlink_target "$db_path")

    [[ -n "$osd_devnode" ]] || fail "Could not resolve kernel devnode for OSD path '$osd_path'"
    [[ -n "$wal_devnode" ]] || fail "Could not resolve kernel devnode for WAL path '$wal_path'"
    [[ -n "$db_devnode" ]] || fail "Could not resolve kernel devnode for DB path '$db_path'"

    install_hurl_in_vm

    hurl_file=$(mktemp)
    cat > "$hurl_file" <<EOF
GET http://localhost/1.0/disks
HTTP 200
[Asserts]
jsonpath "$.metadata" count == 0

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '$osd_devnode')",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.dry_run_devices" count == 1
jsonpath "$.metadata.dry_run_devices[0].path" == "$osd_path"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '$osd_devnode')",
  "wal_match": "eq(@devnode, '$wal_devnode')",
  "wal_size": "1GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "$osd_path"
jsonpath "$.metadata.dry_run_plan[0].wal.kind" == "wal"
jsonpath "$.metadata.dry_run_plan[0].wal.parent_path" == "$wal_path"
jsonpath "$.metadata.dry_run_plan[0].wal.partition" == 1
jsonpath "$.metadata.dry_run_plan[0].wal.size" == "1.00 GiB"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '$osd_devnode')",
  "db_match": "eq(@devnode, '$db_devnode')",
  "db_size": "2GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "$osd_path"
jsonpath "$.metadata.dry_run_plan[0].db.kind" == "db"
jsonpath "$.metadata.dry_run_plan[0].db.parent_path" == "$db_path"
jsonpath "$.metadata.dry_run_plan[0].db.partition" == 1
jsonpath "$.metadata.dry_run_plan[0].db.size" == "2.00 GiB"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '$osd_devnode')",
  "wal_match": "eq(@devnode, '$wal_devnode')",
  "wal_size": "1GiB",
  "db_match": "eq(@devnode, '$db_devnode')",
  "db_size": "2GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "$osd_path"
jsonpath "$.metadata.dry_run_plan[0].wal.parent_path" == "$wal_path"
jsonpath "$.metadata.dry_run_plan[0].db.parent_path" == "$db_path"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '$osd_devnode')",
  "wal_match": "eq(@devnode, '$osd_devnode')",
  "wal_size": "1GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == "OSD and WAL match sets overlap: $osd_path"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '$osd_devnode')",
  "wal_match": "eq(@devnode, '$wal_devnode')",
  "wal_size": "0B",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == "--wal-size must be greater than 0"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '$osd_devnode')",
  "wal_match": "eq(@devnode, '$missing_devnode')",
  "wal_size": "1GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.warnings" count == 1
jsonpath "$.metadata.warnings[0]" == "WAL match expression resolved to no devices; proceeding without WAL"
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "$osd_path"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "osd_match": "eq(@devnode, '$osd_devnode')",
  "db_match": "eq(@devnode, '$missing_devnode')",
  "db_size": "1GiB",
  "dry_run": true
}
HTTP 200
[Asserts]
jsonpath "$.metadata.validation_error" == ""
jsonpath "$.metadata.warnings" count == 1
jsonpath "$.metadata.warnings[0]" == "DB match expression resolved to no devices; proceeding without DB"
jsonpath "$.metadata.dry_run_plan" count == 1
jsonpath "$.metadata.dry_run_plan[0].osd_path" == "$osd_path"

POST http://localhost/1.0/disks
Content-Type: application/json
{
  "path": ["$osd_path"]
}
HTTP 200
[Asserts]
jsonpath "$.metadata.report" count == 1
jsonpath "$.metadata.report[0].report" == "Success"
jsonpath "$.metadata.report[0].path" == "$osd_path"

GET http://localhost/1.0/disks
HTTP 200
[Captures]
osd_id: jsonpath "$.metadata[0].osd"
[Asserts]
jsonpath "$.metadata" count == 1
jsonpath "$.metadata[0].path" == "$osd_path"

DELETE http://localhost/1.0/disks/not-an-int
HTTP 400
[Asserts]
jsonpath "$.error_code" == 400

DELETE http://localhost/1.0/disks/9999
Content-Type: application/json
{
  "osdid": 9999,
  "bypass_safety": true,
  "timeout": 60
}
HTTP 404
[Asserts]
jsonpath "$.error_code" == 404

DELETE http://localhost/1.0/disks/{{osd_id}}
Content-Type: application/json
{
  "osdid": {{osd_id}},
  "bypass_safety": true,
  "timeout": 300
}
HTTP 200

GET http://localhost/1.0/disks
HTTP 200
[Asserts]
jsonpath "$.metadata" count == 0
EOF

    lxc --quiet file push "$hurl_file" "$VM_NAME/tmp/api-disks.hurl"
    rm -f "$hurl_file"

    vm_exec_expect_success "API disk hurl tests should succeed" \
        sudo hurl --unix-socket /var/snap/microceph/common/state/control.socket \
        --test /tmp/api-disks.hurl
}

# Baseline OSD DSL tests ----------------------------------------------------

function test_dsl_disk_list() {
    log "Test: checking disk list JSON output"
    local disk_list
    disk_list=$(get_disk_list_json)
    echo "$disk_list"
    assert_output_contains "$disk_list" 'AvailableDisks' "disk list JSON must contain AvailableDisks"
    assert_output_contains "$disk_list" 'ConfiguredDisks' "disk list JSON must contain ConfiguredDisks"
}

function test_dsl_available_disks() {
    log "Test: verifying available disks"
    local available_count
    available_count=$(json_available_count)
    log "Found $available_count available disks"

    # Expect at least the 7 RW data/WAL/DB carrier disks.
    assert_ge "$available_count" 7 "expected at least 7 available DSL test disks"
}

function test_dsl_type_match() {
    local dtype expected_path dsl_output
    dtype=$(get_test_disk_type)
    expected_path=$(json_first_available_path_for_type "$dtype")
    [[ -n "$expected_path" ]] || fail "Could not resolve an available disk path for type '$dtype'"

    log "Test: DSL dry-run with eq(@type, '$dtype')"
    log_available_disks_snapshot
    log_available_disk_matches_by_type "OSD candidate" "$dtype"
    dsl_output=$(vm_exec_expect_success "type-match dry-run should succeed" microceph disk add --osd-match "eq(@type, '$dtype')" --dry-run)
    assert_output_not_contains "$dsl_output" 'No devices matched the expression|No devices matched' "type-match dry-run unexpectedly matched no devices"
    assert_output_contains "$dsl_output" "$expected_path" "type-match dry-run should list at least one matching device"
}

function test_dsl_size_comparison() {
    log "Test: DSL dry-run with size comparison gt(@size, 5GiB)"
    log_available_disks_snapshot
    local expected_path dsl_size_output
    expected_path=$(json_first_available_path)
    [[ -n "$expected_path" ]] || fail "Could not resolve an available disk path for size-comparison test"
    dsl_size_output=$(vm_exec_expect_success "size-comparison dry-run should succeed" microceph disk add --osd-match "gt(@size, 5GiB)" --dry-run)
    assert_output_not_contains "$dsl_size_output" 'No devices matched the expression|No devices matched' "size-comparison dry-run unexpectedly matched no devices"
    assert_output_contains "$dsl_size_output" "$expected_path" "size-comparison dry-run should include at least one matching device"
}

function test_dsl_combined_conditions() {
    local dtype expected_path dsl_combined
    dtype=$(get_test_disk_type)
    expected_path=$(json_first_available_path_for_type "$dtype")
    [[ -n "$expected_path" ]] || fail "Could not resolve an available disk path for combined-condition test"

    log "Test: DSL dry-run with combined conditions"
    log_available_disks_snapshot
    log_available_disk_matches_by_type "OSD candidate" "$dtype"
    dsl_combined=$(vm_exec_expect_success "combined-condition dry-run should succeed" microceph disk add --osd-match "and(eq(@type, '$dtype'), gt(@size, 5GiB))" --dry-run)
    assert_output_not_contains "$dsl_combined" 'No devices matched the expression|No devices matched' "combined-condition dry-run unexpectedly matched no devices"
    assert_output_contains "$dsl_combined" "$expected_path" "combined-condition dry-run should include at least one matching device"
}

function test_dsl_no_match() {
    log "Test: DSL dry-run with impossible size gt(@size, 100TiB)"
    log_available_disks_snapshot
    local dsl_nomatch
    dsl_nomatch=$(vm_exec microceph disk add --osd-match "gt(@size, 100TiB)" --dry-run 2>&1 || true)
    echo "$dsl_nomatch"
    assert_output_contains "$dsl_nomatch" 'No devices matched the expression|No devices matched' "expected an explicit no-match result"
}

function test_dsl_invalid_expression() {
    log "Test: invalid DSL expression"
    log_available_disks_snapshot
    local dsl_invalid
    dsl_invalid=$(vm_exec microceph disk add --osd-match "invalid(" --dry-run 2>&1 || true)
    echo "$dsl_invalid"
    assert_output_contains "$dsl_invalid" 'error|invalid' "invalid DSL expression should be rejected"
}

function test_dsl_mutual_exclusivity() {
    log "Test: positional args and --osd-match are mutually exclusive"
    log_available_disks_snapshot
    local mutual_excl
    mutual_excl=$(vm_exec microceph disk add --osd-match "gt(@size, 5GiB)" /dev/sdb 2>&1 || true)
    echo "$mutual_excl"
    assert_output_contains "$mutual_excl" 'cannot be used with|mutually exclusive' "mutual exclusivity should be enforced"
}

function test_dsl_add_disk() {
    log "Test: add a single disk using DSL expression"

    local configured_before configured_after add_result expected_path
    configured_before=$(json_configured_count)
    expected_path=$(get_available_disk_path_by_size "10GiB")
    add_result=$(vm_exec_expect_success "dsl add should succeed" microceph disk add --osd-match "eq(@size, 10GiB)")
    assert_output_contains "$add_result" "$expected_path|Success" "dsl add output should mention the added disk or success"

    wait_for_configured_disk_count_ge $((configured_before + 1)) 180
    configured_after=$(json_configured_count)
    assert_ge "$configured_after" $((configured_before + 1)) "expected one disk to be added via DSL"
}

function test_dsl_idempotency() {
    log "Test: DSL should not re-match already-used disks"
    log_configured_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "10GiB"
    local dsl_after_add
    dsl_after_add=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --dry-run 2>&1 || true)
    echo "$dsl_after_add"
    assert_output_contains "$dsl_after_add" 'No devices matched the expression|No devices matched' "used disk should not be re-matched"
}

function test_dsl_pristine_check() {
    log "Test: DSL respects pristine check"

    local test_disk test_devnode dsl_pristine_result
    test_disk=$(json_first_available_path)
    if [[ -z "$test_disk" ]]; then
        skip_test "No available disks left for pristine check"
        return 0
    fi

    test_devnode=$(get_symlink_target "$test_disk")
    if [[ -z "$test_devnode" ]]; then
        fail "Failed to resolve kernel devnode for pristine test disk: $test_disk"
    fi

    log "Using disk for pristine test: $test_disk ($test_devnode)"
    vm_exec dd if=/dev/urandom of="$test_disk" bs=1M count=10 conv=fsync status=none || true

    dsl_pristine_result=$(vm_exec microceph disk add --osd-match "eq(@devnode, '$test_devnode')" 2>&1 || true)
    echo "$dsl_pristine_result"
    assert_output_contains "$dsl_pristine_result" 'not pristine|pristine' "non-pristine disk should be rejected without --wipe"
}

function test_dsl_pristine_with_wipe() {
    log "Test: DSL with --wipe adds a non-pristine disk"

    local test_disk test_devnode configured_before configured_after dsl_wipe_result
    test_disk=$(json_first_available_path)
    if [[ -z "$test_disk" ]]; then
        skip_test "No available disks left for pristine+wipe test"
        return 0
    fi

    test_devnode=$(get_symlink_target "$test_disk")
    if [[ -z "$test_devnode" ]]; then
        fail "Failed to resolve kernel devnode for pristine+wipe test disk: $test_disk"
    fi

    log "Using disk for pristine+wipe test: $test_disk ($test_devnode)"
    vm_exec dd if=/dev/urandom of="$test_disk" bs=1M count=10 conv=fsync status=none || true

    configured_before=$(json_configured_count)
    dsl_wipe_result=$(vm_exec microceph disk add --osd-match "eq(@devnode, '$test_devnode')" --wipe 2>&1 || true)
    echo "$dsl_wipe_result"

    wait_for_configured_disk_count_ge $((configured_before + 1)) 180
    configured_after=$(json_configured_count)
    assert_ge "$configured_after" $((configured_before + 1)) "--wipe should allow adding the non-pristine disk"
}

# WAL/DB DSL coverage --------------------------------------------------------

function test_dsl_readonly_disk_excluded() {
    log "Test: read-only disk exclusion"
    local display_size count output
    display_size=$(human_gib_string "$RO1_SIZE")
    count=$(json_available_count_for_display_size "$display_size")
    assert_eq "$count" "0" "read-only disk should not appear in available disks"
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "$RO1_SIZE"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, ${RO1_SIZE})" --dry-run 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'No devices matched the expression|No devices matched' "read-only disk should not be selected by DSL"
}

function test_dsl_waldb_flag_validation() {
    if ! disk_add_supports_flag '--wal-match'; then
        skip_test "--wal-match not available yet"
        return 0
    fi

    log "Test: WAL/DB DSL flag validation"

    local output
    output=$(vm_exec microceph disk add --wal-match "eq(@size, 20GiB)" 2>&1 || true)
    assert_output_contains "$output" 'osd-match|required' "--wal-match should require --osd-match"

    output=$(vm_exec microceph disk add --db-match "eq(@size, 30GiB)" 2>&1 || true)
    assert_output_contains "$output" 'osd-match|required' "--db-match should require --osd-match"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" 2>&1 || true)
    assert_output_contains "$output" 'wal-size|required' "--wal-match should require --wal-size"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --db-match "eq(@size, 30GiB)" 2>&1 || true)
    assert_output_contains "$output" 'db-size|required' "--db-match should require --db-size"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-encrypt 2>&1 || true)
    assert_output_contains "$output" 'wal-encrypt|required.*wal-match|wal-match.*required' "--wal-encrypt should require --wal-match"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-wipe 2>&1 || true)
    assert_output_contains "$output" 'wal-wipe|required.*wal-match|wal-match.*required' "--wal-wipe should require --wal-match"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --db-encrypt 2>&1 || true)
    assert_output_contains "$output" 'db-encrypt|required.*db-match|db-match.*required' "--db-encrypt should require --db-match"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --db-wipe 2>&1 || true)
    assert_output_contains "$output" 'db-wipe|required.*db-match|db-match.*required' "--db-wipe should require --db-match"
}

function test_dsl_dryrun_wal_only_plan() {
    if ! disk_add_supports_flag '--wal-match'; then
        skip_test "--wal-match not available yet"
        return 0
    fi

    log "Test: WAL-only dry-run plan"
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "10GiB" "11GiB"
    log_available_disk_matches_by_sizes "WAL candidate" "20GiB" "21GiB"
    local output
    output=$(vm_exec microceph disk add --osd-match "or(eq(@size, 10GiB), eq(@size, 11GiB))" --wal-match "or(eq(@size, 20GiB), eq(@size, 21GiB))" --wal-size 1GiB --dry-run 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'Planned OSD/WAL/DB provisioning' "expected dry-run plan header"
    assert_output_contains "$output" 'WAL PARENT' "expected WAL columns in dry-run output"
    assert_output_contains "$output" '1\.00 GiB' "expected requested WAL size in plan"
}

function test_dsl_dryrun_db_only_plan() {
    if ! disk_add_supports_flag '--db-match'; then
        skip_test "--db-match not available yet"
        return 0
    fi

    log "Test: DB-only dry-run plan"
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "10GiB"
    log_available_disk_matches_by_sizes "DB candidate" "30GiB"
    local output
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --db-match "eq(@size, 30GiB)" --db-size 2GiB --dry-run 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'DB PARENT' "expected DB columns in dry-run output"
    assert_output_contains "$output" '2\.00 GiB' "expected requested DB size in plan"
}

function test_dsl_dryrun_waldb_plan() {
    if ! disk_add_supports_flag '--wal-match'; then
        skip_test "WAL/DB flags not available yet"
        return 0
    fi

    log "Test: WAL+DB dry-run plan"
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "10GiB" "11GiB"
    log_available_disk_matches_by_sizes "WAL candidate" "20GiB" "21GiB"
    log_available_disk_matches_by_sizes "DB candidate" "30GiB"
    local output
    output=$(vm_exec microceph disk add --osd-match "or(eq(@size, 10GiB), eq(@size, 11GiB))" --wal-match "or(eq(@size, 20GiB), eq(@size, 21GiB))" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB --dry-run 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'WAL PARENT' "expected WAL columns in combined plan"
    assert_output_contains "$output" 'DB PARENT' "expected DB columns in combined plan"
    assert_output_contains "$output" '/dev/disk/by-(id|path)/' "expected stable device names in plan"
}

function test_dsl_dryrun_deterministic_order() {
    if ! disk_add_supports_flag '--wal-match'; then
        skip_test "WAL/DB flags not available yet"
        return 0
    fi

    log "Test: dry-run output order is deterministic"
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "10GiB" "11GiB"
    log_available_disk_matches_by_sizes "WAL candidate" "20GiB" "21GiB"
    log_available_disk_matches_by_sizes "DB candidate" "30GiB"
    local cmd out1 out2
    cmd='microceph disk add --osd-match "or(eq(@size, 10GiB), eq(@size, 11GiB))" --wal-match "or(eq(@size, 20GiB), eq(@size, 21GiB))" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB --dry-run'
    out1=$(vm_shell "$cmd" 2>&1 || true)
    out2=$(vm_shell "$cmd" 2>&1 || true)
    echo "$out1"
    assert_eq "$out1" "$out2" "dry-run output should be stable across runs"
}

function test_dsl_dryrun_overlap_error() {
    if ! disk_add_supports_flag '--wal-match'; then
        skip_test "WAL/DB flags not available yet"
        return 0
    fi

    log "Test: overlap between OSD and WAL match sets is rejected"
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "Overlapping OSD/WAL candidate" "10GiB"
    local output
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 10GiB)" --wal-size 1GiB --dry-run 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'overlap' "expected overlap validation failure"
}

function test_dsl_dryrun_capacity_error() {
    if ! disk_add_supports_flag '--wal-match'; then
        skip_test "WAL/DB flags not available yet"
        return 0
    fi

    log "Test: impossible WAL capacity is rejected during dry-run"
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "10GiB" "11GiB"
    log_available_disk_matches_by_sizes "WAL candidate" "20GiB"
    local output
    output=$(vm_exec microceph disk add --osd-match "or(eq(@size, 10GiB), eq(@size, 11GiB))" --wal-match "eq(@size, 20GiB)" --wal-size 100GiB --dry-run 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'insufficient capacity|Validation Error' "expected capacity validation failure"
}

function test_dsl_dryrun_empty_wal_warning() {
    if ! disk_add_supports_flag '--wal-match'; then
        skip_test "WAL/DB flags not available yet"
        return 0
    fi

    log "Test: empty WAL match emits warning but succeeds"
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "10GiB"
    log_available_disk_matches_by_sizes "WAL candidate" "999GiB"
    local output
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 999GiB)" --wal-size 1GiB --dry-run 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'Warning: WAL match expression resolved to no devices' "expected WAL warning"
    assert_output_contains "$output" 'Planned OSD/WAL/DB provisioning' "expected plan despite warning"
}

function test_dsl_dryrun_empty_db_warning() {
    if ! disk_add_supports_flag '--db-match'; then
        skip_test "WAL/DB flags not available yet"
        return 0
    fi

    log "Test: empty DB match emits warning but succeeds"
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "10GiB"
    log_available_disk_matches_by_sizes "DB candidate" "999GiB"
    local output
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --db-match "eq(@size, 999GiB)" --db-size 2GiB --dry-run 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'Warning: DB match expression resolved to no devices' "expected DB warning"
    assert_output_contains "$output" 'Planned OSD/WAL/DB provisioning' "expected plan despite warning"
}

function test_dsl_dryrun_no_new_osd_warning() {
    if ! disk_add_supports_flag '--wal-match'; then
        skip_test "WAL/DB flags not available yet"
        return 0
    fi

    log "Test: WAL/DB dry-run warns when no new OSDs are being added"
    local add_output output
    add_output=$(vm_exec microceph disk add --osd-match "eq(@size, 12GiB)" 2>&1 || true)
    echo "$add_output"
    wait_for_configured_disk_count_ge 1 180
    log_available_disks_snapshot
    log_configured_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "12GiB"
    log_available_disk_matches_by_sizes "WAL candidate" "20GiB"
    log_available_disk_matches_by_sizes "DB candidate" "30GiB"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 12GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB --dry-run 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'Warning: WAL/DB settings ignored because no new OSDs are being added' "expected no-new-OSD warning"
}

function test_dsl_add_wal_only() {
    log "Test: add OSD with WAL only"
    local osd_path wal_parent before_parts after_parts output osd_id wal_link target
    osd_path=$(get_available_disk_path_by_size "10GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    before_parts=$(get_partition_count "$wal_parent")
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB 2>&1 || true)
    echo "$output"
    wait_for_configured_disk_count_ge 1 180
    after_parts=$(get_partition_count "$wal_parent")
    assert_eq "$after_parts" "$((before_parts + 1))" "WAL parent should gain one partition"
    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    wal_link="$(get_osd_data_dir "$osd_id")/block.wal"
    assert_path_exists_in_vm "$wal_link"
    target=$(get_symlink_target "$wal_link")
    assert_output_contains "$target" '/dev/' "WAL symlink should resolve to a device"
}

function test_dsl_add_db_only() {
    log "Test: add OSD with DB only"
    local osd_path db_parent before_parts after_parts output osd_id db_link target
    osd_path=$(get_available_disk_path_by_size "11GiB")
    db_parent=$(get_available_disk_path_by_size "30GiB")
    before_parts=$(get_partition_count "$db_parent")
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 11GiB)" --db-match "eq(@size, 30GiB)" --db-size 2GiB 2>&1 || true)
    echo "$output"
    wait_for_configured_disk_count_ge 1 180
    after_parts=$(get_partition_count "$db_parent")
    assert_eq "$after_parts" "$((before_parts + 1))" "DB parent should gain one partition"
    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    db_link="$(get_osd_data_dir "$osd_id")/block.db"
    assert_path_exists_in_vm "$db_link"
    target=$(get_symlink_target "$db_link")
    assert_output_contains "$target" '/dev/' "DB symlink should resolve to a device"
}

function test_dsl_add_waldb() {
    log "Test: add OSD with WAL and DB"
    local osd_path wal_parent db_parent before_wal before_db after_wal after_db output osd_id wal_link db_link
    osd_path=$(get_available_disk_path_by_size "12GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    db_parent=$(get_available_disk_path_by_size "30GiB")
    before_wal=$(get_partition_count "$wal_parent")
    before_db=$(get_partition_count "$db_parent")
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 12GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB 2>&1 || true)
    echo "$output"
    wait_for_configured_disk_count_ge 1 180
    after_wal=$(get_partition_count "$wal_parent")
    after_db=$(get_partition_count "$db_parent")
    assert_eq "$after_wal" "$((before_wal + 1))" "WAL parent should gain one partition"
    assert_eq "$after_db" "$((before_db + 1))" "DB parent should gain one partition"
    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    wal_link="$(get_osd_data_dir "$osd_id")/block.wal"
    db_link="$(get_osd_data_dir "$osd_id")/block.db"
    assert_path_exists_in_vm "$wal_link"
    assert_path_exists_in_vm "$db_link"
}

function test_dsl_empty_wal_match_warns_and_adds_data_only() {
    log "Test: empty WAL match warns and adds data-only OSD"
    local osd_path output osd_id wal_link
    osd_path=$(get_available_disk_path_by_size "10GiB")
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 999GiB)" --wal-size 1GiB 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'Warning: WAL match expression resolved to no devices' "expected WAL warning"
    wait_for_configured_disk_count_ge 1 180
    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    wal_link="$(get_osd_data_dir "$osd_id")/block.wal"
    assert_path_missing_in_vm "$wal_link"
}

function test_dsl_empty_db_match_warns_and_adds_data_only() {
    log "Test: empty DB match warns and adds data-only OSD"
    local osd_path output osd_id db_link
    osd_path=$(get_available_disk_path_by_size "10GiB")
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --db-match "eq(@size, 999GiB)" --db-size 2GiB 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'Warning: DB match expression resolved to no devices' "expected DB warning"
    wait_for_configured_disk_count_ge 1 180
    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    db_link="$(get_osd_data_dir "$osd_id")/block.db"
    assert_path_missing_in_vm "$db_link"
}

function test_dsl_waldb_idempotent_rerun() {
    log "Test: WAL/DB DSL rerun does not create new partitions without new OSDs"
    local wal_parent db_parent before_wal before_db mid_wal mid_db after_wal after_db first second
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    db_parent=$(get_available_disk_path_by_size "30GiB")
    before_wal=$(get_partition_count "$wal_parent")
    before_db=$(get_partition_count "$db_parent")
    first=$(vm_exec microceph disk add --osd-match "eq(@size, 12GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB 2>&1 || true)
    echo "$first"
    wait_for_configured_disk_count_ge 1 180
    mid_wal=$(get_partition_count "$wal_parent")
    mid_db=$(get_partition_count "$db_parent")
    second=$(vm_exec microceph disk add --osd-match "eq(@size, 12GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB 2>&1 || true)
    echo "$second"
    after_wal=$(get_partition_count "$wal_parent")
    after_db=$(get_partition_count "$db_parent")
    assert_eq "$mid_wal" "$((before_wal + 1))" "first run should create one WAL partition"
    assert_eq "$mid_db" "$((before_db + 1))" "first run should create one DB partition"
    assert_eq "$after_wal" "$mid_wal" "rerun should not create more WAL partitions"
    assert_eq "$after_db" "$mid_db" "rerun should not create more DB partitions"
    assert_output_contains "$second" 'Warning: WAL/DB settings ignored because no new OSDs are being added' "expected rerun warning"
}

function test_dsl_waldb_distribution_across_multiple_aux_disks() {
    log "Test: WAL partitions distribute across multiple aux disks"
    local wal1 wal2 before1 before2 after1 after2 output
    wal1=$(get_available_disk_path_by_size "20GiB")
    wal2=$(get_available_disk_path_by_size "21GiB")
    before1=$(get_partition_count "$wal1")
    before2=$(get_partition_count "$wal2")
    output=$(vm_exec microceph disk add --osd-match "or(eq(@size, 10GiB), eq(@size, 11GiB))" --wal-match "or(eq(@size, 20GiB), eq(@size, 21GiB))" --wal-size 1GiB 2>&1 || true)
    echo "$output"
    wait_for_configured_disk_count_ge 2 180
    after1=$(get_partition_count "$wal1")
    after2=$(get_partition_count "$wal2")
    assert_eq "$after1" "$((before1 + 1))" "first WAL carrier should get one partition"
    assert_eq "$after2" "$((before2 + 1))" "second WAL carrier should get one partition"
}

function test_dsl_partitioned_non_ceph_aux_disk_is_rejected() {
    log "Test: partitioned non-Ceph aux disk is rejected as WAL carrier"
    local osd_path wal_parent before_parts after_partition_setup after_parts output osd_id wal_link
    osd_path=$(get_available_disk_path_by_size "10GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    before_parts=$(get_partition_count "$wal_parent")
    create_partition_on_disk "$wal_parent" 512
    after_partition_setup=$(get_partition_count "$wal_parent")
    assert_eq "$after_partition_setup" "$((before_parts + 1))" "setup should create one non-Ceph partition on WAL disk"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'Warning: WAL match expression resolved to no devices' "expected warning for rejected partitioned WAL carrier"
    wait_for_configured_disk_count_ge 1 180

    after_parts=$(get_partition_count "$wal_parent")
    assert_eq "$after_parts" "$after_partition_setup" "partitioned non-Ceph WAL disk must not receive a new WAL partition"
    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    wal_link="$(get_osd_data_dir "$osd_id")/block.wal"
    assert_path_missing_in_vm "$wal_link"
}

function test_dsl_non_pristine_whole_aux_device_requires_wipe() {
    log "Test: non-pristine whole WAL carrier is rejected without --wal-wipe"
    local osd_path wal_parent before_parts after_parts output osd_id wal_link

    osd_path=$(get_available_disk_path_by_size "10GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    before_parts=$(get_partition_count "$wal_parent")
    mark_disk_non_pristine "$wal_parent"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB 2>&1 || true)
    echo "$output"
    assert_output_contains "$output" 'Warning: WAL match expression resolved to no devices' "expected warning for non-pristine WAL carrier without wipe"
    wait_for_configured_disk_count_ge 1 180

    after_parts=$(get_partition_count "$wal_parent")
    assert_eq "$after_parts" "$before_parts" "non-pristine WAL carrier without wipe must not receive a partition"
    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    wal_link="$(get_osd_data_dir "$osd_id")/block.wal"
    assert_path_missing_in_vm "$wal_link"
}

function test_dsl_non_pristine_whole_aux_device_with_wipe_is_allowed() {
    log "Test: non-pristine whole WAL carrier is allowed with --wal-wipe"
    local osd_path wal_parent before_parts after_parts output osd_id wal_link wal_target

    osd_path=$(get_available_disk_path_by_size "10GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    before_parts=$(get_partition_count "$wal_parent")
    mark_disk_non_pristine "$wal_parent"

    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --wal-wipe 2>&1 || true)
    echo "$output"
    assert_output_not_contains "$output" 'Warning: WAL match expression resolved to no devices' "non-pristine WAL carrier with wipe should remain eligible"
    wait_for_configured_disk_count_ge 1 180

    after_parts=$(get_partition_count "$wal_parent")
    assert_eq "$after_parts" "$((before_parts + 1))" "non-pristine WAL carrier with wipe should gain one partition"
    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    wal_link="$(get_osd_data_dir "$osd_id")/block.wal"
    assert_path_exists_in_vm "$wal_link"
    wal_target=$(get_symlink_target "$wal_link")
    assert_output_contains "$wal_target" '/dev/' "WAL symlink should resolve to a device"
}

function test_dsl_partitioned_foreign_aux_disk_with_wipe_is_reclaimed() {
    log "Test: partitioned foreign WAL carrier is reclaimed with --wal-wipe"
    local osd_path wal_parent before_parts setup_parts after_parts dry_run_output output osd_id wal_link wal_target

    osd_path=$(get_available_disk_path_by_size "10GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    before_parts=$(get_partition_count "$wal_parent")
    create_partition_on_disk "$wal_parent" 512
    setup_parts=$(get_partition_count "$wal_parent")
    assert_eq "$setup_parts" "$((before_parts + 1))" "setup should create one foreign partition on WAL disk"

    dry_run_output=$(disk_add_dry_run_json --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --wal-wipe)
    assert_output_contains "$dry_run_output" '"warnings"' "dry-run json should include warnings field"
    assert_eq "$(jq -r '.warnings[0]' <<<"$dry_run_output")" "WAL carrier ${wal_parent} will be wiped/reset before partitioning" "dry-run json should name the reclaimed WAL carrier"
    assert_eq "$(jq -r '.dry_run_plan[0].wal.parent_path' <<<"$dry_run_output")" "$wal_parent" "dry-run json should report the reclaimed WAL parent"
    assert_eq "$(jq -r '.dry_run_plan[0].wal.reset_before_use' <<<"$dry_run_output")" "true" "dry-run json should mark the WAL carrier for reset"

    output=$(vm_exec_expect_success "partitioned foreign WAL carrier with wipe should succeed" microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --wal-wipe)
    assert_output_not_contains "$output" 'Warning: WAL match expression resolved to no devices' "partitioned foreign WAL carrier with wipe should remain eligible"
    wait_for_configured_disk_count_ge 1 180

    after_parts=$(get_partition_count "$wal_parent")
    assert_eq "$after_parts" "$((before_parts + 1))" "reclaimed WAL carrier should be reset and end with one fresh partition"
    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    wal_link="$(get_osd_data_dir "$osd_id")/block.wal"
    assert_path_exists_in_vm "$wal_link"
    wal_target=$(get_symlink_target "$wal_link")
    assert_output_contains "$wal_target" '/dev/' "WAL symlink should resolve to a device"
}

function test_dsl_whole_disk_ceph_aux_device_is_rejected() {
    log "Test: whole-disk Ceph WAL device is rejected as a DSL WAL carrier"
    local osd1_path osd2_path wal_parent first_output second_output first_osd_id second_osd_id
    local before_parts after_first_parts after_second_parts first_wal_link second_wal_link

    osd1_path=$(get_available_disk_path_by_size "10GiB")
    osd2_path=$(get_available_disk_path_by_size "11GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    before_parts=$(get_partition_count "$wal_parent")

    first_output=$(vm_exec microceph disk add "$osd1_path" --wal-device "$wal_parent" 2>&1 || true)
    echo "$first_output"
    wait_for_configured_disk_count_ge 1 180

    first_osd_id=$(get_osd_id_for_path "$osd1_path")
    [[ -n "$first_osd_id" ]] || fail "Could not resolve OSD id for $osd1_path"
    first_wal_link="$(get_osd_data_dir "$first_osd_id")/block.wal"
    assert_path_exists_in_vm "$first_wal_link"

    after_first_parts=$(get_partition_count "$wal_parent")
    assert_eq "$after_first_parts" "$before_parts" "whole-disk WAL setup must not create partitions on the WAL carrier"

    second_output=$(vm_exec microceph disk add --osd-match "eq(@size, 11GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB 2>&1 || true)
    echo "$second_output"
    assert_output_contains "$second_output" 'Warning: WAL match expression resolved to no devices' "expected warning for Ceph-owned whole-disk WAL carrier"
    wait_for_configured_disk_count_ge 2 180

    after_second_parts=$(get_partition_count "$wal_parent")
    assert_eq "$after_second_parts" "$after_first_parts" "Ceph-owned whole-disk WAL carrier must not receive a DSL WAL partition"

    second_osd_id=$(get_osd_id_for_path "$osd2_path")
    [[ -n "$second_osd_id" ]] || fail "Could not resolve OSD id for $osd2_path"
    second_wal_link="$(get_osd_data_dir "$second_osd_id")/block.wal"
    assert_path_missing_in_vm "$second_wal_link"
}

function test_dsl_encrypted_aux_carrier_is_reused_for_additional_partitions() {
    log "Test: encrypted WAL/DB carriers are reused for additional partitions"
    if ! ensure_dm_crypt_ready; then
        return 0
    fi

    local osd1_path osd2_path wal_parent db_parent first_output second_output
    local before_wal before_db after_first_wal after_first_db after_second_wal after_second_db
    local osd1_id osd2_id osd1_dir osd2_dir
    local osd1_wal_mapper osd1_db_mapper osd2_wal_mapper osd2_db_mapper
    local osd1_wal_raw osd1_db_raw osd2_wal_raw osd2_db_raw
    local osd1_wal_part osd1_db_part osd2_wal_part osd2_db_part

    osd1_path=$(get_available_disk_path_by_size "10GiB")
    osd2_path=$(get_available_disk_path_by_size "11GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    db_parent=$(get_available_disk_path_by_size "30GiB")
    before_wal=$(get_partition_count "$wal_parent")
    before_db=$(get_partition_count "$db_parent")

    first_output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --wal-encrypt --db-match "eq(@size, 30GiB)" --db-size 2GiB --db-encrypt 2>&1 || true)
    echo "$first_output"
    wait_for_configured_disk_count_ge 1 180
    after_first_wal=$(get_partition_count "$wal_parent")
    after_first_db=$(get_partition_count "$db_parent")
    assert_eq "$after_first_wal" "$((before_wal + 1))" "first encrypted run should create one WAL partition"
    assert_eq "$after_first_db" "$((before_db + 1))" "first encrypted run should create one DB partition"

    osd1_id=$(get_osd_id_for_path "$osd1_path")
    [[ -n "$osd1_id" ]] || fail "Could not resolve OSD id for $osd1_path"
    osd1_dir=$(get_osd_data_dir "$osd1_id")
    assert_path_exists_in_vm "$osd1_dir/block.wal"
    assert_path_exists_in_vm "$osd1_dir/block.db"
    assert_path_exists_in_vm "$osd1_dir/unencrypted.wal"
    assert_path_exists_in_vm "$osd1_dir/unencrypted.db"

    osd1_wal_mapper=$(get_symlink_value "$osd1_dir/block.wal")
    osd1_db_mapper=$(get_symlink_value "$osd1_dir/block.db")
    assert_output_contains "$osd1_wal_mapper" "luksosd\\.wal-${osd1_id}" "encrypted WAL link should point at the WAL mapper"
    assert_output_contains "$osd1_db_mapper" "luksosd\\.db-${osd1_id}" "encrypted DB link should point at the DB mapper"

    osd1_wal_raw=$(get_symlink_target "$osd1_dir/unencrypted.wal")
    osd1_db_raw=$(get_symlink_target "$osd1_dir/unencrypted.db")
    osd1_wal_part=$(partition_number_from_path "$osd1_wal_raw")
    osd1_db_part=$(partition_number_from_path "$osd1_db_raw")
    assert_eq "$osd1_wal_part" "1" "first encrypted WAL partition should be partition 1"
    assert_eq "$osd1_db_part" "1" "first encrypted DB partition should be partition 1"

    second_output=$(vm_exec microceph disk add --osd-match "eq(@size, 11GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --wal-encrypt --db-match "eq(@size, 30GiB)" --db-size 2GiB --db-encrypt 2>&1 || true)
    echo "$second_output"
    assert_output_not_contains "$second_output" 'Warning: WAL match expression resolved to no devices' "encrypted WAL carrier should remain reusable"
    assert_output_not_contains "$second_output" 'Warning: DB match expression resolved to no devices' "encrypted DB carrier should remain reusable"
    wait_for_configured_disk_count_ge 2 180
    after_second_wal=$(get_partition_count "$wal_parent")
    after_second_db=$(get_partition_count "$db_parent")
    assert_eq "$after_second_wal" "$((before_wal + 2))" "second encrypted run should create a second WAL partition"
    assert_eq "$after_second_db" "$((before_db + 2))" "second encrypted run should create a second DB partition"

    osd2_id=$(get_osd_id_for_path "$osd2_path")
    [[ -n "$osd2_id" ]] || fail "Could not resolve OSD id for $osd2_path"
    osd2_dir=$(get_osd_data_dir "$osd2_id")
    assert_path_exists_in_vm "$osd2_dir/block.wal"
    assert_path_exists_in_vm "$osd2_dir/block.db"
    assert_path_exists_in_vm "$osd2_dir/unencrypted.wal"
    assert_path_exists_in_vm "$osd2_dir/unencrypted.db"

    osd2_wal_mapper=$(get_symlink_value "$osd2_dir/block.wal")
    osd2_db_mapper=$(get_symlink_value "$osd2_dir/block.db")
    assert_output_contains "$osd2_wal_mapper" "luksosd\\.wal-${osd2_id}" "second encrypted WAL link should point at the WAL mapper"
    assert_output_contains "$osd2_db_mapper" "luksosd\\.db-${osd2_id}" "second encrypted DB link should point at the DB mapper"

    osd2_wal_raw=$(get_symlink_target "$osd2_dir/unencrypted.wal")
    osd2_db_raw=$(get_symlink_target "$osd2_dir/unencrypted.db")
    osd2_wal_part=$(partition_number_from_path "$osd2_wal_raw")
    osd2_db_part=$(partition_number_from_path "$osd2_db_raw")
    assert_eq "$osd2_wal_part" "2" "second encrypted WAL partition should be partition 2"
    assert_eq "$osd2_db_part" "2" "second encrypted DB partition should be partition 2"
}

function test_dsl_encrypted_whole_disk_aux_device_is_rejected() {
    log "Test: encrypted whole-disk WAL device is rejected as a DSL WAL carrier"
    if ! ensure_dm_crypt_ready; then
        return 0
    fi

    local osd1_path osd2_path wal_parent first_output second_output first_osd_id second_osd_id
    local before_parts after_first_parts after_second_parts first_osd_dir first_wal_link second_wal_link first_wal_value

    osd1_path=$(get_available_disk_path_by_size "10GiB")
    osd2_path=$(get_available_disk_path_by_size "11GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    before_parts=$(get_partition_count "$wal_parent")

    first_output=$(vm_exec microceph disk add "$osd1_path" --wal-device "$wal_parent" --wal-encrypt 2>&1 || true)
    echo "$first_output"
    wait_for_configured_disk_count_ge 1 180

    first_osd_id=$(get_osd_id_for_path "$osd1_path")
    [[ -n "$first_osd_id" ]] || fail "Could not resolve OSD id for $osd1_path"
    first_osd_dir=$(get_osd_data_dir "$first_osd_id")
    first_wal_link="$first_osd_dir/block.wal"
    assert_path_exists_in_vm "$first_wal_link"
    assert_path_exists_in_vm "$first_osd_dir/unencrypted.wal"
    assert_output_contains "$(get_symlink_value "$first_wal_link")" "luksosd\\.wal-${first_osd_id}" "encrypted whole-disk WAL link should point at the WAL mapper"
    first_wal_value=$(get_symlink_value "$first_osd_dir/unencrypted.wal")
    assert_eq "$first_wal_value" "$wal_parent" "unencrypted whole-disk WAL link should retain the stable carrier path"

    after_first_parts=$(get_partition_count "$wal_parent")
    assert_eq "$after_first_parts" "$before_parts" "encrypted whole-disk WAL setup must not create partitions on the WAL carrier"

    second_output=$(vm_exec microceph disk add --osd-match "eq(@size, 11GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB 2>&1 || true)
    echo "$second_output"
    assert_output_contains "$second_output" 'Warning: WAL match expression resolved to no devices' "expected warning for encrypted Ceph-owned whole-disk WAL carrier"
    wait_for_configured_disk_count_ge 2 180

    after_second_parts=$(get_partition_count "$wal_parent")
    assert_eq "$after_second_parts" "$after_first_parts" "encrypted whole-disk WAL carrier must not receive a DSL WAL partition"

    second_osd_id=$(get_osd_id_for_path "$osd2_path")
    [[ -n "$second_osd_id" ]] || fail "Could not resolve OSD id for $osd2_path"
    second_wal_link="$(get_osd_data_dir "$second_osd_id")/block.wal"
    assert_path_missing_in_vm "$second_wal_link"
}

function test_dsl_remove_osd_cleans_generated_aux_partitions() {
    log "Test: removing an OSD cleans generated WAL/DB partitions"
    local osd_path output osd_id osd_dir wal_target db_target manifest_path remove_output
    osd_path=$(get_available_disk_path_by_size "12GiB")
    output=$(vm_exec_expect_success "dsl WAL+DB add should succeed before remove" microceph disk add --osd-match "eq(@size, 12GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB)
    wait_for_configured_disk_count_ge 1 180

    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    osd_dir=$(get_osd_data_dir "$osd_id")
    manifest_path="$osd_dir/generated-aux-devices.json"
    wait_for_path_exists_in_vm "$manifest_path" 120
    wal_target=$(get_symlink_target "$osd_dir/block.wal")
    db_target=$(get_symlink_target "$osd_dir/block.db")

    assert_path_exists_in_vm "$wal_target"
    assert_path_exists_in_vm "$db_target"

    remove_output=$(vm_exec_expect_success "dsl OSD remove should succeed" microceph disk remove "$osd_id" --bypass-safety-checks)
    assert_output_contains "$remove_output" "Removing osd\.${osd_id}" "remove output should mention the OSD being removed"
    wait_for_configured_disk_count_eq 0 180
    wait_for_path_missing_in_vm "$manifest_path" 120
    wait_for_path_missing_in_vm "$wal_target" 120
    wait_for_path_missing_in_vm "$db_target" 120
}

function test_dsl_remove_osd_cleanup_survives_daemon_restart() {
    log "Test: generated WAL/DB cleanup survives a daemon restart"
    local osd_path output osd_id osd_dir wal_target db_target manifest_path

    osd_path=$(get_available_disk_path_by_size "12GiB")
    output=$(vm_exec_expect_success "dsl WAL+DB add should succeed before restart" microceph disk add --osd-match "eq(@size, 12GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB)
    wait_for_configured_disk_count_ge 1 180

    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    osd_dir=$(get_osd_data_dir "$osd_id")
    manifest_path="$osd_dir/generated-aux-devices.json"
    wait_for_path_exists_in_vm "$manifest_path" 120
    wal_target=$(get_symlink_target "$osd_dir/block.wal")
    db_target=$(get_symlink_target "$osd_dir/block.db")

    vm_exec_expect_success "microceph daemon restart should succeed" snap restart microceph.daemon >/dev/null
    wait_for_microceph_ready 180

    vm_exec_expect_success "dsl OSD remove after restart should succeed" microceph disk remove "$osd_id" --bypass-safety-checks >/dev/null
    wait_for_configured_disk_count_eq 0 180
    wait_for_path_missing_in_vm "$manifest_path" 120
    wait_for_path_missing_in_vm "$wal_target" 120
    wait_for_path_missing_in_vm "$db_target" 120
}

function test_dsl_remove_one_of_two_osds_only_cleans_its_partitions() {
    log "Test: removing one of two OSDs only cleans its generated WAL/DB partitions"
    local output osd1_path osd2_path osd1_id osd2_id osd1_dir osd2_dir
    local osd1_wal osd1_db osd2_wal osd2_db

    osd1_path=$(get_available_disk_path_by_size "10GiB")
    osd2_path=$(get_available_disk_path_by_size "11GiB")
    output=$(vm_exec_expect_success "two-OSD WAL+DB add should succeed" microceph disk add --osd-match "or(eq(@size, 10GiB), eq(@size, 11GiB))" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB)
    wait_for_configured_disk_count_ge 2 180

    osd1_id=$(get_osd_id_for_path "$osd1_path")
    osd2_id=$(get_osd_id_for_path "$osd2_path")
    [[ -n "$osd1_id" ]] || fail "Could not resolve OSD id for $osd1_path"
    [[ -n "$osd2_id" ]] || fail "Could not resolve OSD id for $osd2_path"

    osd1_dir=$(get_osd_data_dir "$osd1_id")
    osd2_dir=$(get_osd_data_dir "$osd2_id")
    osd1_wal=$(get_symlink_target "$osd1_dir/block.wal")
    osd1_db=$(get_symlink_target "$osd1_dir/block.db")
    osd2_wal=$(get_symlink_target "$osd2_dir/block.wal")
    osd2_db=$(get_symlink_target "$osd2_dir/block.db")

    assert_path_exists_in_vm "$osd1_wal"
    assert_path_exists_in_vm "$osd1_db"
    assert_path_exists_in_vm "$osd2_wal"
    assert_path_exists_in_vm "$osd2_db"

    vm_exec_expect_success "first OSD remove should succeed" microceph disk remove "$osd1_id" --bypass-safety-checks >/dev/null
    wait_for_configured_disk_count_eq 1 180
    wait_for_path_missing_in_vm "$osd1_wal" 120
    wait_for_path_missing_in_vm "$osd1_db" 120
    assert_path_exists_in_vm "$osd2_dir/block.wal"
    assert_path_exists_in_vm "$osd2_dir/block.db"
    assert_path_exists_in_vm "$osd2_wal"
    assert_path_exists_in_vm "$osd2_db"
}

function test_dsl_snap_contains_partition_tools() {
    log "Test: installed snap contains partition helper tools"
    assert_path_exists_in_vm "/snap/microceph/current/bin/sfdisk"
    assert_path_exists_in_vm "/snap/microceph/current/bin/partx"
    assert_path_exists_in_vm "/snap/microceph/current/bin/blockdev"
}

function test_dsl_end_to_end_matrix_from_local_snap() {
    if [[ -z "$SNAP_PATH" ]]; then
        skip_test "local-snap matrix requires --snap-path"
        return 0
    fi

    log "Test: compact add/remove matrix using local snap"
    local wal1 db1 wal2 db2 before before_wal after_wal osd_path osd_id output

    wal1=$(get_available_disk_path_by_size "20GiB")
    db1=$(get_available_disk_path_by_size "30GiB")
    wal2=$(get_available_disk_path_by_size "21GiB")
    db2=$(get_available_disk_path_by_size "31GiB")

    # WAL-only add/remove.
    osd_path=$(get_available_disk_path_by_size "10GiB")
    before_wal=$(get_partition_count "$wal1")
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 10GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB 2>&1 || true)
    echo "$output"
    wait_for_configured_disk_count_ge 1 180
    osd_id=$(get_osd_id_for_path "$osd_path")
    vm_exec microceph disk remove "$osd_id" --bypass-safety-checks
    wait_for_configured_disk_count_eq 0 180
    assert_eq "$(get_partition_count "$wal1")" "$before_wal" "WAL-only remove should restore WAL carrier partition count"

    # DB-only add/remove.
    osd_path=$(get_available_disk_path_by_size "11GiB")
    before=$(get_partition_count "$db1")
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 11GiB)" --db-match "eq(@size, 30GiB)" --db-size 2GiB 2>&1 || true)
    echo "$output"
    wait_for_configured_disk_count_ge 1 180
    osd_id=$(get_osd_id_for_path "$osd_path")
    vm_exec microceph disk remove "$osd_id" --bypass-safety-checks
    wait_for_configured_disk_count_eq 0 180
    assert_eq "$(get_partition_count "$db1")" "$before" "DB-only remove should restore DB carrier partition count"

    # WAL+DB add/remove.
    osd_path=$(get_available_disk_path_by_size "12GiB")
    before_wal=$(get_partition_count "$wal2")
    before=$(get_partition_count "$db2")
    output=$(vm_exec microceph disk add --osd-match "eq(@size, 12GiB)" --wal-match "eq(@size, 21GiB)" --wal-size 1GiB --db-match "eq(@size, 31GiB)" --db-size 2GiB 2>&1 || true)
    echo "$output"
    wait_for_configured_disk_count_ge 1 180
    osd_id=$(get_osd_id_for_path "$osd_path")
    vm_exec microceph disk remove "$osd_id" --bypass-safety-checks
    wait_for_configured_disk_count_eq 0 180
    after_wal=$(get_partition_count "$wal2")
    assert_eq "$after_wal" "$before_wal" "WAL+DB remove should restore WAL carrier partition count"
    assert_eq "$(get_partition_count "$db2")" "$before" "WAL+DB remove should restore DB carrier partition count"
}

function test_dsl_dryrun_and_execute_consistency() {
    log "Test: dry-run partition plan matches executed layout"
    local osd_path wal_parent db_parent output execute_output osd_id osd_dir wal_target db_target
    local planned_wal_part planned_db_part actual_wal_part actual_db_part before_wal before_db after_wal after_db

    osd_path=$(get_available_disk_path_by_size "12GiB")
    wal_parent=$(get_available_disk_path_by_size "20GiB")
    db_parent=$(get_available_disk_path_by_size "30GiB")
    before_wal=$(get_partition_count "$wal_parent")
    before_db=$(get_partition_count "$db_parent")
    log_available_disks_snapshot
    log_available_disk_matches_by_sizes "OSD candidate" "12GiB"
    log_available_disk_matches_by_sizes "WAL candidate" "20GiB"
    log_available_disk_matches_by_sizes "DB candidate" "30GiB"

    output=$(disk_add_dry_run_json --osd-match "eq(@size, 12GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB)
    assert_eq "$(jq -r '.dry_run_plan[0].osd_path' <<<"$output")" "$osd_path" "dry-run json should include planned OSD path"
    assert_eq "$(jq -r '.dry_run_plan[0].wal.parent_path' <<<"$output")" "$wal_parent" "dry-run json should include WAL parent path"
    assert_eq "$(jq -r '.dry_run_plan[0].db.parent_path' <<<"$output")" "$db_parent" "dry-run json should include DB parent path"
    planned_wal_part=$(jq -r '.dry_run_plan[0].wal.partition' <<<"$output")
    planned_db_part=$(jq -r '.dry_run_plan[0].db.partition' <<<"$output")
    [[ -n "$planned_wal_part" && "$planned_wal_part" != "null" ]] || fail "Could not parse planned WAL partition number from json dry-run"
    [[ -n "$planned_db_part" && "$planned_db_part" != "null" ]] || fail "Could not parse planned DB partition number from json dry-run"

    execute_output=$(vm_exec_expect_success "dsl add should match dry-run plan" microceph disk add --osd-match "eq(@size, 12GiB)" --wal-match "eq(@size, 20GiB)" --wal-size 1GiB --db-match "eq(@size, 30GiB)" --db-size 2GiB)
    wait_for_configured_disk_count_ge 1 180
    after_wal=$(get_partition_count "$wal_parent")
    after_db=$(get_partition_count "$db_parent")
    assert_eq "$after_wal" "$((before_wal + 1))" "execute should create one WAL partition"
    assert_eq "$after_db" "$((before_db + 1))" "execute should create one DB partition"

    osd_id=$(get_osd_id_for_path "$osd_path")
    [[ -n "$osd_id" ]] || fail "Could not resolve OSD id for $osd_path"
    osd_dir=$(get_osd_data_dir "$osd_id")
    wal_target=$(get_symlink_target "$osd_dir/block.wal")
    db_target=$(get_symlink_target "$osd_dir/block.db")
    actual_wal_part=$(partition_number_from_path "$wal_target")
    actual_db_part=$(partition_number_from_path "$db_target")
    assert_eq "$actual_wal_part" "$planned_wal_part" "executed WAL partition should match dry-run plan"
    assert_eq "$actual_db_part" "$planned_db_part" "executed DB partition should match dry-run plan"
}

# Status/debug helpers -------------------------------------------------------

function show_dsl_final_status() {
    log "=== Final Cluster Status ==="
    vm_exec microceph status || true
    vm_exec microceph disk list --json || true
    vm_exec lsblk -o NAME,PATH,TYPE,SIZE,RO || true
}

# Suite bodies ---------------------------------------------------------------

function dsl_suite_title() {
    case "$1" in
        baseline) echo "baseline OSD DSL tests" ;;
        waldb_validation) echo "WAL/DB validation DSL tests" ;;
        waldb_dryrun) echo "WAL/DB dry-run DSL tests" ;;
        waldb_provision) echo "WAL/DB provisioning DSL tests" ;;
        waldb_cleanup) echo "WAL/DB cleanup DSL tests" ;;
        waldb_consistency) echo "WAL/DB consistency DSL tests" ;;
        *) fail "Unknown DSL suite: $1" ;;
    esac
}

function dsl_suite_mode() {
    case "$1" in
        baseline|waldb_validation|waldb_dryrun) echo "shared" ;;
        waldb_provision|waldb_cleanup|waldb_consistency) echo "isolated" ;;
        *) fail "Unknown DSL suite: $1" ;;
    esac
}

function dsl_suite_shared_tests() {
    case "$1" in
        baseline)
            cat <<'EOF'
test_dsl_disk_list
test_dsl_available_disks
test_dsl_type_match
test_dsl_size_comparison
test_dsl_combined_conditions
test_dsl_no_match
test_dsl_invalid_expression
test_dsl_mutual_exclusivity
test_dsl_add_disk
test_dsl_idempotency
test_dsl_pristine_check
test_dsl_pristine_with_wipe
EOF
            ;;
        waldb_validation)
            cat <<'EOF'
test_dsl_disk_list
test_dsl_available_disks
test_dsl_type_match
test_dsl_size_comparison
test_dsl_combined_conditions
test_dsl_no_match
test_dsl_invalid_expression
test_dsl_mutual_exclusivity
test_dsl_add_disk
test_dsl_idempotency
test_dsl_pristine_check
test_dsl_pristine_with_wipe
test_dsl_readonly_disk_excluded
test_dsl_waldb_flag_validation
EOF
            ;;
        waldb_dryrun)
            cat <<'EOF'
test_dsl_disk_list
test_dsl_available_disks
test_dsl_type_match
test_dsl_size_comparison
test_dsl_combined_conditions
test_dsl_no_match
test_dsl_invalid_expression
test_dsl_mutual_exclusivity
test_dsl_readonly_disk_excluded
test_dsl_waldb_flag_validation
test_dsl_dryrun_wal_only_plan
test_dsl_dryrun_db_only_plan
test_dsl_dryrun_waldb_plan
test_dsl_dryrun_deterministic_order
test_dsl_dryrun_overlap_error
test_dsl_dryrun_capacity_error
test_dsl_dryrun_empty_wal_warning
test_dsl_dryrun_empty_db_warning
test_dsl_dryrun_no_new_osd_warning
EOF
            ;;
        *) fail "Suite '$1' is not a shared suite" ;;
    esac
}

function dsl_suite_isolated_cases() {
    case "$1" in
        waldb_provision)
            cat <<'EOF'
w1 test_dsl_add_wal_only
d1 test_dsl_add_db_only
wd test_dsl_add_waldb
ew test_dsl_empty_wal_match_warns_and_adds_data_only
ed test_dsl_empty_db_match_warns_and_adds_data_only
rr test_dsl_waldb_idempotent_rerun
ds test_dsl_waldb_distribution_across_multiple_aux_disks
EOF
            ;;
        waldb_cleanup)
            cat <<'EOF'
rm1 test_dsl_remove_osd_cleans_generated_aux_partitions
rmr test_dsl_remove_osd_cleanup_survives_daemon_restart
rm2 test_dsl_remove_one_of_two_osds_only_cleans_its_partitions
EOF
            ;;
        waldb_consistency)
            cat <<'EOF'
t1 test_dsl_snap_contains_partition_tools
p1 test_dsl_partitioned_non_ceph_aux_disk_is_rejected
np1 test_dsl_non_pristine_whole_aux_device_requires_wipe
np2 test_dsl_non_pristine_whole_aux_device_with_wipe_is_allowed
pf1 test_dsl_partitioned_foreign_aux_disk_with_wipe_is_reclaimed
w1 test_dsl_whole_disk_ceph_aux_device_is_rejected
ew1 test_dsl_encrypted_whole_disk_aux_device_is_rejected
er1 test_dsl_encrypted_aux_carrier_is_reused_for_additional_partitions
m1 test_dsl_end_to_end_matrix_from_local_snap
c1 test_dsl_dryrun_and_execute_consistency
EOF
            ;;
        *) fail "Suite '$1' is not an isolated suite" ;;
    esac
}

function run_requested_single_test() {
    if [[ -z "$REQUESTED_FUNCTION" ]]; then
        fail "REQUESTED_FUNCTION is not set"
    fi
    dsl_run_test "$REQUESTED_FUNCTION"
}

function run_dsl_single_test() {
    (
        trap cleanup_dsl_test EXIT

        dsl_reset_test_tracking
        dsl_set_suite_context "single test: $REQUESTED_FUNCTION"
        dsl_plan_test "$REQUESTED_FUNCTION"

        setup_dsl_test
        install_microceph_in_vm
        run_requested_single_test
        show_dsl_final_status
    )
}

function run_dsl_shared_suite() {
    local suite_name="$1"
    local title
    title=$(dsl_suite_title "$suite_name")

    (
        trap cleanup_dsl_test EXIT

        dsl_reset_test_tracking
        dsl_set_suite_context "suite: $suite_name"
        while read -r test_function; do
            [[ -n "$test_function" ]] || continue
            dsl_plan_test "$test_function"
        done < <(dsl_suite_shared_tests "$suite_name")

        setup_dsl_test
        install_microceph_in_vm

        log "=== Running $title ==="
        while read -r test_function; do
            [[ -n "$test_function" ]] || continue
            dsl_run_test "$test_function"
        done < <(dsl_suite_shared_tests "$suite_name")

        show_dsl_final_status
    )
}

function run_dsl_case() {
    local case_name="$1"
    local test_function="$2"
    local result_file="${3:-}"
    local script_path case_vm_name
    script_path=$(readlink -f "${BASH_SOURCE[0]}")
    case_vm_name="${VM_NAME}-${case_name}x"

    log "=== Running isolated case '$case_name' ($test_function) ==="
    log "Isolated case VM name: $case_vm_name"

    local cmd=("$script_path" "--vm-name" "$case_vm_name" "--storage-pool" "$STORAGE_POOL")
    if [[ -n "$SNAP_PATH" ]]; then
        cmd+=("--snap-path" "$SNAP_PATH")
    else
        cmd+=("--snap-channel" "$SNAP_CHANNEL")
    fi
    if [[ "$NO_CLEANUP" == "1" ]]; then
        cmd+=("--no-cleanup")
    fi
    cmd+=("$test_function")

    if [[ -n "$result_file" ]]; then
        env DSL_RESULT_FILE="$result_file" DSL_SUPPRESS_TEST_SUMMARY=1 "${cmd[@]}" </dev/null
    else
        "${cmd[@]}" </dev/null
    fi
}

function run_dsl_isolated_suite() {
    local suite_name="$1"
    local title
    local failures=0
    title=$(dsl_suite_title "$suite_name")

    dsl_reset_test_tracking
    dsl_set_suite_context "suite: $suite_name"
    while read -r case_name test_function; do
        [[ -n "$case_name" ]] || continue
        dsl_plan_test "$case_name" "$case_name: $test_function"
    done < <(dsl_suite_isolated_cases "$suite_name")

    log "=== Running $title ==="
    while read -r case_name test_function; do
        local status output result_file child_status child_key child_label child_detail summary_label

        [[ -n "$case_name" ]] || continue
        summary_label="$case_name: $test_function"
        result_file=$(mktemp)

        run_and_capture status output run_dsl_case "$case_name" "$test_function" "$result_file"
        if [[ -n "$output" ]]; then
            echo "$output"
        fi

        child_status=""
        child_key="$case_name"
        child_label="$summary_label"
        child_detail=""
        if [[ -s "$result_file" ]]; then
            IFS=$'\t' read -r child_status child_key child_label child_detail < "$result_file"
        fi
        rm -f "$result_file"

        if [[ -z "$child_status" ]]; then
            if [[ "$status" == "0" ]]; then
                child_status="PASS"
            else
                child_status="FAIL"
                child_detail="case exited with status $status before reporting a result"
            fi
        fi

        dsl_add_test_result "$case_name" "$summary_label" "$child_status" "$child_detail"
        if [[ -n "$child_detail" ]]; then
            log "CASE RESULT: $child_status $summary_label -- $child_detail"
        else
            log "CASE RESULT: $child_status $summary_label"
        fi

        if [[ "$child_status" == "FAIL" || "$status" != "0" ]]; then
            failures=$((failures + 1))
        fi
    done < <(dsl_suite_isolated_cases "$suite_name")

    dsl_print_test_summary

    if (( failures > 0 )); then
        return 1
    fi
}

function run_dsl_suite_by_name() {
    local suite_name="$1"
    local mode
    mode=$(dsl_suite_mode "$suite_name")

    case "$mode" in
        shared) run_dsl_shared_suite "$suite_name" ;;
        isolated) run_dsl_isolated_suite "$suite_name" ;;
        *) fail "Unsupported suite mode '$mode' for suite '$suite_name'" ;;
    esac
}

# Public entrypoints ---------------------------------------------------------

function run_dsl_baseline_tests() {
    run_dsl_suite_by_name baseline
}

function run_dsl_waldb_validation_tests() {
    run_dsl_suite_by_name waldb_validation
}

function run_dsl_waldb_dryrun_tests() {
    run_dsl_suite_by_name waldb_dryrun
}

function run_dsl_waldb_provision_tests() {
    run_dsl_suite_by_name waldb_provision
}

function run_dsl_waldb_cleanup_tests() {
    run_dsl_suite_by_name waldb_cleanup
}

function run_dsl_waldb_consistency_tests() {
    run_dsl_suite_by_name waldb_consistency
}

# Backward-compatible aliases.
function run_dsl_phase1_tests() {
    run_dsl_baseline_tests
}

function run_dsl_pr1_tests() {
    run_dsl_waldb_validation_tests
}

function run_dsl_pr2_tests() {
    run_dsl_waldb_dryrun_tests
}

function run_dsl_pr3_tests() {
    run_dsl_waldb_provision_tests
}

function run_dsl_pr4_tests() {
    run_dsl_waldb_cleanup_tests
}

function run_dsl_pr5_tests() {
    run_dsl_waldb_consistency_tests
}

function run_dsl_full_tests() {
    run_dsl_baseline_tests
    run_dsl_waldb_validation_tests
    run_dsl_waldb_dryrun_tests
    run_dsl_waldb_provision_tests
    run_dsl_waldb_cleanup_tests
    run_dsl_waldb_consistency_tests
}

function run_dsl_functest() {
    run_dsl_full_tests
}

# Parse command line arguments for standalone execution
function parse_dsl_args() {
    local requested="run_dsl_functest"

    while [[ $# -gt 0 ]]; do
        case $1 in
            --vm-name)
                VM_NAME="$2"
                OSD1_NAME="${VM_NAME}-osd1"
                OSD2_NAME="${VM_NAME}-osd2"
                OSD3_NAME="${VM_NAME}-osd3"
                WAL1_NAME="${VM_NAME}-wal1"
                WAL2_NAME="${VM_NAME}-wal2"
                DB1_NAME="${VM_NAME}-db1"
                DB2_NAME="${VM_NAME}-db2"
                RO1_NAME="${VM_NAME}-ro1"
                shift 2
                ;;
            --snap-path)
                SNAP_PATH="$2"
                shift 2
                ;;
            --snap-channel)
                SNAP_CHANNEL="$2"
                shift 2
                ;;
            --storage-pool)
                STORAGE_POOL="$2"
                shift 2
                ;;
            --no-cleanup)
                NO_CLEANUP=1
                shift
                ;;
            --help)
                cat <<EOF
Usage: $0 [OPTIONS] [FUNCTION]

Options:
  --vm-name NAME       Name for the test VM (default: microceph-dsl-test)
  --snap-path PATH     Path or glob for local snap file to install
  --snap-channel CHAN  Snap channel to install from (default: latest/edge)
  --storage-pool POOL  LXD storage pool to use (default: default)
  --no-cleanup         Keep VM and volumes on exit
  --help               Show this help message

Environment overrides:
  LXD_VM_IMAGE         Local image fingerprint/alias to use for VM launch

Primary suites:
  run_dsl_baseline_tests           Run the baseline DSL functest suite
  run_dsl_waldb_validation_tests   Run WAL/DB validation-focused DSL tests
  run_dsl_waldb_dryrun_tests       Run WAL/DB dry-run planning DSL tests
  run_dsl_waldb_provision_tests    Run WAL/DB provisioning DSL tests
  run_dsl_waldb_cleanup_tests      Run WAL/DB cleanup DSL tests
  run_dsl_waldb_consistency_tests  Run WAL/DB consistency DSL tests
  run_dsl_full_tests               Run the full DSL functest matrix

Legacy aliases:
  run_dsl_phase1_tests
  run_dsl_pr1_tests .. run_dsl_pr5_tests

Other callable functions:
  test_dsl_*                       Run an individual test inside a fresh DSL test VM
EOF
                exit 0
                ;;
            *)
                requested="$1"
                shift
                break
                ;;
        esac
    done

    if ! declare -F "$requested" >/dev/null; then
        fail "Unknown function: $requested"
    fi

    if [[ "$requested" == test_dsl_* ]]; then
        REQUESTED_FUNCTION="$requested"
        run_dsl_single_test
    else
        "$requested" "$@"
    fi
}

# Entry point - if script is run directly (not sourced)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    parse_dsl_args "$@"
fi
