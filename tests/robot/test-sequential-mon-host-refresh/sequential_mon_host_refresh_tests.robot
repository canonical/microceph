*** Settings ***
Documentation    test-sequential-mon-host-refresh
...    Regression test for issue #556: after nodes are added one at a time, all nodes'
...    ceph.conf must be updated with the complete monitor address list.
Resource        ../resources/microceph_harness.resource
Suite Setup     Sequential Mon Refresh Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    regression    mon    lxd    slow    integration

*** Variables ***
${NODE0_IP}    ${EMPTY}
${NODE1_IP}    ${EMPTY}
${NW}          ${EMPTY}

*** Keywords ***
Sequential Mon Refresh Suite Setup
    Provision Multinode VM    microceph-smr-vm    ${OUTER_VM_DISK}    public

Wait For IP In Ceph Conf On Node
    [Documentation]    Polls until ${ip} appears in ceph.conf on the given LXD container.
    [Arguments]    ${node}    ${ip}    ${attempts}=24
    FOR    ${i}    IN RANGE    ${attempts}
        ${result}=    Run In VM    lxc exec ${node} -- sh -c "grep -q '${ip}' /var/snap/microceph/current/conf/ceph.conf && echo yes || echo no" 2>/dev/null || true    15
        IF    "${result.stdout.strip()}" == "yes"
            Log To Console    [mon] ${node} ceph.conf contains ${ip} (attempt ${i})
            RETURN
        END
        Sleep    5s
    END
    Fail    Timed out waiting for ${ip} in ceph.conf on ${node}

Mon Host Line Should Contain IP Once
    [Documentation]    Verifies ${ip} appears exactly once on the mon host line in ${node}'s ceph.conf.
    [Arguments]    ${node}    ${ip}
    ${count}=    Run In VM    lxc exec ${node} -- sh -c "grep 'mon host' /var/snap/microceph/current/conf/ceph.conf | grep -c '${ip}'"    30
    Should Be Equal As Strings    ${count.stdout.strip()}    1    msg=${ip} not exactly-once on mon host line in ${node} ceph.conf

Public Network Should Be Set Once
    [Documentation]    Verifies public_network = ${cidr} appears exactly once in ${node}'s ceph.conf.
    [Arguments]    ${node}    ${cidr}
    ${count}=    Run In VM    lxc exec ${node} -- sh -c "grep -c 'public_network = ${cidr}' /var/snap/microceph/current/conf/ceph.conf"    30
    Should Be Equal As Strings    ${count.stdout.strip()}    1    msg=public_network = ${cidr} not exactly-once in ${node} ceph.conf

Derive Node IPs
    [Documentation]    Computes node0 and node1 IPs from the public network gateway and saves
    ...    them as suite variables for use across test cases.
    ${nw}=    Get Public Network CIDR
    ${gw}=    Evaluate    '${nw}'.split('/')[0]
    Set Suite Variable    ${NODE0_IP}    ${gw}0
    Set Suite Variable    ${NODE1_IP}    ${gw}1
    Set Suite Variable    ${NW}          ${nw}
    Log To Console    [mon] nw=${NW} node0_ip=${NODE0_IP}, node1_ip=${NODE1_IP}

*** Test Cases ***
Test Derive Network IPs
    [Documentation]    Derives the expected IP addresses for node-wrk0 and node-wrk1 from the
    ...    public network configuration and stores them for subsequent tests.
    [Tags]    multi-node    regression    mon
    Derive Node IPs

Test Bootstrap Head Node With Public Network
    [Documentation]    Bootstraps node-wrk0 with --public-network so that its IP is recorded
    ...    as the first monitor address.
    [Tags]    multi-node    regression    mon
    ${nw}=    Get Public Network CIDR
    Log To Console    [mon] Bootstrapping node-wrk0 with public-network=${nw}...
    Run In Container    node-wrk0    microceph cluster bootstrap --public-network=${nw}    120

Test Wait For Head Node First Monitor Refresh
    [Documentation]    Polls node-wrk0's ceph.conf until the monitor host entry contains
    ...    node0_ip (regression: the first refresh must complete before the second node joins).
    [Tags]    multi-node    regression    mon
    Log To Console    [mon] Waiting for node-wrk0 first monitor refresh (node0_ip=${NODE0_IP})...
    Wait For IP In Ceph Conf On Node    node-wrk0    ${NODE0_IP}

Test Join First Worker Node To Cluster
    [Documentation]    Generates a join token on node-wrk0 and joins node-wrk1 to the cluster.
    [Tags]    multi-node    regression    mon
    Log To Console    [mon] Joining node-wrk1 sequentially...
    ${tok}=    Run In VM    lxc exec node-wrk0 -- microceph cluster add node-wrk1    60
    Run In Container    node-wrk1    microceph cluster join ${tok.stdout.strip()}    120

Test Head Node Config Updated With Worker IP
    [Documentation]    After node-wrk1 joins, polls node-wrk0's ceph.conf until it contains
    ...    node1_ip (verifying the sequential monitor-host refresh propagates to the head node).
    [Tags]    multi-node    regression    mon
    Log To Console    [mon] Waiting for node-wrk0 to update ceph.conf with ${NODE1_IP}...
    Wait For IP In Ceph Conf On Node    node-wrk0    ${NODE1_IP}

Test Worker Node Config Updated With Own IP
    [Documentation]    Polls node-wrk1's ceph.conf until it contains node1_ip (verifying the
    ...    joining node also receives the full monitor list).
    [Tags]    multi-node    regression    mon
    Log To Console    [mon] Waiting for node-wrk1 to update ceph.conf with ${NODE1_IP}...
    Wait For IP In Ceph Conf On Node    node-wrk1    ${NODE1_IP}

Test All Monitor IPs Present In Head Node Config
    [Documentation]    Verifies that node-wrk0's ceph.conf mon host line contains both the head
    ...    node IP and the worker node IP exactly once each (mirrors bash verify_bootstrap_configs).
    [Tags]    multi-node    regression    mon
    Mon Host Line Should Contain IP Once    node-wrk0    ${NODE0_IP}
    Mon Host Line Should Contain IP Once    node-wrk0    ${NODE1_IP}

Test All Monitor IPs Present In Worker Node Config
    [Documentation]    Verifies that node-wrk1's ceph.conf mon host line contains both the head
    ...    node IP and the worker node IP exactly once each (mirrors bash verify_bootstrap_configs).
    [Tags]    multi-node    regression    mon
    Mon Host Line Should Contain IP Once    node-wrk1    ${NODE0_IP}
    Mon Host Line Should Contain IP Once    node-wrk1    ${NODE1_IP}

Test Public Network Set In Both Node Configs
    [Documentation]    Verifies that public_network = <cidr> with the exact network value appears
    ...    exactly once in ceph.conf on both nodes (mirrors bash verify_bootstrap_configs).
    [Tags]    multi-node    regression    mon
    Public Network Should Be Set Once    node-wrk0    ${NW}
    Public Network Should Be Set Once    node-wrk1    ${NW}
