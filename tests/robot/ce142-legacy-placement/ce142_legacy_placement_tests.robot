*** Settings ***
Documentation    ce142-legacy-placement
...    UAT for the CE142 S1 blocker fix: a fresh NON-deferred (legacy)
...    SimpleBootstrapper bootstrap must mark the cluster_lifecycle row as
...    bootstrapped, so that GET /1.0/placement reports bootstrapped and a
...    non-empty placement policy is accepted (not rejected with
...    "Ceph not bootstrapped") on a newly-created cluster.
...    Covers the regression that schemaUpdate8 only backfills pre-existing
...    clusters, leaving fresh legacy clusters reported as not_bootstrapped.
Resource        ../resources/microceph_harness.resource
Suite Setup     CE142 Legacy Placement Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       ce142    single-node    legacy    placement    lxd    integration

*** Keywords ***
CE142 Legacy Placement Suite Setup
    [Documentation]    Launch VM, install snap, and perform a standard (non-deferred)
    ...    bootstrap so the cluster is created on this revision.
    Launch Outer Test VM    vm_name=microceph-ce142-legacy-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install And Bootstrap MicroCeph

Wait For MicroCeph Control Socket
    [Documentation]    Polls until the microceph control socket exists in the VM.
    FOR    ${i}    IN RANGE    24
        ${ready}=    Run In VM    test -S /var/snap/microceph/common/state/control.socket && echo yes || echo no    15
        IF    "${ready.stdout.strip()}" == "yes"    RETURN
        Sleep    5s
    END
    Fail    MicroCeph control socket never appeared

Get Placement Bootstrap State
    [Documentation]    Returns the bootstrap_state field from GET /1.0/placement.
    ${json}=    Get Placement Status JSON
    RETURN    ${json}

*** Test Cases ***
Test Legacy Bootstrap Reports Bootstrapped
    [Documentation]    A fresh non-deferred bootstrap must set cluster_lifecycle to
    ...    bootstrapped. GET /1.0/placement reports bootstrap_state=bootstrapped,
    ...    ceph_bootstrapped=true.
    [Tags]    legacy    placement
    ${json}=    Get Placement Bootstrap State
    Should Contain    ${json}    "bootstrap_state":"bootstrapped"
    ...    msg=Fresh legacy cluster not reported as bootstrapped: ${json}
    Should Contain    ${json}    "ceph_bootstrapped":true
    ...    msg=ceph_bootstrapped not true on fresh legacy cluster: ${json}

Test Placement Accepted On Fresh Legacy Cluster
    [Documentation]    A non-empty placement policy PUT on a freshly-bootstrapped
    ...    legacy cluster must NOT be rejected with "Ceph not bootstrapped".
    ...    We PUT a policy that adds control on the local node and assert a 200
    ...    status_code (the control service may already be present, so this is
    ...    idempotent; the point is it is accepted, not rejected pre-bootstrap).
    [Tags]    placement
    ${hn}=    Get VM Hostname
    ${body}=    Set Variable    {"mode":"reconcile","members":{"${hn}":{"control":true}}}
    ${result}=    Run In VM And Check    sudo curl -s -X PUT --unix-socket /var/snap/microceph/common/state/control.socket -H 'Content-Type: application/json' -d '${body}' http://localhost/1.0/placement    120
    Should Contain    ${result.stdout}    "status_code":200
    ...    msg=Placement rejected on fresh legacy cluster (expected accepted): ${result.stdout}
    Should Not Contain    ${result.stdout}    "Ceph not bootstrapped"
    ...    msg=Placement rejected with pre-bootstrap guard on a bootstrapped cluster: ${result.stdout}

Test Ceph Only Bootstrap Is Idempotent On Legacy Cluster
    [Documentation]    On a fresh legacy cluster, `cluster bootstrap-ceph --target`
    ...    the local node must be a no-op success (the cluster is already
    ...    bootstrapped), NOT attempt a second bootstrap over the existing cluster.
    [Tags]    ceph-only-bootstrap
    ${hn}=    Get VM Hostname
    Run In VM And Check    sudo microceph cluster bootstrap-ceph --target ${hn}    120
    Run In VM And Check    sudo microceph.ceph status    30
