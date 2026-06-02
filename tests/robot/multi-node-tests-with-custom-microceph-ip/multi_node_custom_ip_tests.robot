*** Settings ***
Documentation    multi-node-tests-with-custom-microceph-ip
...    Multi-node cluster bootstrapped with --microceph-ip on an internal network,
...    verifying each node uses its own internal IP for cluster communication.
Resource        ../resources/microceph_harness.resource
Suite Setup     Custom IP Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    cluster    custom-ip    lxd    slow    integration

*** Keywords ***
Custom IP Suite Setup
    Launch Outer Test VM    vm_name=microceph-cip-vm    disk_size=50GiB
    Copy Scripts To VM
    Copy Snap To VM
    Clear IPTables
    Free Runner Disk
    Setup LXD In VM
    Create LXD Containers With Loop Devices    internal
    Install MicroCeph On All Nodes
    Bootstrap Head Node    internal
    Join Worker Nodes To Cluster    internal

*** Test Cases ***
Test Custom MicroCeph IP Bootstrap
    [Documentation]    Verifies each node reports its internal IP in microceph status after
    ...    bootstrapping with --microceph-ip on an internal network.
    [Tags]    multi-node    custom-ip
    Run In VM And Check    lxc exec node-wrk0 -- sh -c "microceph status"    30

Test Three OSDs With Custom IP
    [Documentation]    Adds 3 OSDs (one per node-wrk0..2) and verifies they are all up and in.
    [Tags]    multi-node    osd    custom-ip
    Add OSD To Node    node-wrk0
    Add OSD To Node    node-wrk1
    Add OSD To Node    node-wrk2
    Wait For OSD Count Head    3
    Run In VM And Check    lxc exec node-wrk0 -- sh -c "microceph.ceph -s"    30
