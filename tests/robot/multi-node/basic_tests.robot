*** Settings ***
Documentation    multi-node basic tests
...    Bootstraps a 4-node cluster in LXD containers and verifies OSD addition.
Resource        ../resources/microceph_harness.resource
Suite Setup     Multi Node Basic Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    cluster    integration    lxd    slow

*** Keywords ***
Multi Node Basic Suite Setup
    Provision Multinode VM    microceph-mn-basic-vm    50GiB    public
    Bootstrap Head Node    public
    Join Worker Nodes To Cluster    public

*** Test Cases ***
Test Multi Node Bootstrap
    [Documentation]    Verifies all 4 nodes are visible in microceph status after bootstrapping.
    [Tags]    multi-node    cluster
    Wait For N Nodes In Cluster    4

Test OSD Addition
    [Documentation]    Adds an OSD to each worker node and verifies 3 OSDs are in.
    [Tags]    multi-node    osd
    Add OSD To Node    node-wrk1
    Add OSD To Node    node-wrk2
    Add OSD To Node    node-wrk3
    Wait For OSD Count Head    3
    Run In VM And Check    lxc exec node-wrk0 -- microceph.ceph -s | egrep "osd: 3 osds"    60
