*** Settings ***
Documentation    multi-subnet-tests
...    Bootstrap a node that has interface addresses on two subnets using a
...    comma-delimited --public-network / --cluster-network, with --mon-ip on the
...    SECOND listed subnet. This reproduces canonical/microceph#734, where such a
...    bootstrap previously failed with "provided mon-ip ... is not available on
...    provided public network ...". The tests assert the comma-delimited value is
...    rendered verbatim into ceph.conf, cluster_network is stored as the list, and
...    the mon binds on the second subnet and reaches quorum.
Resource         ../resources/microceph_harness.resource
Suite Setup      Multi Subnet Suite Setup
Suite Teardown   Teardown MicroCeph Environment
Test Tags        multi-subnet    cluster    integration    lxd    network

*** Variables ***
${HEAD}    node-wrk0

*** Keywords ***
Multi Subnet Suite Setup
    [Documentation]    Provisions the outer VM and inner nodes on the "public"
    ...    subnet, then bootstraps the head across a second subnet with mon-ip on it.
    Provision Multinode VM    microceph-ms-vm    ${OUTER_VM_DISK}    public
    ${pubnets}    ${mon_ip} =    Bootstrap Multi Subnet Head
    Set Suite Variable    ${EXPECTED_PUBNETS}    ${pubnets}
    Set Suite Variable    ${MON_IP}    ${mon_ip}

*** Test Cases ***
Public Network List Is Rendered Verbatim
    [Documentation]    The comma-delimited public_network is written to ceph.conf unchanged.
    [Tags]    network
    ${value} =    Get Ceph Conf Value    ${HEAD}    public_network
    Should Be Equal    ${value}    ${EXPECTED_PUBNETS}

Cluster Network List Is Stored
    [Documentation]    The comma-delimited cluster_network is stored in the cluster config.
    [Tags]    network
    ${result} =    Run In Container    ${HEAD}    microceph.ceph config get osd cluster_network    30
    Should Be Equal    ${result.stdout.strip()}    ${EXPECTED_PUBNETS}

Mon Binds On The Second Listed Subnet
    [Documentation]    The mon-ip on the non-first listed subnet is accepted, written to
    ...    ceph.conf, and reaches single-node quorum.
    [Tags]    network
    ${mon_host} =    Get Ceph Conf Value    ${HEAD}    mon host
    Should Contain    ${mon_host}    ${MON_IP}
    Run In Container    ${HEAD}    microceph.ceph -s | grep "mon: 1 daemons, quorum ${HEAD}"    30
