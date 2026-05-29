*** Settings ***
Documentation    Translated from: .github/workflows/tests.yml — cephadm-adopt-test
...    Runs adoptutils.sh functions directly on the host runner (no outer VM).
...    The script creates its own KVM VMs internally; nesting KVM inside an LXD
...    VM makes the inner agent unreachable, so we must run on bare metal.
Resource        ../resources/microceph_harness.resource
Library         ../resources/streaming_process.py
Suite Setup     Cephadm Adopt Suite Setup
Suite Teardown  Cephadm Adopt Suite Teardown
Test Tags       cephadm    adopt    cephfs    replication    lxd    slow    integration

*** Variables ***
${XTRACE}       ${False}

*** Keywords ***
Cephadm Adopt Suite Setup
    [Documentation]    Checks that lxc is available on the host, copies adoptutils.sh to ~/,
    ...    and marks it executable. No outer VM is launched here.
    Require Host Commands    lxc
    Log To Console    [setup] Cephadm adopt tests run on host — no outer VM (KVM nesting not available).
    ${prep}=    Run Process
    ...    bash -c "cp ${REPO_ROOT}/tests/scripts/adoptutils.sh ~/adoptutils.sh && chmod +x ~/adoptutils.sh"
    ...    shell=True    timeout=60
    Should Be Equal As Integers    ${prep.rc}    0    msg=Setup failed: ${prep.stderr}

Cephadm Adopt Suite Teardown
    [Documentation]    Deletes the primary/secondary VMs and their storage volumes.
    Log To Console    [teardown] Cleaning up cephadm VMs (primary, secondary)...
    Run Process    lxc delete --force primary     shell=True    timeout=60
    Run Process    lxc delete --force secondary    shell=True    timeout=60
    Run Process
    ...    bash -c "for v in primary-1 primary-2 primary-3 secondary-1 secondary-2 secondary-3; do lxc storage volume delete default \$v 2>/dev/null || true; done"
    ...    shell=True    timeout=60

Run Adoptutils
    [Documentation]    Runs ~/adoptutils.sh <function> [args] with streaming output.
    [Arguments]    ${function}    ${args}=${EMPTY}    ${timeout}=3600
    ${cmd}=    Set Variable    ~/adoptutils.sh ${function} ${args}
    ${rc}    ${out}=    Run Streaming Process    ${cmd}    timeout=${timeout}    xtrace=${XTRACE}
    Log    ${out}
    Should Be Equal As Integers    ${rc}    0    msg=${function} failed (rc=${rc})

*** Test Cases ***
Test Setup Cephadm Primary VM
    [Documentation]    Creates the primary LXD VM with 3 block storage volumes for cephadm.
    [Tags]    cephadm    adopt
    Run Adoptutils    create_cephadm_vm    primary    timeout=600

Test Setup Cephadm Secondary VM
    [Documentation]    Creates the secondary LXD VM with 3 block storage volumes for cephadm.
    [Tags]    cephadm    adopt
    Run Adoptutils    create_cephadm_vm    secondary    timeout=600

Test Bootstrap Cephadm Primary Cluster
    [Documentation]    Installs cephadm on the primary VM and bootstraps a single-host Ceph cluster
    ...    with OSD auto-provisioning.
    [Tags]    cephadm    adopt
    Run Adoptutils    bootstrap_cephadm    primary    timeout=1800

Test Bootstrap Cephadm Secondary Cluster
    [Documentation]    Installs cephadm on the secondary VM and bootstraps a single-host Ceph cluster
    ...    with OSD auto-provisioning.
    [Tags]    cephadm    adopt
    Run Adoptutils    bootstrap_cephadm    secondary    timeout=1800

Test Adopt Primary Cephadm Cluster Into MicroCeph
    [Documentation]    Reads the FSID, mon IP, and admin key from the primary cephadm cluster
    ...    and adopts it into MicroCeph using microceph cluster adopt.
    [Tags]    cephadm    adopt
    ${snap_arg}=    Set Variable If    "${SNAP_PATH}" != "${EMPTY}"    ${SNAP_PATH}    ${EMPTY}
    ${args}=    Set Variable If    "${snap_arg}" != "${EMPTY}"    primary ${snap_arg}    primary
    Run Adoptutils    adopt_cephadm    ${args}    timeout=1800

Test Adopt Secondary Cephadm Cluster Into MicroCeph
    [Documentation]    Same adoption process for the secondary cluster.
    [Tags]    cephadm    adopt
    ${snap_arg}=    Set Variable If    "${SNAP_PATH}" != "${EMPTY}"    ${SNAP_PATH}    ${EMPTY}
    ${args}=    Set Variable If    "${snap_arg}" != "${EMPTY}"    secondary ${snap_arg}    secondary
    Run Adoptutils    adopt_cephadm    ${args}    timeout=1800

Test Exchange Remote Tokens Between Adopted Sites
    [Documentation]    Exports cluster tokens from each adopted site and imports them cross-site
    ...    to enable cross-cluster MicroCeph communication.
    [Tags]    cephadm    adopt    remote
    Run Adoptutils    exchange_adopt_remote_tokens    primary secondary    timeout=300

Test Enable MDS And CephFS Mirror On Both Sites
    [Documentation]    Enables MDS and cephfs-mirror services on both adopted clusters,
    ...    creates a CephFS volume, and enables snapshot mirroring.
    [Tags]    cephadm    cephfs    replication
    Run Adoptutils    remote_enable_fs_rep    primary secondary    timeout=1200

Test Bootstrap CephFS Mirror Peer
    [Documentation]    Creates a peer bootstrap token on the secondary and imports it on the primary
    ...    to establish the CephFS replication relationship.
    [Tags]    cephadm    cephfs    replication
    Run Adoptutils    bootstrap_adopt_cephfs_mirror    primary secondary    timeout=300

Test Verify Remote Subvolume Replication
    [Documentation]    Creates a subvolume on the primary, adds it to the mirror list, and waits
    ...    for it to appear on the secondary (up to 15 minutes).
    [Tags]    cephadm    cephfs    replication    slow
    Run Adoptutils    replication_adopt_check_subvolume_on_sec    primary secondary    timeout=1200
