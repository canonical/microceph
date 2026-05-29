*** Settings ***
Documentation    Translated from: .github/workflows/tests.yml — dsl-functional-tests
...    Runs test_dsl_functest.sh directly on the host runner (no outer VM).
...    The script creates its own KVM VMs internally; nesting KVM inside an LXD
...    VM makes the inner agent unreachable, so we must run on bare metal.
...
...    The full suite is split into six named sub-suites that map 1-to-1 with
...    the public entrypoints in test_dsl_functest.sh. Each sub-suite can be
...    selected individually via --test "<name>" for parallel CI execution.
Resource        ../resources/microceph_harness.resource
Library         ../resources/streaming_process.py
Suite Setup     DSL Functional Tests Suite Setup
Test Tags       dsl    functional    lxd    slow    integration

*** Variables ***
${XTRACE}       ${False}

*** Keywords ***
DSL Functional Tests Suite Setup
    [Documentation]    Checks that lxc is available on the host (the DSL script creates
    ...    its own KVM VMs using lxc), copies actionutils.sh to ~/,
    ...    and marks the test script executable. No outer VM is launched here.
    Require Host Commands    lxc
    Log To Console    [setup] DSL tests run on host — no outer VM (KVM nesting not available).
    ${prep}=    Run Process
    ...    bash -c "cp ${REPO_ROOT}/tests/scripts/actionutils.sh ~/actionutils.sh && chmod +x ~/actionutils.sh ${REPO_ROOT}/tests/scripts/test_dsl_functest.sh"
    ...    shell=True    timeout=60
    Should Be Equal As Integers    ${prep.rc}    0    msg=DSL host prep failed: ${prep.stderr}

Run DSL Suite
    [Documentation]    Invokes test_dsl_functest.sh with the given entrypoint function.
    ...    Output is streamed to the console in real time. Pass --variable XTRACE:True
    ...    to enable bash -x tracing.
    [Arguments]    ${entrypoint}
    ${cmd}=    Set Variable
    ...    ${REPO_ROOT}/tests/scripts/test_dsl_functest.sh --snap-path ${SNAP_PATH} ${entrypoint}
    ${rc}    ${out}=    Run Streaming Process    ${cmd}    timeout=14400    xtrace=${XTRACE}
    Log    ${out}
    Should Be Equal As Integers    ${rc}    0
    ...    msg=DSL suite '${entrypoint}' failed (rc=${rc})

*** Test Cases ***
Run DSL Baseline Tests
    [Documentation]    Baseline OSD DSL coverage: disk-list, type/size matching, add, idempotency, pristine.
    ...    Runs on a single shared VM via run_dsl_baseline_tests.
    [Tags]    dsl    baseline
    Run DSL Suite    run_dsl_baseline_tests

Run DSL WAL-DB Validation Tests
    [Documentation]    WAL/DB flag validation and readonly-disk exclusion on a shared VM.
    ...    Runs via run_dsl_waldb_validation_tests.
    [Tags]    dsl    waldb    validation
    Run DSL Suite    run_dsl_waldb_validation_tests

Run DSL WAL-DB Dry-Run Tests
    [Documentation]    WAL/DB dry-run planning tests: plan output, determinism, error cases.
    ...    Runs via run_dsl_waldb_dryrun_tests.
    [Tags]    dsl    waldb    dryrun
    Run DSL Suite    run_dsl_waldb_dryrun_tests

Run DSL WAL-DB Provision Tests
    [Documentation]    WAL/DB provisioning tests, each isolated in its own VM.
    ...    Runs via run_dsl_waldb_provision_tests.
    [Tags]    dsl    waldb    provision
    Run DSL Suite    run_dsl_waldb_provision_tests

Run DSL WAL-DB Cleanup Tests
    [Documentation]    OSD removal cleans up generated WAL/DB partitions; each case isolated.
    ...    Runs via run_dsl_waldb_cleanup_tests.
    [Tags]    dsl    waldb    cleanup
    Run DSL Suite    run_dsl_waldb_cleanup_tests

Run DSL WAL-DB Consistency Tests
    [Documentation]    Partition-tool presence, pristine checks, encryption, end-to-end matrix.
    ...    Each case runs in its own isolated VM via run_dsl_waldb_consistency_tests.
    [Tags]    dsl    waldb    consistency
    Run DSL Suite    run_dsl_waldb_consistency_tests
