*** Settings ***
Documentation    single-node basic tests
...    Verifies waitready lifecycle and the Orchestrator module on a single-node cluster.
Resource        ../resources/microceph_harness.resource
Suite Setup     Single Node Basic Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    smoke    waitready    mgr    lxd    fast

*** Keywords ***
Single Node Basic Suite Setup
    Launch Outer Test VM    vm_name=microceph-sn-basic-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools

*** Test Cases ***
Test Waitready Pre Bootstrap
    [Documentation]    Installs snap and verifies microceph waitready fails before bootstrap.
    [Tags]    waitready
    Install MicroCeph From Local Snap
    Run In VM Must Fail    sudo microceph waitready --timeout 5 2>/dev/null

Test Waitready Post Bootstrap
    [Documentation]    Bootstraps the cluster and verifies waitready succeeds.
    [Tags]    waitready
    Bootstrap MicroCeph Cluster
    Run In VM And Check    sudo microceph waitready --timeout 30    60

Test Orchestrator Module
    [Documentation]    Enables and verifies the MicroCeph Orchestrator module.
    [Tags]    mgr
    Run In VM And Check    sudo microceph.ceph mgr module enable microceph    30
    Run In VM And Check    sudo microceph.ceph orch set backend microceph    30
    ${hn}=    Get VM Hostname
    Run In VM And Check    sudo microceph.ceph orch host ls | grep -F ${hn}    30
    Run In VM And Check    sudo microceph.ceph orch ls | grep -F "mon" | grep -F ${hn}    30
