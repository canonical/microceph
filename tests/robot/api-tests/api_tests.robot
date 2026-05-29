*** Settings ***
Documentation    Translated from: .github/workflows/tests.yml — api-tests
...    Tests the MicroCeph REST API via Hurl: services-mon, maintenance-put-failed,
...    disks-list, disk encryption-support endpoints, and the disk API with discoverable
...    devices (via DSL).
...
...    Tests 1-3 run via an outer LXD VM. Test 4 (disk API with discoverable devices)
...    runs test_dsl_functest.sh directly on the host runner — the DSL script creates
...    its own KVM VM; nesting KVM inside the outer VM makes the inner agent unreachable.
Resource        ../resources/microceph_harness.resource
Library         ../resources/streaming_process.py
Suite Setup     API Tests Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    api    hurl    lxd    integration

*** Variables ***
${XTRACE}       ${False}

*** Keywords ***
API Tests Suite Setup
    Launch Outer Test VM    vm_name=microceph-api-vm
    Copy Scripts To VM
    Copy DSL Test Script To VM
    Copy Hurl Files To VM
    Copy Snap To VM
    Free Runner Disk
    Install And Bootstrap MicroCeph

*** Test Cases ***
Test Services Mon API
    [Documentation]    Verifies the /services/mon endpoint via Hurl.
    [Tags]    api    hurl
    Install Hurl
    Prepare Disk API Hurl Fixtures
    Run Hurl    tests/hurl/services-mon.hurl

Test Maintenance Put Failed API
    [Documentation]    Verifies that a maintenance PUT request fails as expected via Hurl.
    [Tags]    api    hurl
    Run Hurl    tests/hurl/maintenance-put-failed.hurl

Test Disks List API
    [Documentation]    Verifies the /disks endpoint via Hurl.
    [Tags]    api    hurl
    Run Hurl    tests/hurl/disks-list.hurl

Test Disk Encryption Support Unsupported
    [Documentation]    Verifies the /1.0/disks/encryption-support endpoint returns
    ...    supported=false before dm-crypt is connected (dangerous-install has no auto-connect).
    [Tags]    api    hurl    encryption
    Run Hurl    tests/hurl/disks-encryption-support-unsupported.hurl

Test Disk Encryption Support Supported
    [Documentation]    Connects the dm-crypt snap interface and loads the dm_crypt kernel module,
    ...    then verifies the /1.0/disks/encryption-support endpoint returns supported=true.
    ...    Mirrors the "API disk encryption-support (supported)" CI step added in 00d313c.
    [Tags]    api    hurl    encryption
    Run In VM And Check    sudo snap connect microceph:dm-crypt    30
    Run In VM And Check    sudo modprobe dm_crypt    30
    Run Hurl    tests/hurl/disks-encryption-support-supported.hurl

Test Disk API With Discoverable Devices
    [Documentation]    Tests the disk add/remove API with loop devices that have proper device
    ...    paths (discoverable) via the DSL functional test test_dsl_api_disk_hurl.
    ...    Runs test_dsl_functest.sh directly on the host runner (no nested VM) — the DSL
    ...    script creates its own KVM VM on host LXD, which is already initialised.
    [Tags]    api    hurl    dsl    lxd
    ${snap_arg}=    Set Variable If    "${SNAP_PATH}" != "${EMPTY}"    --snap-path ${SNAP_PATH}    ${EMPTY}
    ${cmd}=    Set Variable
    ...    ${REPO_ROOT}/tests/scripts/test_dsl_functest.sh ${snap_arg} test_dsl_api_disk_hurl
    ${rc}    ${out}=    Run Streaming Process    ${cmd}    timeout=1800    xtrace=${XTRACE}
    Log    ${out}
    Should Be Equal As Integers    ${rc}    0
    ...    msg=test_dsl_api_disk_hurl failed (rc=${rc})
