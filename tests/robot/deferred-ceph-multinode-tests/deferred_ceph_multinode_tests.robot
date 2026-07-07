*** Settings ***
Documentation    deferred-ceph-multinode-tests
...    Multi-node deferred-Ceph coverage:
...      deferred join forms MicroCluster membership without Ceph auto-placement,
...      Ceph-only bootstrap targets a non-head member,
...      idempotent retry succeeds as a no-op,
...      declarative control placement add/migrate + keep-one invariant.
...    Each suite creates and destroys its own outer LXD VM with 4 inner MicroCeph nodes.
Resource        ../resources/microceph_harness.resource
Suite Setup     Deferred Ceph Multinode Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    deferred    placement    lxd    integration    slow

*** Keywords ***
Deferred Ceph Multinode Suite Setup
    Provision Multinode VM    microceph-deferred-mn-vm    50GiB    public
    Deferred Bootstrap Head Node
    Deferred Join Worker Nodes
    Log To Console    [deferred-ceph] Deferred MicroCluster formed (4 members, Ceph unbootstrapped)

Assert No Ceph Anywhere
    [Documentation]    No container has a Ceph cluster after deferred bootstrap+join.
    FOR    ${c}    IN    node-wrk0    node-wrk1    node-wrk2    node-wrk3
        Assert No Ceph Cluster On Container    ${c}
    END

Ceph Only Bootstrap Target And Verify
    [Documentation]    Bootstrap Ceph on a non-head target member and verify Ceph comes up.
    [Arguments]    ${target}
    Ceph Only Bootstrap Target    ${target}
    Wait For Ceph Healthy On Container    ${target}
    Run In Container    node-wrk0    microceph.ceph -s    30
    Assert Member Has Control Services    ${target}    yes

*** Test Cases ***
Test Deferred Join Forms MicroCluster Without Ceph
    [Documentation]    `microceph cluster join --defer-ceph` joins MicroCluster but does
    ...    not run ceph.Join or auto-place MON/MGR/MDS. All 4 nodes are members; no Ceph cluster.
    [Tags]    deferred
    Assert No Ceph Anywhere
    ${status}=    Run In VM And Check    lxc exec node-wrk0 -- microceph status    30
    Should Contain    ${status.stdout}    node-wrk3    msg=Not all 4 members present after deferred join
    Assert Bootstrap State In Container    node-wrk0    not_bootstrapped

Test Ceph Only Bootstrap On Non Head Target
    [Documentation]    `microceph cluster bootstrap-ceph --target node-wrk1 --public-network=<nw>`
    ...    bootstraps Ceph exactly once on node-wrk1 (a non-head member). Ceph comes up there.
    ...    The public network is the one captured during deferred bootstrap (network flags are
    ...    rejected by `cluster bootstrap --defer-ceph`).
    [Tags]    ceph-only-bootstrap
    Ceph Only Bootstrap Target And Verify    node-wrk1
    Assert Bootstrap State In Container    node-wrk1    bootstrapped

Test Ceph Only Bootstrap Idempotent Retry
    [Documentation]    Re-running `cluster bootstrap-ceph --target node-wrk1` succeeds
    ...    as a no-op (the cluster is already bootstrapped).
    [Tags]    ceph-only-bootstrap
    Run In Container    node-wrk0    microceph cluster bootstrap-ceph --target node-wrk1    120
    Run In Container    node-wrk0    microceph.ceph -s    30

Test Declarative Control Placement Add
    [Documentation]    PUT /1.0/placement with control:true on node-wrk0 adds MON/MGR/MDS
    ...    there via the declarative placement engine.
    [Tags]    placement
    ${resp}=    MicroCeph API Put In Container    node-wrk0    placement    {"mode":"reconcile","members":{"node-wrk0":{"control":true}}}
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=Control placement PUT on node-wrk0 failed: ${resp}
    Wait For Mon Count    2
    Run In Container    node-wrk0    microceph.ceph -s    30

Test Declarative Control Placement Keep One Invariant
    [Documentation]    A placement that would remove the last control service must be
    ...    rejected with a clear keep-one reason (HTTP non-2xx / error), and the last MON must
    ...    remain. We request control:false on the only control member while no other control
    ...    member exists.
    [Tags]    placement
    # node-wrk1 has control from bootstrap; node-wrk0 has control from the previous test.
    # Request control:false on BOTH current control members at once -> keep-one refuses the last.
    ${resp}=    MicroCeph API Put In Container    node-wrk0    placement    {"mode":"reconcile","members":{"node-wrk0":{"control":false},"node-wrk1":{"control":false}}}
    ${code}=    Response Status Code    ${resp}
    Run Keyword And Continue On Failure    Should Not Be Equal As Integers    ${code}    200
        ...    msg=Expected keep-one refusal (non-200), got ${resp}
    # At least one MON must still be present.
    ${mons}=    Get Mon Count
    Should Be True    ${mons} >= 1    msg=All MONs removed despite keep-one invariant
