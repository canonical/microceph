*** Settings ***
Documentation    nfs-multinode-test
...    Tests MicroCeph NFS in a multi-node LXD cluster: enables NFS on 3 nodes,
...    creates a CephFS volume and export, then mounts and writes via NFS v4.
Resource        ../resources/microceph_harness.resource
Suite Setup     NFS Multinode Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    nfs    cephfs    lxd    slow    integration

*** Keywords ***
NFS Multinode Suite Setup
    Launch Outer Test VM    vm_name=microceph-nfsmn-vm    disk_size=50GiB
    Copy Scripts To VM
    Copy Snap To VM
    Clear IPTables
    Free Runner Disk
    Setup LXD In VM
    Create LXD Containers With Loop Devices    public
    Install MicroCeph On All Nodes
    Bootstrap Head Node    public
    Join Worker Nodes To Cluster    public
    Add OSD To Node    node-wrk0
    Add OSD To Node    node-wrk1
    Add OSD To Node    node-wrk2
    Wait For OSD Count Head    3

Enable NFS In Nodes
    [Documentation]    Enables NFS cluster in each specified container.
    [Arguments]    ${cluster_id}    @{containers}
    Log To Console    [nfs] Enabling NFS cluster ${cluster_id} in nodes: @{containers}
    FOR    ${container}    IN    @{containers}
        Log To Console    [nfs] Enabling NFS on ${container}...
        Run In VM And Check    lxc exec ${container} -- microceph enable nfs --cluster-id ${cluster_id}    120
    END

Create NFS FS Volume In Node
    [Documentation]    Creates a CephFS volume on a specific container.
    [Arguments]    ${volume_name}    ${container}
    Log To Console    [nfs] Creating CephFS volume ${volume_name} on ${container}...
    Run In VM And Check    lxc exec ${container} -- microceph.ceph fs volume create ${volume_name}    120

Create NFS Export In Node
    [Documentation]    Creates an NFS export on a specific container.
    [Arguments]    ${cluster_id}    ${fsname}    ${container}
    Log To Console    [nfs] Creating NFS export for ${cluster_id}/${fsname} on ${container}...
    Run In VM And Check    lxc exec ${container} -- microceph.ceph nfs export create cephfs ${cluster_id} /${fsname}dir ${fsname}    60

Disable NFS In Nodes
    [Documentation]    Disables NFS cluster in each specified container.
    [Arguments]    ${cluster_id}    @{containers}
    Log To Console    [nfs] Disabling NFS cluster ${cluster_id} in nodes: @{containers}
    FOR    ${container}    IN    @{containers}
        Log To Console    [nfs] Disabling NFS on ${container}...
        Run In VM And Check    lxc exec ${container} -- microceph disable nfs --cluster-id ${cluster_id}    60
    END

*** Test Cases ***
Test Enable NFS On All Nodes
    [Documentation]    Enables NFS cluster "foo" on node-wrk0, node-wrk1, and node-wrk2.
    [Tags]    nfs    multi-node
    Enable NFS In Nodes    foo    node-wrk0    node-wrk1    node-wrk2

Test Create NFS FS Volume
    [Documentation]    Creates CephFS volume "testfs" on node-wrk0.
    [Tags]    nfs    cephfs
    Create NFS FS Volume In Node    testfs    node-wrk0

Test Create NFS Export
    [Documentation]    Creates NFS export for cluster "foo" backed by "testfs" on node-wrk0.
    [Tags]    nfs
    Create NFS Export In Node    foo    testfs    node-wrk0

Test Mount And Write NFS
    [Documentation]    Installs nfs-common in the outer VM, mounts the NFS share from node-wrk0
    ...    via NFS v4, writes a file, reads it back, and unmounts.
    [Tags]    nfs    multi-node
    Run In VM And Check    sudo apt install nfs-common -y    300
    ${ip}=    Get Node IP    node-wrk0
    Run In VM And Check    sudo mkdir -p /mnt/nfs    10
    Mount NFS In VM    ${ip}    /testfsdir    /mnt/nfs/
    Write File In VM    /mnt/nfs/general.kenobi    Hello there!
    File In VM Should Contain    /mnt/nfs/general.kenobi    Hello there!
    Run In VM And Check    sudo umount /mnt/nfs    10

Test Disable NFS On All Nodes
    [Documentation]    Disables NFS cluster "foo" on all 3 nodes.
    [Tags]    nfs    multi-node
    Disable NFS In Nodes    foo    node-wrk0    node-wrk1    node-wrk2
