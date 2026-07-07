*** Settings ***
Documentation    deferred-ceph-single-tests
...    Single-node deferred-Ceph coverage:
...      deferred bootstrap leaves MicroCluster up but Ceph not bootstrapped,
...      capability markers are advertised, lifecycle state is not_bootstrapped, and
...      an empty-members placement policy is accepted as a no-op.
...    Legacy (non-deferred) bootstrap compatibility is covered by the existing
...    single-system-tests and cluster-tests suites running against the same snap.
Resource        ../resources/microceph_harness.resource
Suite Setup     Deferred Ceph Single Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    deferred    lxd    integration

*** Keywords ***
Deferred Ceph Single Suite Setup
    [Documentation]    Launch VM, install snap, and perform a SINGLE deferred bootstrap.
    ...    All tests assert against this one deferred-cluster state; none re-bootstrap
    ...    (microcluster refuses a second bootstrap with "Database is online").
    Launch Outer Test VM    vm_name=microceph-deferred-single-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install MicroCeph From Local Snap
    Log To Console    [deferred-ceph] Deferred bootstrap (single node)...
    Wait For MicroCeph Control Socket
    Run In VM And Check    sudo microceph cluster bootstrap --defer-ceph    120
    Sleep    5s
    Run In VM And Check    sudo microceph status    30

*** Test Cases ***
Test Deferred Bootstrap Leaves Ceph Not Bootstrapped
    [Documentation]    `microceph cluster bootstrap --defer-ceph` brought up MicroCluster/dqlite
    ...    and microcephd, but did NOT create FSID, admin keyring, ceph.conf, or MON/MGR/MDS.
    ...    (Bootstrap happened once in Suite Setup.)
    [Tags]    deferred
    ${hn}=    Get VM Hostname
    ${status}=    Run In VM And Check    sudo microceph cluster list    30
    Should Contain    ${status.stdout}    ${hn}    msg=Bootstrap node not in cluster list
    Run In VM Must Fail    sudo microceph.ceph status 2>/dev/null    30
    ${conf}=    Run In VM    test -f /var/snap/microceph/current/conf/ceph.conf && echo yes || echo no    15
    Should Be Equal As Strings    ${conf.stdout.strip()}    no
    ...    msg=ceph.conf exists but Ceph should not be bootstrapped in deferred mode

Test Lifecycle State Not Bootstrapped
    [Documentation]    GET /1.0/placement reports bootstrap_state=not_bootstrapped before Ceph-only bootstrap.
    [Tags]    deferred    api
    Assert Lifecycle State    not_bootstrapped

Test Capability Markers Advertised
    [Documentation]    Precondition: GET /1.0/cluster/capabilities advertises
    ...    the deferred-Ceph / placement capability markers so the charm can detect support.
    [Tags]    api    capabilities
    ${caps}=    Get Supported Capabilities
    List Should Contain Value    ${caps}    deferred-ceph-bootstrap
    ...    msg=deferred-ceph-bootstrap capability missing: ${caps}
    List Should Contain Value    ${caps}    ceph-only-bootstrap
    ...    msg=ceph-only-bootstrap capability missing: ${caps}
    List Should Contain Value    ${caps}    declarative-placement
    ...    msg=declarative-placement capability missing: ${caps}

Test Placement Policy Empty Members Is No Op
    [Documentation]    Precondition: PUT /1.0/placement with an empty members map performs
    ...    no service operations, is accepted, and is stored as the active policy. Ceph must
    ...    remain unbootstrapped.
    [Tags]    api    placement
    ${resp}=    MicroCeph API Put    placement    {"mode":"reconcile","members":{}}    timeout=30
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=Empty placement PUT failed: ${resp}
    ${active}=    Placement Policy Active
    Should Be True    ${active}    msg=Empty placement policy not stored as active
    Run In VM Must Fail    sudo microceph.ceph status 2>/dev/null    30
