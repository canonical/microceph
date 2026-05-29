*** Settings ***
Documentation    Translated from: .github/workflows/tests.yml — single-system-tests
...    Single-node MicroCeph with encryption, LVM, RGW (plain and SSL), certificate rotation,
...    cluster config, pool replication, log control, IPv6 mon-ip, and snap disable/enable.
Resource        ../resources/microceph_harness.resource
Suite Setup     Single System Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    osd    rgw    mon    mgr    disk-management    snap-packaging    waitready    e2e    integration    lxd    loop-devices    slow

*** Keywords ***
Single System Suite Setup
    Launch Outer Test VM    vm_name=microceph-ss-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools

Test Waitready Inline
    [Documentation]    Installs snap, verifies waitready fails before bootstrap,
    ...    bootstraps, then verifies waitready passes after bootstrap.
    Log To Console    [waitready] Installing snap and testing waitready lifecycle...
    Install MicroCeph From Local Snap
    Run In VM Must Fail    sudo microceph waitready --timeout 5 2>/dev/null
    Bootstrap MicroCeph Cluster
    Run In VM And Check    sudo microceph waitready --timeout 30    60

*** Test Cases ***
Test Waitready
    [Documentation]    Installs snap, verifies waitready fails before bootstrap, bootstraps,
    ...    then verifies waitready passes after bootstrap.
    [Tags]    waitready
    Test Waitready Inline

Verify Post-Bootstrap State
    [Documentation]    Checks metadata.yaml contains ceph-version, verifies OSD pool crush rule.
    [Tags]    waitready
    Run In VM And Check    grep -q ceph-version /var/snap/microceph/current/conf/metadata.yaml    30
    Run In VM And Check    sudo microceph.ceph health | grep -q "OSD count 0 < osd_pool_default_size 3"    30
    Run In VM And Check    sudo microceph.ceph osd crush rule ls | grep -F microceph_auto_osd    30

Test Waitready Storage Insufficient
    [Documentation]    Verifies waitready --storage fails when no OSDs are present.
    [Tags]    waitready
    Run In VM Must Fail    sudo microceph waitready --storage --timeout 1 2>/dev/null

Test Orchestrator Module
    [Documentation]    Enables the MicroCeph orchestrator module and verifies host/service listing.
    [Tags]    mgr
    Run In VM And Check    sudo microceph.ceph mgr module ls | grep -F "microceph"    30
    Run In VM And Check    sudo microceph.ceph mgr module enable microceph    30
    Run In VM And Check    sudo microceph.ceph orch set backend microceph    30
    ${hn}=    Run In VM    hostname
    ${hn_str}=    Strip String    ${hn.stdout}
    Run In VM And Check    sudo microceph.ceph orch host ls | grep -F ${hn_str}    30
    Run In VM And Check    sudo microceph.ceph orch ls | grep -F "mon" | grep -F ${hn_str}    30
    Run In VM And Check    sudo microceph.ceph orch ls | grep -F "mds" | grep -F ${hn_str}    30
    Run In VM And Check    sudo microceph.ceph orch ls | grep -F "mgr" | grep -F ${hn_str}    30

Add OSD With Failure
    [Documentation]    Verifies adding an encrypted OSD fails without dm-crypt,
    ...    and batch disk add fails with wal/db device flags.
    [Tags]    osd    disk-management
    ${lf_result}=    Run In VM    mktemp /tmp/mctestXXXXXX    30
    ${lf}=    Set Variable    ${lf_result.stdout.strip()}
    Run In VM And Check    sudo truncate -s 1G ${lf}    30
    ${ld_result}=    Run In VM    sudo losetup --show -f ${lf}    30
    ${ld}=    Set Variable    ${ld_result.stdout.strip()}
    ${minor}=    Evaluate    '${ld}'.replace('/dev/loop', '')
    Run In VM And Check    sudo mknod -m 0660 /dev/sdi21 b 7 ${minor}    30
    ${r}=    Run In VM    sudo microceph disk add --wipe /dev/sdi21 --encrypt 2>&1 | grep -c Failure    30
    Should Not Be Equal As Strings    ${r.stdout.strip()}    0    msg=FDE should fail without dm-crypt
    ${r2}=    Run In VM    sudo microceph disk add /dev/sdi21 /dev/sdi22 --wal-device /dev/sdi23 2>&1 | grep -c "not supported for batch disk addition"    30
    Should Not Be Equal As Strings    ${r2.stdout.strip()}    0    msg=Batch add with wal should fail
    ${r3}=    Run In VM    sudo microceph disk add /dev/sdi21 /dev/sdi22 --db-device /dev/sdi23 2>&1 | grep -c "not supported for batch disk addition"    30
    Should Not Be Equal As Strings    ${r3.stdout.strip()}    0    msg=Batch add with db should fail

Test Verify Mount Check
    [Documentation]    Verifies adding a mounted device fails even with --wipe.
    [Tags]    osd    disk-management
    Verify Mount Check

Test Encrypted OSDs With DM Crypt
    [Documentation]    Enables dm-crypt, creates loop devices, adds two encrypted OSDs,
    ...    and verifies they appear in disk list.
    [Tags]    osd    disk-management
    Add Encrypted OSDs

Test LVM Volume OSD
    [Documentation]    Creates an LVM logical volume and adds it as an OSD.
    [Tags]    osd    disk-management
    Add LVM Volume OSD

Test Waitready Storage After OSDs
    [Documentation]    Verifies waitready --storage passes once sufficient OSDs are present.
    [Tags]    waitready
    Run In VM And Check    sudo microceph waitready --storage --timeout 30    60

Test Enable RGW
    [Documentation]    Enables Rados Gateway and waits for the daemon to become active.
    [Tags]    rgw
    Enable RGW

Test Cluster Shows One Monitor Daemon
    [Documentation]    Polls ceph status until it shows exactly 1 monitor daemon running.
    [Tags]    osd    rgw    mon
    Run In VM And Check    sudo microceph disk list    30
    Poll Ceph Status Contains    mon: 1 daemons

Test Cluster Shows Active Manager
    [Documentation]    Polls ceph status until it shows an active manager.
    [Tags]    mgr
    Poll Ceph Status Contains    (active, since

Test Cluster Shows Three OSDs
    [Documentation]    Polls ceph status until it shows 3 OSDs in/up.
    [Tags]    osd
    Poll Ceph Status Contains    osd: 3 osds

Test Cluster Shows One RGW Daemon
    [Documentation]    Polls ceph status until it shows 1 RGW daemon running.
    [Tags]    rgw
    Poll Ceph Status Contains    rgw: 1 daemon

Test Cluster Restart Verify
    [Documentation]    Stops and starts the microceph snap, then polls until all four daemon types
    ...    (mon, mgr, osd, rgw) are visible in ceph status and ceph-osd is running.
    ...    Mirrors the "Check health after restart" block in the old bash "Run system tests" step.
    [Tags]    osd    rgw    mon    mgr
    Run In VM And Check    sudo snap stop microceph    120
    Run In VM And Check    sudo snap start microceph    60
    FOR    ${i}    IN RANGE    16
        ${result}=    Run In VM    sudo microceph.ceph status    30
        Log    Attempt ${i}: ${result.stdout}
        IF    "mon: 1 daemons" in $result.stdout and "(active, since" in $result.stdout and "osd: 3 osds" in $result.stdout and "rgw: 1 daemon" in $result.stdout
            Log To Console    [status] PASS: all daemons visible after restart (attempt ${i})
            BREAK
        END
        IF    ${i} == 15
            Fail    Cluster never came back fully after snap restart
        END
        Sleep    15s
    END
    FOR    ${i}    IN RANGE    10
        ${pgrep}=    Run In VM    pgrep ceph-osd    10
        IF    ${pgrep.rc} == 0
            Log To Console    [status] ceph-osd process found
            BREAK
        END
        IF    ${i} == 9    Fail    ceph-osd process never appeared after snap restart
        Sleep    2s
    END

Test Snap Disable Enable Service Restoration
    [Documentation]    Disables then re-enables the snap and verifies all services restart.
    [Tags]    snap-packaging
    Test Snap Disable Enable

Test Exercise RGW
    [Documentation]    Creates S3 user, uploads file, and verifies public URL via curl.
    [Tags]    rgw
    Exercise RGW

Test RGW Stale Run Dir Migration
    [Documentation]    Injects a stale run dir into radosgw.conf and verifies the daemon repairs it.
    [Tags]    rgw
    Test RGW Stale Run Dir Migration

Test RGW SSL
    [Documentation]    Disables plain RGW, enables SSL RGW, and exercises S3 over HTTPS.
    [Tags]    rgw
    Disable RGW
    Enable RGW SSL
    Exercise RGW SSL

Test SSL Certificate Rotation
    [Documentation]    Rotates the RGW SSL certificate with --restart and without, verifying both.
    [Tags]    rgw
    Test SSL Certificate Rotation

Test SSL Certificate Rotation With Target Self
    [Documentation]    Rotates the RGW SSL certificate using --target (self) via test_certificate_set_rgw_target.
    [Tags]    rgw
    ${hn}=    Run In VM    hostname
    ${hn_str}=    Strip String    ${hn.stdout}
    Test SSL Certificate Rotation With Target    ${hn_str}

Test SSL Certificate Set When Not Running
    [Documentation]    Verifies that certificate set rgw fails when RGW is not running.
    [Tags]    rgw
    Test SSL Certificate Set When Not Running

Re-Enable RGW With SSL
    [Documentation]    Re-enables RGW with SSL so subsequent tests have a clean HTTPS endpoint.
    [Tags]    rgw
    Enable RGW SSL

Test Cluster Config Set Reset
    [Documentation]    Verifies rbd_default_features and tests cluster_network config round-trip.
    [Tags]    mgr
    Test Cluster Config Operations

Test Pool Replication Factor Operations
    [Documentation]    Creates a pool, adjusts its replication factor, and verifies expected sizes.
    [Tags]    osd
    Test Pool Replication Operations

Test Log Level Control
    [Documentation]    Sets log level to warning and verifies it reads back as 3.
    [Tags]    mgr
    Test Log Level Control

Test IPv6 Monitor Address Formatting
    [Documentation]    Reinstalls MicroCeph with an IPv6 mon-ip and verifies the address is wrapped
    ...    in square brackets in ceph.conf (as required by Ceph).
    [Tags]    mon
    Test IPv6 Monitor Address Formatting
