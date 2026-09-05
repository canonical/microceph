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

Get Public Network IP
    [Documentation]    The container's address on the LXD public network, which is the
    ...    address ceph.conf's mon host carries. `hostname -I` lists the management
    ...    address first, so the public one is selected by subnet.
    [Arguments]    ${container}
    ${cidr}=    Get Public Network CIDR
    ${res}=    Run In Container    ${container}    hostname -I    30
    ${ip}=    Evaluate    next((a for a in """${res.stdout}""".split() if ipaddress.ip_address(a) in ipaddress.ip_network("""${cidr}""", strict=False)), "")    modules=ipaddress
    Should Not Be Empty    ${ip}    msg=No address of ${container} on public network ${cidr}: ${res.stdout}
    RETURN    ${ip}

Pin Mon Host To Self
    [Documentation]    Overwrite the container's ceph.conf `mon host` line with only its own
    ...    address, reproducing a render from before a peer MON was added. The daemon's
    ...    refresh loop only re-renders when the MON list changes, so once it has rendered
    ...    the current list the pin holds until service teardown renders the file itself.
    [Arguments]    ${container}
    ${ip}=    Get Public Network IP    ${container}
    Run In Container    ${container}    sed -i 's/^mon host = .*/mon host = ${ip}/' /var/snap/microceph/current/conf/ceph.conf    30
    ${pinned}=    Get Ceph Conf Value    ${container}    mon host
    Should Be Equal As Strings    ${pinned}    ${ip}    msg=Failed to pin mon host on ${container}: ${pinned}

Assert Mon Host Includes
    [Documentation]    The container's rendered ceph.conf `mon host` line names the given address.
    [Arguments]    ${container}    ${peer_ip}
    ${hosts}=    Get Ceph Conf Value    ${container}    mon host
    Should Contain    ${hosts}    ${peer_ip}    msg=mon host on ${container} does not list ${peer_ip}: ${hosts}

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

Test Declarative Control Placement Migration Preserves Quorum
    [Documentation]    Moving the only desired control placement from the bootstrap member
    ...    to its replacement must commit MON membership removal before stopping the source.
    ...    The destination remains a one-MON quorum and repeating the policy is idempotent.
    ...    Once the source's refresh loop has rendered the destination MON, the source's
    ...    ceph.conf is pinned to list only its own MON, as a render from before the add
    ...    would. Nothing rewrites that pin before teardown, so the removal verify on the
    ...    source only succeeds if service teardown re-renders mon host before `ceph mon rm`;
    ...    otherwise it hangs on the exited local MON until its deadline kills it.
    [Tags]    placement    migration
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"node-wrk0":{"control":true},"node-wrk1":{"control":false}}}
    ${dst_ip}=    Get Public Network IP    node-wrk0
    # The refresh loop renders the destination MON into the source's conf within a
    # minute of the add and then stays idle until the MON list changes again.
    Wait Until Keyword Succeeds    90s    5s    Assert Mon Host Includes    node-wrk1    ${dst_ip}
    Pin Mon Host To Self    node-wrk1
    ${start}=    Get Time    epoch
    ${resp}=    MicroCeph API Put In Container    node-wrk0    placement    ${policy}
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=Control migration failed: ${resp}
    ${end}=    Get Time    epoch
    ${elapsed}=    Evaluate    ${end} - ${start}
    Should Be True    ${elapsed} < 25    msg=Migration took ${elapsed}s: the MON removal verify ran against a stale mon host and timed out
    Assert Mon Host Includes    node-wrk1    ${dst_ip}
    ${mons}=    Get Mon Count
    Should Be Equal As Integers    ${mons}    1    msg=Expected destination-only monmap after migration
    Assert Mon Quorum Members    node-wrk0
    Assert Member Has Control Services    node-wrk0    yes
    # The source's MON/MDS/MGR teardown commits the daemon stop, map eviction,
    # and DB removal, but the mgrmap/fsmap may take a moment to converge (or up
    # to the beacon-aging window if an eviction was a no-op). Poll for absence
    # rather than asserting a single-shot snapshot, mirroring the add path's
    # Wait For Mon Count.
    Wait For Member Control Services    node-wrk1    no

    # A second reconcile must observe the completed migration as a no-op.
    ${retry}=    MicroCeph API Put In Container    node-wrk0    placement    ${policy}
    ${retry_code}=    Response Status Code    ${retry}
    Should Be Equal As Integers    ${retry_code}    200    msg=Idempotent migration retry failed: ${retry}
    Assert Mon Quorum Members    node-wrk0

Test Declarative Control Placement Keep One Invariant
    [Documentation]    A placement that would remove the last control service must be
    ...    rejected with a clear keep-one reason (HTTP non-2xx / error), and the last MON must
    ...    remain. We request control:false on the only control member while no other control
    ...    member exists.
    [Tags]    placement
    # node-wrk0 is the only control member after the preceding migration.
    # Include the already-drained bootstrap member as false as an idempotency check.
    ${resp}=    MicroCeph API Put In Container    node-wrk0    placement    {"mode":"reconcile","members":{"node-wrk0":{"control":false},"node-wrk1":{"control":false}}}
    ${code}=    Response Status Code    ${resp}
    Run Keyword And Continue On Failure    Should Not Be Equal As Integers    ${code}    200
        ...    msg=Expected keep-one refusal (non-200), got ${resp}
    # At least one MON must still be present.
    ${mons}=    Get Mon Count
    Should Be True    ${mons} >= 1    msg=All MONs removed despite keep-one invariant
