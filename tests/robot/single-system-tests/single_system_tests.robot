*** Settings ***
Documentation    single-system-tests
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

Wait For All Daemons After Restart
    [Documentation]    Polls ceph status until mon, mgr, osd, and rgw are all visible, then
    ...    waits for the ceph-osd process to appear. Used after snap stop/start.
    FOR    ${i}    IN RANGE    16
        ${result}=    Run In VM    sudo microceph.ceph status    30
        Log    Attempt ${i}: ${result.stdout}
        IF    "mon: 1 daemons" in $result.stdout and "(active, since" in $result.stdout and "osd: 3 osds" in $result.stdout and "rgw: 1 daemon" in $result.stdout
            Log To Console    [status] PASS: all daemons visible after restart (attempt ${i})
            BREAK
        END
        IF    ${i} == 15    Fail    Cluster never came back fully after snap restart
        Sleep    15s
    END
    FOR    ${i}    IN RANGE    10
        ${pgrep}=    Run In VM    pgrep ceph-osd    10
        IF    ${pgrep.rc} == 0
            Log To Console    [status] ceph-osd process found
            RETURN
        END
        IF    ${i} == 9    Fail    ceph-osd process never appeared after snap restart
        Sleep    2s
    END

Test Waitready Inline
    [Documentation]    Installs snap, verifies waitready fails before bootstrap,
    ...    bootstraps, then verifies waitready passes after bootstrap.
    Log To Console    [waitready] Installing snap and testing waitready lifecycle...
    Install MicroCeph From Local Snap
    Run In VM Must Fail    sudo microceph waitready --timeout 5 2>/dev/null
    Bootstrap MicroCeph Cluster
    Run In VM And Check    sudo microceph waitready --timeout 30    60

Test Pool Replication Operations
    [Documentation]    Tests pool replication-factor set/reset operations.
    Log To Console    [pool] Testing pool replication factor operations...
    Run In VM    sudo microceph.ceph osd pool create mypool    30
    Run In VM And Check    sudo microceph pool set-rf --size 1 ""    30
    Run In VM And Check    sudo microceph.ceph config get osd.1 osd_pool_default_size | grep -Fx "1"    30
    Run In VM And Check    sudo microceph pool list | grep "mypool"    30
    Run In VM And Check    sudo microceph pool list | grep "mypool" | grep -F " 3 "    30
    Run In VM And Check    sudo microceph pool set-rf --size 3 mypool    30
    Run In VM And Check    sudo microceph.ceph osd pool get mypool size | grep -Fx "size: 3"    30
    Run In VM And Check    sudo microceph pool list | grep "mypool" | grep -F " 3 "    30
    Run In VM And Check    sudo microceph pool set-rf --size 1 "*"    30
    Run In VM And Check    sudo microceph.ceph osd pool get mypool size | grep -Fx "size: 1"    30
    Run In VM And Check    sudo microceph pool list | grep "mypool" | grep -F " 1 "    30

Test Log Level Control
    [Documentation]    Sets log level to warning (3) and verifies.
    Run In VM And Check    sudo microceph log set-level warning    30
    ${result}=    Run In VM    sudo microceph log get-level    30
    Should Be Equal As Strings    ${result.stdout.strip()}    3    msg=Incorrect log level: ${result.stdout}

Test SSL Certificate Rotation
    [Documentation]    Tests RGW SSL certificate rotation (with/without --restart).
    Log To Console    [rgw] Testing SSL certificate rotation...
    Run In VM And Check    sudo openssl genrsa -out /tmp/rotated-cert.key 2048    30
    Run In VM And Check    sudo openssl req -new -key /tmp/rotated-cert.key -out /tmp/rotated-cert.csr -subj "/CN=rotated-cert"    30
    Run In VM And Check    bash -c "echo 'subjectAltName = DNS:localhost' > /tmp/rotated-cert-ext.cnf"    10
    Run In VM And Check    sudo openssl x509 -req -in /tmp/rotated-cert.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/rotated-cert.crt -days 365 -extfile /tmp/rotated-cert-ext.cnf    30
    ${cert}=    Run In VM    sudo base64 -w0 /tmp/rotated-cert.crt    30
    ${key}=    Run In VM    sudo base64 -w0 /tmp/rotated-cert.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${cert.stdout.strip()}" --ssl-private-key="${key.stdout.strip()}" --restart    120
    Wait For RGW    1
    Wait For RGW SSL Port
    ${cn}=    Get RGW SSL CN
    Should Be Equal As Strings    ${cn}    rotated-cert    msg=Expected CN=rotated-cert after --restart
    Run In VM And Check    sudo openssl genrsa -out /tmp/rotated-cert-2.key 2048    30
    Run In VM And Check    sudo openssl req -new -key /tmp/rotated-cert-2.key -out /tmp/rotated-cert-2.csr -subj "/CN=rotated-cert-2"    30
    Run In VM And Check    bash -c "echo 'subjectAltName = DNS:localhost' > /tmp/rotated-cert-2-ext.cnf"    10
    Run In VM And Check    sudo openssl x509 -req -in /tmp/rotated-cert-2.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/rotated-cert-2.crt -days 365 -extfile /tmp/rotated-cert-2-ext.cnf    30
    ${cert2}=    Run In VM    sudo base64 -w0 /tmp/rotated-cert-2.crt    30
    ${key2}=    Run In VM    sudo base64 -w0 /tmp/rotated-cert-2.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${cert2.stdout.strip()}" --ssl-private-key="${key2.stdout.strip()}"    60
    Sleep    3s
    ${cn}=    Get RGW SSL CN
    Should Be Equal As Strings    ${cn}    rotated-cert    msg=Old cert should still be served without restart
    Run In VM And Check    sudo snap restart microceph.rgw    60
    Wait For RGW    1
    Wait For RGW SSL Port
    ${cn}=    Get RGW SSL CN
    Should Be Equal As Strings    ${cn}    rotated-cert-2    msg=Expected rotated-cert-2 after manual restart
    ${orig_cert}=    Run In VM    sudo base64 -w0 /tmp/server.crt    30
    ${orig_key}=    Run In VM    sudo base64 -w0 /tmp/server.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${orig_cert.stdout.strip()}" --ssl-private-key="${orig_key.stdout.strip()}" --restart    120
    Wait For RGW    1
    Wait For RGW SSL Port

Test SSL Certificate Rotation With Target
    [Documentation]    Tests RGW SSL certificate rotation with --target flag.
    [Arguments]    ${target}
    Log To Console    [rgw] Testing certificate rotation with --target ${target}...
    ${addr_result}=    Run In VM    sudo microceph status | grep -F "${target}" | grep -oP '\\(\\K[^)]+' || true    30
    ${target_addr}=    Set Variable    ${addr_result.stdout.strip()}
    Run In VM And Check    sudo openssl genrsa -out /tmp/target-cert.key 2048    30
    Run In VM And Check    sudo openssl req -new -key /tmp/target-cert.key -out /tmp/target-cert.csr -subj "/CN=target-cert"    30
    Run In VM And Check    bash -c "echo 'subjectAltName = DNS:localhost' > /tmp/target-cert-ext.cnf"    10
    Run In VM And Check    sudo openssl x509 -req -in /tmp/target-cert.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/target-cert.crt -days 365 -extfile /tmp/target-cert-ext.cnf    30
    ${cert}=    Run In VM    sudo base64 -w0 /tmp/target-cert.crt    30
    ${key}=    Run In VM    sudo base64 -w0 /tmp/target-cert.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${cert.stdout.strip()}" --ssl-private-key="${key.stdout.strip()}" --target ${target} --restart    120
    Wait For RGW    1
    Wait For RGW SSL Port    ${target_addr}
    ${cn}=    Get RGW SSL CN    ${target_addr}
    Should Be Equal As Strings    ${cn}    target-cert    msg=Expected CN=target-cert on ${target}
    ${orig_cert}=    Run In VM    sudo base64 -w0 /tmp/server.crt    30
    ${orig_key}=    Run In VM    sudo base64 -w0 /tmp/server.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${orig_cert.stdout.strip()}" --ssl-private-key="${orig_key.stdout.strip()}" --target ${target} --restart    120
    Wait For RGW    1
    Wait For RGW SSL Port    ${target_addr}

Test SSL Certificate Set When Not Running
    [Documentation]    Verifies that certificate set fails when RGW is not running.
    Log To Console    [rgw] Verifying certificate set fails when RGW is not running...
    Run In VM And Check    sudo microceph disable rgw    60
    Sleep    3s
    ${cert}=    Run In VM    sudo base64 -w0 /tmp/server.crt    30
    ${key}=    Run In VM    sudo base64 -w0 /tmp/server.key    30
    Run In VM Must Fail    sudo microceph certificate set rgw --ssl-certificate="${cert.stdout.strip()}" --ssl-private-key="${key.stdout.strip()}"

Test IPv6 Monitor Address Formatting
    [Documentation]    Reinstalls with IPv6 mon-ip and verifies square brackets in ceph.conf.
    [Arguments]    ${mon_ip}=fd42:7273:f336:a22::1
    Log To Console    [mon] Testing IPv6 monitor address formatting...
    Run In VM And Check    sudo snap remove microceph    300
    ${iface_result}=    Run In VM    ip route show default | awk '/default via/ {print $5}' | head -1    30
    ${iface}=    Set Variable    ${iface_result.stdout.strip()}
    Log To Console    [mon] Adding IPv6 address to interface ${iface}...
    Run In VM And Check    sudo ip -6 addr add dev ${iface} ${mon_ip}    30
    Install And Bootstrap MicroCeph    ${mon_ip}
    ${result}=    Run In VM    grep -F "[${mon_ip}]" /var/snap/microceph/current/conf/ceph.conf    30
    Should Be Equal As Integers    ${result.rc}    0    msg=IPv6 address ${mon_ip} not wrapped in brackets in ceph.conf

Test RGW Stale Run Dir Migration
    [Documentation]    Injects a stale run dir into radosgw.conf, restarts the snap, and verifies
    ...    the daemon repairs the config to use the stable 'current' symlink.
    ...    Mirrors actionutils.sh test_rgw_stale_run_dir_migration.
    Log To Console    [rgw] Testing RGW stale run dir migration...
    Run In VM And Check    sudo sed -i 's|run dir = .*|run dir = /var/snap/microceph/1/run|' /var/snap/microceph/current/conf/radosgw.conf    10
    Run In VM And Check    grep "run dir" /var/snap/microceph/current/conf/radosgw.conf    10
    Run In VM And Check    sudo snap stop microceph    120
    Run In VM And Check    sudo snap start microceph    60
    Wait For RGW    1
    Run In VM And Check    grep -q "run dir = /var/snap/microceph/current/run" /var/snap/microceph/current/conf/radosgw.conf    10

Verify Mount Check
    [Documentation]    Verifies that adding a mounted root device fails with "is currently mounted",
    ...    even when --wipe is specified. Mirrors actionutils.sh verify_mount_check.
    Log To Console    [disk] Testing mount check...
    ${rootdev}=    Run In VM    findmnt --noheadings --output SOURCE /    30
    ${err}=    Run In VM    sudo microceph disk add --wipe ${rootdev.stdout.strip()} 2>&1 || true    30
    Should Contain    ${err.stdout}    is currently mounted    msg=Expected mount check failure for ${rootdev.stdout.strip()}

Test Cluster Config Operations
    [Documentation]    Verifies rbd_default_features and tests cluster_network config set/reset.
    Log To Console    [config] Testing cluster config set/reset...
    ${rbd_feat}=    Run In VM    sudo microceph.ceph config get mon rbd_default_features    30
    Should Be Equal As Strings    ${rbd_feat.stdout.strip()}    63    msg=rbd_default_features not 63
    ${cip}=    Run In VM    ip -4 -j route | jq -r '.[] | select(.dst | contains("default")) | .prefsrc' | tr -d '[:space:]'    30
    ${ts}=    Run In VM    sudo systemctl show --property ActiveEnterTimestampMonotonic snap.microceph.osd.service | cut -d= -f2    30
    Run In VM And Check    sudo microceph cluster config set cluster_network ${cip.stdout.strip()}/8 --wait    60
    ${ts2}=    Run In VM    sudo systemctl show --property ActiveEnterTimestampMonotonic snap.microceph.osd.service | cut -d= -f2    30
    ${out}=    Run In VM    sudo microceph cluster config get cluster_network | grep -cim1 'cluster_network'    30
    Should Be True    int('${out.stdout.strip()}') >= 1    msg=config check failed
    Should Be True    int('${ts2.stdout.strip()}') >= int('${ts.stdout.strip()}')    msg=OSD service did not restart after config set
    Run In VM And Check    sudo microceph cluster config reset cluster_network --wait    60
    ${ts3}=    Run In VM    sudo systemctl show --property ActiveEnterTimestampMonotonic snap.microceph.osd.service | cut -d= -f2    30
    Should Be True    int('${ts3.stdout.strip()}') >= int('${ts2.stdout.strip()}')    msg=OSD service did not restart after config reset

Test Snap Disable Enable
    [Documentation]    Tests that snap disable/enable re-enables all services.
    ...    Records the names of services that were enabled+active before disable and verifies
    ...    each one by name is restored after re-enable (not just the aggregate count), and
    ...    fails on timeout. Mirrors bash test_snap_disable_enable + check_snap_service_active_enabled.
    Log To Console    [snap] Testing snap disable/enable service restoration...
    ${before_result}=    Run In VM    snap services microceph    30
    @{services_before}=    Enabled Active Services    ${before_result.stdout}
    ${count_before}=    Get Length    ${services_before}
    Log To Console    [snap] ${count_before} enabled+active service(s) before disable: ${services_before}
    Run In VM And Check    sudo snap disable microceph    60
    Run In VM And Check    sudo snap enable microceph    60
    ${restored}=    Set Variable    ${False}
    FOR    ${i}    IN RANGE    30
        ${now_result}=    Run In VM    snap services microceph    30
        @{services_now}=    Enabled Active Services    ${now_result.stdout}
        ${active}=    Get Length    ${services_now}
        IF    ${active} >= ${count_before}
            Log To Console    [snap] All ${count_before} service(s) re-enabled after ${i}s
            ${restored}=    Set Variable    ${True}
            BREAK
        END
        Sleep    1s
    END
    IF    not ${restored}    Fail    Not all services re-enabled after 30s (expected ${count_before})
    # Verify each previously enabled+active service is back, by name.
    FOR    ${svc}    IN    @{services_before}
        ${svc_state}=    Run In VM    snap services ${svc}    30
        Should Contain    ${svc_state.stdout}    enabled    msg=Service ${svc} not enabled after re-enable
        Should Contain    ${svc_state.stdout}    active    msg=Service ${svc} not active after re-enable
    END
    Verify Cluster Health

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
    ${hn}=    Get VM Hostname
    Run In VM And Check    sudo microceph.ceph orch host ls | grep -F ${hn}    30
    Run In VM And Check    sudo microceph.ceph orch ls | grep -F "mon" | grep -F ${hn}    30
    Run In VM And Check    sudo microceph.ceph orch ls | grep -F "mds" | grep -F ${hn}    30
    Run In VM And Check    sudo microceph.ceph orch ls | grep -F "mgr" | grep -F ${hn}    30

Add OSD With Failure
    [Documentation]    Verifies adding an encrypted OSD fails without dm-crypt,
    ...    and batch disk add fails with wal/db device flags.
    [Tags]    osd    disk-management
    Create Loop Device At    /dev/sdi21    1G
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
    Wait For All Daemons After Restart

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
    ${hn}=    Get VM Hostname
    Test SSL Certificate Rotation With Target    ${hn}

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
