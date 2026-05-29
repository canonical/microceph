*** Settings ***
Documentation    Translated from: .github/workflows/tests.yml — nfs-test
...    Tests MicroCeph NFS feature on a single node: enable NFS cluster, create CephFS volume,
...    create export, mount via CephFS NFS, write/read a file, test log rotation and stale run dir migration.
Resource        ../resources/microceph_harness.resource
Suite Setup     NFS Tests Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    nfs    cephfs    lxd    integration

*** Keywords ***
NFS Tests Suite Setup
    Launch Outer Test VM    vm_name=microceph-nfs-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install And Bootstrap MicroCeph
    Run In VM And Check    sudo microceph disk add loop,1G,3    120
    Wait For OSD Count    3

Test Log Rotation Inline
    [Documentation]    Verifies that the log-rotate snap service rotates both Ceph daemon
    ...    and Ganesha log files.
    Log To Console    [nfs] Testing log rotation...
    # Skip if this snap version does not have the log-rotate app
    ${has_app}=    Run In VM    test -e /snap/microceph/current/commands/log-rotate.start && echo yes || echo no    30
    IF    "${has_app.stdout.strip()}" != "yes"
        Skip    microceph.log-rotate app not available in this snap version — skipping
    END
    # Wait for Ganesha log to appear (NFS must be enabled by caller)
    FOR    ${i}    IN RANGE    30
        ${found}=    Run In VM    test -f /var/snap/microceph/common/logs/ganesha/ganesha.log && echo yes || echo no    10
        IF    "${found.stdout.strip()}" == "yes"    BREAK
        IF    ${i} == 29
            Fail    Ganesha log not found after 30s
        END
        Sleep    1s
    END
    # Verify ceph-mon log exists
    Run In VM And Check    ls /var/snap/microceph/common/logs/ceph-mon.*.log    30
    # Wait 60s for daemons to write log content
    Log To Console    [nfs] Waiting 60s for log content...
    Sleep    60s
    # Show timer status in CI output
    Run In VM    cat /etc/systemd/system/snap.microceph.log-rotate.timer || true    10
    Run In VM    systemctl status snap.microceph.log-rotate.timer || true    10
    # Show log file sizes before rotation
    Run In VM    ls -la /var/snap/microceph/common/logs/*.log || true    10
    Run In VM    ls -la /var/snap/microceph/common/logs/ganesha/*.log || true    10
    # Backdate the state file to yesterday so the daily check is satisfied
    ${yesterday}=    Run In VM    date -d 'yesterday' '+%Y-%-m-%-d-0:0:0'    10
    ${ydate}=    Set Variable    ${yesterday.stdout.strip()}
    Run In VM And Check    echo 'logrotate state -- version 2' | sudo tee /var/snap/microceph/common/logrotate.status > /dev/null    10
    Run In VM And Check    for f in /var/snap/microceph/common/logs/*.log /var/snap/microceph/common/logs/ganesha/*.log; do [ -f $f ] && printf '"%s" %s\n' $f ${ydate} | sudo tee -a /var/snap/microceph/common/logrotate.status > /dev/null; done    30
    Run In VM And Check    sudo chmod 600 /var/snap/microceph/common/logrotate.status    10
    Run In VM And Check    sudo snap run microceph.log-rotate    60
    # Show resulting directory state
    Run In VM    ls -la /var/snap/microceph/common/logs/ || true    10
    Run In VM    ls -la /var/snap/microceph/common/logs/ganesha/ || true    10
    # Verify rotated backups exist
    Run In VM And Check    ls /var/snap/microceph/common/logs/ceph-mon.*.log.1    30
    Run In VM And Check    ls /var/snap/microceph/common/logs/ganesha/ganesha.log.1    30
    # Original ganesha.log must still exist
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
        ${found}=    Run In VM    sudo snap logs microceph.daemon -n 100 | grep -q "fixed stale run dir" && echo yes || echo no    15
        IF    "${found.stdout.strip()}" == "yes"
            Log To Console    [nfs] Daemon logged migration complete
            BREAK
        END
        IF    ${i} == 29
            Run In VM    sudo snap logs microceph.daemon -n 100 || true    15
            Fail    Daemon did not log migration after 30s
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
    Run In VM And Check    sudo apt install ceph-common -y    300
    Run In VM And Check    sudo mkdir -p /mnt/nfs    10
    Run In VM And Check    sudo cp /var/snap/microceph/current/conf/ceph.conf /etc/ceph/    10
    Run In VM And Check    sudo cp /var/snap/microceph/current/conf/ceph.client.admin.keyring /etc/ceph/    10
    ${addr}=    Run In VM    hostname -I | cut -d ' ' -f1
    ${ip}=    Strip String    ${addr.stdout}
    Run In VM And Check    sudo mount -t ceph "${ip}:/" /mnt/nfs -o name=admin    30
    Run In VM And Check    echo "Hello there!" | sudo tee /mnt/nfs/general.kenobi    10
    Run In VM And Check    cat /mnt/nfs/general.kenobi | grep -F "Hello there!"    10
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
