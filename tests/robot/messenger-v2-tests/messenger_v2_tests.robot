*** Settings ***
Documentation    messenger-v2-tests
...    Verifies that MicroCeph uses Messenger v2 protocol only (no v1 addresses, no port 6789).
...    Bootstraps with --v2-only flag, then verifies on single node and all nodes.
Resource        ../resources/microceph_harness.resource
Suite Setup     Messenger V2 Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    messenger-v2    mon    lxd    slow    integration

*** Keywords ***
Messenger V2 Suite Setup
    Provision Multinode VM    microceph-msgv2-vm    50GiB    public

Test Messenger V2 On Single Node
    [Documentation]    Verifies node-wrk0 has no v1 addresses in mon dump and is not listening on port 6789.
    Log To Console    [messenger-v2] Checking node-wrk0 for messenger v2 compliance...
    ${out}=    Run In VM    lxc exec node-wrk0 -- microceph.ceph mon dump    30
    Should Not Contain    ${out.stdout}    v1:    msg=Messenger V1 address is still present in mon dump
    Should Not Contain    ${out.stdout}    6789    msg=Messenger V1 port 6789 is still present in mon dump
    ${listening_6789}=    Run In VM    lxc exec node-wrk0 -- sh -c "sudo ss -Htnpl | grep -c ':6789.*ceph-mon' || true"    30
    Should Be Equal As Strings    ${listening_6789.stdout.strip()}    0    msg=ceph-mon is still listening on port 6789

Ceph Conf Should Have No V1 Addresses
    [Documentation]    Asserts that ceph.conf on ${node} contains no v1: monitor addresses.
    ...    Uses || true so that grep -c exiting rc=1 (zero matches) does not also trigger
    ...    the fallback and produce duplicate output -- grep -c already prints "0" on stdout.
    [Arguments]    ${node}
    ${result}=    Run In VM    lxc exec ${node} -- sh -c "grep -c 'v1:' /var/snap/microceph/current/conf/ceph.conf || true"    30
    Should Be Equal As Strings    ${result.stdout.strip()}    0    msg=Messenger V1 address found in ceph.conf on ${node}

Test Messenger V2 On All Nodes
    [Documentation]    Verifies that none of the 4 nodes have v1 monitor addresses in ceph.conf.
    Log To Console    [messenger-v2] Checking all nodes for v1 addresses in ceph.conf...
    FOR    ${i}    IN    0    1    2    3
        Ceph Conf Should Have No V1 Addresses    node-wrk${i}
    END

*** Test Cases ***
Test Bootstrap With V2 Only
    [Documentation]    Bootstraps the head node (node-wrk0) with --v2-only flag.
    [Tags]    messenger-v2    mon
    Bootstrap Head Node    public    --v2-only

Test Messenger V2 On Head Node
    [Documentation]    Verifies node-wrk0 has no v1 addresses in mon dump and is not listening on port 6789.
    [Tags]    messenger-v2    mon
    Test Messenger V2 On Single Node

Test Cluster Join
    [Documentation]    Joins the remaining 3 worker nodes to the cluster.
    [Tags]    messenger-v2    cluster
    Join Worker Nodes To Cluster    public

Test Messenger V2 On All Nodes After Join
    [Documentation]    Verifies that none of the 4 nodes have v1 monitor addresses in ceph.conf.
    [Tags]    messenger-v2    mon
    Test Messenger V2 On All Nodes
