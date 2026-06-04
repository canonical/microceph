*** Settings ***
Documentation    upgrade-reef-tests
...    Installs MicroCeph from reef/stable Snap Store channel on 4 containers, bootstraps,
...    adds 3 OSDs, enables and exercises RGW, then upgrades to the locally-built snap
...    and verifies the cluster remains healthy.
Resource        ../resources/microceph_harness.resource
Suite Setup     Upgrade Reef Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    upgrade    osd    rgw    lxd    slow    integration    deprecated

*** Keywords ***
Upgrade Reef Suite Setup
    Launch Outer Test VM    vm_name=microceph-ureef-vm    disk_size=50GiB
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Clear IPTables
    Free Runner Disk
    Setup LXD In VM
    Create LXD Containers With Loop Devices    public
    Install MicroCeph From Store On All Nodes    reef/stable
    Bootstrap Head Node
    Join Worker Nodes To Cluster

*** Test Cases ***
Test Add OSDs Before Upgrade
    [Documentation]    Adds 3 OSDs (node-wrk0..2) from the reef/stable build.
    [Tags]    osd
    Add OSD To Node    node-wrk0
    Add OSD To Node    node-wrk1
    Add OSD To Node    node-wrk2
    Wait For OSD Count Head    3

Test Enable RGW Before Upgrade
    [Documentation]    Enables RGW and exercises S3 access before upgrading.
    [Tags]    rgw
    Enable RGW Head Node
    Exercise RGW Head Node

Test Upgrade To Local Build
    [Documentation]    Installs the local snap build on all 4 containers and verifies that
    ...    all 3 OSDs remain up after each container's upgrade.
    [Tags]    upgrade
    Upgrade Multi Node

Test Cluster Healthy After Upgrade
    [Documentation]    Waits for all 3 OSDs to be up and the cluster to reach HEALTH_OK.
    [Tags]    upgrade    osd
    Sleep    30s
    Wait For OSD Count Head    3    60
    Verify Cluster Health Head Node

Test Exercise RGW After Upgrade
    [Documentation]    Exercises RGW S3 access after the upgrade to confirm data integrity.
    [Tags]    rgw    upgrade
    Exercise RGW Head Node

Test Status After Upgrade
    [Documentation]    Runs microceph status on node-wrk0 to confirm overall cluster visibility.
    ...    Retries up to 60s to allow the database to finish starting after upgrade.
    [Tags]    upgrade
    FOR    ${i}    IN RANGE    12
        ${result}=    Run In VM    lxc exec node-wrk0 -- sudo microceph status    30
        IF    ${result.rc} == 0    BREAK
        Sleep    5s
    END
    Should Be Equal As Integers    ${result.rc}    0    msg=microceph status failed: ${result.stderr}
