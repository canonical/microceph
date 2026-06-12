*** Settings ***
Documentation    wiping-test
...    Tests MicroCeph pristine disk check: creates dirty disks via an OSD add/remove
...    cycle, removes the snap, reinstalls, then verifies dirty disks are rejected
...    and can be successfully added with --wipe.
...
...    All operations run inside an outer LXD VM via actionutils.sh verify_pristine_check.
...    The script is streamed so snap remove/reinstall output is visible in real time
...    and no per-command Robot timeout can cut off a slow snap purge.
Resource        ../resources/microceph_harness.resource
Suite Setup     Wiping Tests Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    disk-management    pristine    lxd    integration

*** Keywords ***
Wiping Tests Suite Setup
    [Documentation]    Launches the outer VM, copies scripts and snap into it.
    Launch Outer Test VM    vm_name=microceph-wipe-vm
    Copy Scripts To VM
    Copy Snap To VM
    Free Runner Disk

*** Test Cases ***
Test Pristine Disk Check
    [Documentation]    Full pristine-check cycle inside the outer VM via
    ...    actionutils.sh verify_pristine_check: creates loop-backed dirty disks,
    ...    removes and reinstalls microceph, verifies dirty disks are rejected without
    ...    --wipe, then adds them successfully with --wipe.
    ...
    ...    Runs as a single command so snap remove/reinstall are not subject to
    ...    per-step Robot timeouts; streaming shows progress in real time.
    ...    Pass --variable XTRACE:True to trace the script with bash -x.
    [Tags]    disk-management    pristine
    ${rc}    ${out}=    Run Script In VM With Trace    /root/actionutils.sh
    ...    verify_pristine_check    timeout=3600
    Log    ${out}
    Should Be Equal As Integers    ${rc}    0    msg=verify_pristine_check failed (rc=${rc})
