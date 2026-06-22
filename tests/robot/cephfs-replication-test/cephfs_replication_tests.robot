*** Settings ***
Documentation    cephfs-replication-test
...    Tests MicroCeph CephFS remote replication: two 2-node sites, exchange tokens,
...    enable cephfs-mirror, configure directory mirroring, verify sync and data integrity.
Resource        ../resources/microceph_harness.resource
Resource        ../resources/replication.resource
Library         ../resources/cephfs_replication.py
Suite Setup     CephFS Replication Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    cephfs    replication    remote    lxd    slow    integration

*** Variables ***
${STR1}    ABCDEFGH
${STR2}    IJKLMNOP

*** Keywords ***
CephFS Replication Suite Setup
    Provision Multinode VM    microceph-cfsrep-vm    ${OUTER_VM_DISK}    public

Configure CephFS Mirroring
    [Documentation]    Creates CephFS volumes on both sites, enables directory mirroring,
    ...    mounts the primary filesystem, and writes test data to two directories.
    Log To Console    [cephfs] Configuring CephFS mirroring...
    Run In Container    node-wrk0    sudo microceph.ceph fs volume create vol    120
    Run In Container    node-wrk2    sudo microceph.ceph fs volume create vol    120
    Run In Container    node-wrk0    sudo microceph replication enable cephfs --volume vol --dir-path /dir1/ --remote siteb    120
    Run In Container    node-wrk0    sudo microceph replication enable cephfs --volume vol --dir-path /dir2/ --remote siteb    120
    Log To Console    [cephfs] Installing ceph-common and mounting primary filesystem...
    Mount CephFS From Container    node-wrk0    /mnt/primary    vol
    Run In VM And Check    sudo mkdir -p /mnt/primary/dir1 /mnt/primary/dir2    10
    Run In VM And Check    echo ${STR1} | sudo tee /mnt/primary/dir1/test_file    10
    Run In VM And Check    echo ${STR2} | sudo tee /mnt/primary/dir2/test_file    10

Verify CephFS Mirror List Output
    [Documentation]    Creates subvolumes, adds them to the mirror list, and verifies the list output.
    ...    subvolumegroup / subvolume creation is best-effort: some Ceph builds hit an open cephfs
    ...    python-binding bug (canonical/microceph#610) where get_ancestor_xattr raises a TypeError in
    ...    a mirroring environment, so the create can fail even with a healthy MDS. The create outcome
    ...    is logged so the bug status is visible in the run. The list output is still non-empty from
    ...    the directory paths configured in Configure CephFS Mirroring. Entry-type classification is
    ...    done in cephfs_replication.py.
    Log To Console    [cephfs] Verifying CephFS mirror list output...
    ${grp_status}    ${grp_msg}=    Run Keyword And Ignore Error    Run In Container    node-wrk0    sudo microceph.ceph fs subvolumegroup create vol testGroup    60
    Log To Console    [cephfs] subvolumegroup create (best-effort, #610): ${grp_status}
    ${sv_status}    ${sv_msg}=    Run Keyword And Ignore Error    Run In Container    node-wrk0    sudo microceph.ceph fs subvolume create vol testSubVol    60
    Log To Console    [cephfs] subvolume create (best-effort, #610): ${sv_status}
    Run Keyword And Ignore Error    Run In Container    node-wrk0    sudo microceph.ceph fs subvolume create vol testGroupedSubVol testGroup    60
    IF    "${sv_status}" == "PASS"
        ${subvolpath}=    Run In VM    lxc exec node-wrk0 -- bash -c "sudo microceph.ceph fs subvolume getpath vol testSubVol 2>/dev/null || echo ''"    30
        IF    "${subvolpath.stdout.strip()}" != ""
            Run In Container    node-wrk0    sudo microceph.ceph fs snapshot mirror add vol ${subvolpath.stdout.strip()}    60
        END
    END
    ${groupedpath}=    Run In VM    lxc exec node-wrk0 -- bash -c "sudo microceph.ceph fs subvolume getpath vol testGroupedSubVol testGroup 2>/dev/null || echo ''"    30
    IF    "${groupedpath.stdout.strip()}" != ""
        Run In Container    node-wrk0    sudo microceph.ceph fs snapshot mirror add vol ${groupedpath.stdout.strip()}    60
    END
    Wait For CephFS Replication List Non Empty    node-wrk0    vol
    ${list_json}=    Run In VM    lxc exec node-wrk0 -- sudo microceph replication list cephfs --json    30
    Log    CephFS list output: ${list_json.stdout}
    # /volumes/... paths must classify as "subvolume", every other entry as "directory".
    ${items}=    Verify CephFS List Entry Types    ${list_json.stdout}    vol
    Log    CephFS list classified entries: ${items}

Wait For CephFS Sync
    [Documentation]    Takes snapshots in both mirrored directories and waits for replication.
    ...    Uses the Python helper _cephfs_snaps_synced_total to sum snaps_synced across all
    ...    mirror_status entries so the exact dir key format (/dir1 vs /dir1/) does not matter.
    [Arguments]    ${attempts}
    Log To Console    [cephfs] Taking snapshots and waiting for sync...
    Run In VM And Check    sudo mkdir -p /mnt/primary/dir1/.snap/two-snap    30
    Run In VM And Check    sudo mkdir -p /mnt/primary/dir2/.snap/two-snap    30
    Sleep    20s
    Wait For CephFS Snaps Synced    node-wrk0    vol    2    ${attempts}
    Log To Console    [cephfs] Snapshots replicated to secondary

Verify CephFS Data Integrity
    [Documentation]    Mounts the secondary filesystem and verifies file contents match the primary.
    Log To Console    [cephfs] Verifying CephFS data integrity...
    ${node0_f1}=    Read File In VM    /mnt/primary/dir1/test_file
    ${node0_f2}=    Read File In VM    /mnt/primary/dir2/test_file
    Mount CephFS From Container    node-wrk2    /mnt/secondary    vol
    ${node2_f1}=    Read File In VM    /mnt/secondary/dir1/test_file
    ${node2_f2}=    Read File In VM    /mnt/secondary/dir2/test_file
    Should Be Equal As Strings    ${node0_f1.stdout.strip()}    ${node2_f1.stdout.strip()}    msg=dir1/test_file mismatch between primary and secondary
    Should Be Equal As Strings    ${node0_f2.stdout.strip()}    ${node2_f2.stdout.strip()}    msg=dir2/test_file mismatch between primary and secondary

Disable CephFS Mirroring
    [Documentation]    Verifies non-forced disable fails, then force-disables mirroring.
    Log To Console    [cephfs] Disabling CephFS mirroring...
    ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "sudo microceph replication disable cephfs --volume vol 2>&1"    60
    Should Not Be Equal As Integers    ${result.rc}    0    msg=Non-forced disable should fail
    Run In Container    node-wrk0    sudo microceph replication disable cephfs --volume vol --force    60

*** Test Cases ***
Test Bootstrap Two Sites
    [Documentation]    Bootstraps two independent 2-node MicroCeph clusters (sitea=wrk0/1, siteb=wrk2/3)
    ...    each with 2 loopback-file OSDs.
    [Tags]    cephfs    replication    remote
    Bootstrap Two Sites

Test Exchange Remote Tokens
    [Documentation]    Exchanges cluster export tokens between sitea and siteb.
    [Tags]    cephfs    replication    remote
    Exchange Remote Site Tokens

Test Verify Remote Authentication
    [Documentation]    Verifies cross-cluster ceph commands work on both sites and all nodes.
    [Tags]    cephfs    replication    remote
    Verify Remote Authentication On All Nodes

Test Enable CephFS Mirror Daemon
    [Documentation]    Enables cephfs-mirror daemon on the primary (wrk0) and secondary (wrk2) sites.
    [Tags]    cephfs    replication
    Enable Mirror Service On Both Sites    cephfs-mirror

Test Install Ceph Common On Host
    [Documentation]    Installs ceph-common in the outer VM so that CephFS can be mounted.
    [Tags]    cephfs    replication
    Run In VM And Check    sudo apt install ceph-common -y    300

Test Configure CephFS Mirroring
    [Documentation]    Creates CephFS volumes on both sites, enables mirroring for /dir1 and /dir2,
    ...    mounts the primary filesystem, and writes test data.
    [Tags]    cephfs    replication
    Configure CephFS Mirroring

Test Verify CephFS Mirror List Output
    [Documentation]    Creates subvolumes, adds them to the mirror list, and verifies the list
    ...    output correctly classifies directories vs subvolumes.
    [Tags]    cephfs    replication
    Verify CephFS Mirror List Output

Test Wait For CephFS Sync
    [Documentation]    Takes snapshots and waits for both dirs to replicate to the secondary site.
    [Tags]    cephfs    replication    slow
    Wait For CephFS Sync    240

Test Verify CephFS Data Integrity
    [Documentation]    Mounts the secondary CephFS and verifies file contents match the primary.
    [Tags]    cephfs    replication
    Verify CephFS Data Integrity

Test Disable CephFS Mirroring
    [Documentation]    Verifies non-forced disable fails, then force-disables mirroring.
    [Tags]    cephfs    replication
    Disable CephFS Mirroring

Test Disable CephFS Mirror Daemon
    [Documentation]    Disables the cephfs-mirror daemon on both sites.
    [Tags]    cephfs    replication
    Disable Mirror Service On Both Sites    cephfs-mirror
