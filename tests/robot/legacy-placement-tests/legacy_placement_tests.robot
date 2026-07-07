*** Settings ***
Documentation    legacy-placement-tests
...    Verifies that a fresh NON-deferred (legacy) SimpleBootstrapper bootstrap
...    marks the cluster_lifecycle row as bootstrapped, so that GET /1.0/placement
...    reports bootstrapped and a non-empty placement policy is accepted (not
...    rejected with "Ceph not bootstrapped") on a newly-created cluster.
...    Covers the regression that schemaUpdate8 only backfills pre-existing
...    clusters, leaving fresh legacy clusters reported as not_bootstrapped.
Resource        ../resources/microceph_harness.resource
Suite Setup     Legacy Placement Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    legacy    placement    lxd    integration

*** Keywords ***
Legacy Placement Suite Setup
    [Documentation]    Launch VM, install snap, and perform a standard (non-deferred)
    ...    bootstrap so the cluster is created on this revision.
    Launch Outer Test VM    vm_name=microceph-legacy-placement-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install And Bootstrap MicroCeph

*** Test Cases ***
Test Legacy Bootstrap Reports Bootstrapped
    [Documentation]    A fresh non-deferred bootstrap must set cluster_lifecycle to
    ...    bootstrapped. GET /1.0/placement reports bootstrap_state=bootstrapped.
    [Tags]    legacy    placement
    Assert Lifecycle State    bootstrapped

Test Placement Accepted On Fresh Legacy Cluster
    [Documentation]    A non-empty placement policy PUT on a freshly-bootstrapped
    ...    legacy cluster must NOT be rejected with "Ceph not bootstrapped".
    ...    We PUT a policy that adds control on the local node and assert a 200
    ...    status_code (the control service may already be present, so this is
    ...    idempotent; the point is it is accepted, not rejected pre-bootstrap).
    [Tags]    placement
    ${hn}=    Get VM Hostname
    ${resp}=    MicroCeph API Put    placement    {"mode":"reconcile","members":{"${hn}":{"control":true}}}    timeout=120
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200
    ...    msg=Placement rejected on fresh legacy cluster (expected accepted): ${resp}
    Should Not Contain    ${resp}    Ceph not bootstrapped
    ...    msg=Placement rejected with pre-bootstrap guard on a bootstrapped cluster: ${resp}

Test Ceph Only Bootstrap Is Idempotent On Legacy Cluster
    [Documentation]    On a fresh legacy cluster, `cluster bootstrap-ceph --target`
    ...    the local node must be a no-op success (the cluster is already
    ...    bootstrapped), NOT attempt a second bootstrap over the existing cluster.
    [Tags]    ceph-only-bootstrap
    ${hn}=    Get VM Hostname
    Run In VM And Check    sudo microceph cluster bootstrap-ceph --target ${hn}    120
    Run In VM And Check    sudo microceph.ceph status    30
