*** Settings ***
Documentation    nfs-test
...    Tests MicroCeph NFS feature on a single node: enable NFS cluster, create CephFS volume,
...    create export, mount via CephFS NFS, write/read a file, test log rotation and stale run dir migration.
Resource        ../resources/microceph_harness.resource
Suite Setup     NFS Tests Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    nfs    cephfs    lxd    integration

*** Variables ***
${OUTER_VM_IMAGE}    ubuntu:26.04

*** Keywords ***
NFS Tests Suite Setup
    Launch Outer Test VM    vm_name=microceph-nfs-vm
    Verify Resolute Outer VM
    Copy Scripts To VM
    Copy Snap To VM
    Install And Bootstrap MicroCeph
    Run In VM And Check    sudo microceph disk add loop,1G,3    120
    Wait For OSD Count    3
    Prepare Grace Cleanup Audit

Prepare Grace Cleanup Audit
    [Documentation]    Builds an observer that forwards every call to the real librados.
    Run In VM And Check    sudo apt-get update    120
    Run In VM And Check    sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y gcc libc6-dev librados-dev    300
    ${copy}=    Run Process    lxc    file    push    ${REPO_ROOT}/tests/scripts/grace_cleanup_audit.c    ${OUTER_VM}/root/grace_cleanup_audit.c    timeout=60
    Should Be Equal As Integers    ${copy.rc}    0    msg=${copy.stderr}
    Run In VM And Check    sudo gcc -shared -fPIC -Wall -Wextra -Werror /root/grace_cleanup_audit.c -o /var/snap/microceph/common/grace-cleanup-audit.so -ldl    60

Run Audited Grace Helper
    [Documentation]    Checks real resource release before exit, not whether a timing-dependent crash occurs.
    [Arguments]    ${arguments}    ${expected_rc}=0    ${cephconf}=/var/snap/microceph/current/conf/ceph.conf
    ${result}=    Run In VM    sudo snap run --shell microceph.daemon -c 'LD_PRELOAD=/var/snap/microceph/common/grace-cleanup-audit.so exec "$SNAP/bin/ganesha-rados-grace" --cephconf ${cephconf} --userid admin --pool .nfs --ns microceph-ci-grace-cleanup ${arguments}'    60
    Should Be Equal As Integers    ${result.rc}    ${expected_rc}    msg=${result.stderr}
    Should Contain    ${result.stderr}    grace cleanup: OK

Skip If Log Rotate App Not Available
    [Documentation]    Skips the test if the microceph.log-rotate snap app is absent.
    ${has_app}=    Run In VM    test -e /snap/microceph/current/commands/log-rotate.start && echo yes || echo no    30
    IF    $has_app.stdout.strip() != "yes"
        Skip    microceph.log-rotate app not available in this snap version — skipping
    END

Wait For Ganesha Log
    [Documentation]    Polls until /var/snap/microceph/common/logs/ganesha/ganesha.log exists (30 s max).
    FOR    ${i}    IN RANGE    30
        ${found}=    Run In VM    test -f /var/snap/microceph/common/logs/ganesha/ganesha.log && echo yes || echo no    10
        IF    $found.stdout.strip() == "yes"    RETURN
        IF    ${i} == 29    Fail    Ganesha log not found after 30s
        Sleep    1s
    END

Backdate Logrotate State File
    [Documentation]    Writes a logrotate state file backdated to yesterday so the daily check passes.
    ${yesterday}=    Run In VM    date -d 'yesterday' '+%Y-%-m-%-d-0:0:0'    10
    ${ydate}=    Set Variable    ${yesterday.stdout.strip()}
    Run In VM And Check    echo 'logrotate state -- version 2' | sudo tee /var/snap/microceph/common/logrotate.status > /dev/null    10
    Run In VM And Check    for f in /var/snap/microceph/common/logs/*.log /var/snap/microceph/common/logs/ganesha/*.log; do [ -f $f ] && printf '"%s" %s\n' $f ${ydate} | sudo tee -a /var/snap/microceph/common/logrotate.status > /dev/null; done    30
    Run In VM And Check    sudo chmod 600 /var/snap/microceph/common/logrotate.status    10

Run Log Rotate
    [Documentation]    Runs the microceph.log-rotate snap app.
    Run In VM And Check    sudo snap run microceph.log-rotate    60

Rotated Log Should Exist
    [Documentation]    Asserts that at least one file matching ${pattern} exists on the outer VM.
    [Arguments]    ${pattern}
    Run In VM And Check    ls ${pattern}    30

Test Log Rotation Inline
    [Documentation]    Verifies that the log-rotate snap service rotates both Ceph daemon
    ...    and Ganesha log files.
    Log To Console    [nfs] Testing log rotation...
    Skip If Log Rotate App Not Available
    Wait For Ganesha Log
    Run In VM And Check    ls /var/snap/microceph/common/logs/ceph-mon.*.log    30
    Log To Console    [nfs] Waiting 60s for log content...
    Sleep    60s
    Run In VM    cat /etc/systemd/system/snap.microceph.log-rotate.timer || true    10
    Run In VM    systemctl status snap.microceph.log-rotate.timer || true    10
    Run In VM    ls -la /var/snap/microceph/common/logs/*.log || true    10
    Run In VM    ls -la /var/snap/microceph/common/logs/ganesha/*.log || true    10
    Backdate Logrotate State File
    Run Log Rotate
    Run In VM    ls -la /var/snap/microceph/common/logs/ || true    10
    Run In VM    ls -la /var/snap/microceph/common/logs/ganesha/ || true    10
    Rotated Log Should Exist    /var/snap/microceph/common/logs/ceph-mon.*.log.1
    Rotated Log Should Exist    /var/snap/microceph/common/logs/ganesha/ganesha.log.1
    Run In VM And Check    test -f /var/snap/microceph/common/logs/ganesha/ganesha.log    10

Test NFS Stale Run Dir Migration Inline
    [Documentation]    Injects a stale run dir into ganesha.conf and verifies the daemon repairs it.
    Log To Console    [nfs] Testing NFS stale run dir migration...
    # Inject a stale revision-specific CCacheDir
    Run In VM And Check    sudo sed -i 's|CCacheDir = ".*";|CCacheDir = "/var/snap/microceph/1/run/ganesha";|' /var/snap/microceph/current/conf/ganesha/ganesha.conf    10
    Run In VM And Check    grep "CCacheDir" /var/snap/microceph/current/conf/ganesha/ganesha.conf    10
    # Restart daemon so migrateStaleRunDir fires
    Run In VM And Check    sudo snap stop microceph    30
    Run In VM And Check    sudo snap start microceph    30
    # Wait for migration log message
    FOR    ${i}    IN RANGE    30
        ${logs}=    Run In VM    sudo snap logs microceph.daemon -n 100    15    quiet=${True}
        ${found}=    Get Regexp Matches    ${logs.stdout}    fixed stale run dir.*ganesha.conf
        IF    $logs.rc == 0 and $found
            Log To Console    [nfs] Daemon logged ganesha migration complete
            BREAK
        END
        IF    ${i} == 29
            Run In VM    sudo snap logs microceph.daemon -n 100 || true    15
            Fail    Daemon did not log ganesha migration after 30s
        END
        Sleep    1s
    END
    # Confirm config was repaired to use stable 'current' symlink
    Run In VM And Check    grep -q 'CCacheDir = "/var/snap/microceph/current/run/ganesha"' /var/snap/microceph/current/conf/ganesha/ganesha.conf    10

*** Test Cases ***
Test Enable NFS
    [Documentation]    Enables NFS with cluster-id "foo" and waits for the daemon to start.
    [Tags]    nfs
    Enable NFS    foo

Test Grace Helper Cleanup
    [Documentation]    Add and remove must destroy the I/O context before shutting down the client.
    Run Audited Grace Helper    add cleanup-node
    Run Audited Grace Helper    remove cleanup-node

Test Grace Helper Cleanup On Command Failure
    [Documentation]    A failed grace operation must still release both handles and retain exit status 1.
    Run Audited Grace Helper    remove absent-node    expected_rc=1

Test Grace Helper Cleanup On Setup Failure
    [Documentation]    A configuration failure after client creation must release the partial connection.
    Run Audited Grace Helper    dump    expected_rc=1    cephconf=/var/snap/microceph/common/no-such-grace.conf

Test Create NFS FS Volume
    [Documentation]    Creates a CephFS volume named "testfs" for the NFS export.
    [Tags]    nfs    cephfs
    Create NFS FS Volume    testfs

Test Create NFS Export
    [Documentation]    Creates an NFS export for cluster "foo" backed by filesystem "testfs".
    [Tags]    nfs
    Create NFS Export    foo    testfs

Test Mount And Write NFS
    [Documentation]    Installs ceph-common, mounts the CephFS NFS share, writes a file,
    ...    reads it back, and unmounts.
    [Tags]    nfs    cephfs
    Install Ceph Client From PPA
    Run In VM And Check    sudo mkdir -p /mnt/nfs    10
    Run In VM And Check    sudo cp /var/snap/microceph/current/conf/ceph.conf /etc/ceph/    10
    Run In VM And Check    sudo cp /var/snap/microceph/current/conf/ceph.client.admin.keyring /etc/ceph/    10
    ${ip}=    Get VM IP
    Run In VM And Check    sudo mount -t ceph "${ip}:/" /mnt/nfs -o name=admin    30
    Write File In VM    /mnt/nfs/general.kenobi    Hello there!
    File In VM Should Contain    /mnt/nfs/general.kenobi    Hello there!
    Run In VM And Check    sudo umount /mnt/nfs    10

Test Log Rotation
    [Documentation]    Verifies that the log-rotate snap service rotates both Ceph daemon
    ...    and Ganesha log files.
    [Tags]    nfs
    Test Log Rotation Inline

Test NFS Stale Run Dir Migration
    [Documentation]    Injects a stale run dir into ganesha.conf and verifies the daemon repairs it.
    [Tags]    nfs
    Test NFS Stale Run Dir Migration Inline

Test Disable NFS
    [Documentation]    Disables the NFS cluster.
    [Tags]    nfs
    Disable NFS    foo
