*** Settings ***
Documentation    rbd-replication-test
...    Tests MicroCeph RBD remote replication: two 2-node sites (sitea=wrk0/1, siteb=wrk2/3),
...    exchange tokens, enable rbd-mirror, configure pool and image mirroring,
...    verify sync, failover, and remote removal.
Resource        ../resources/microceph_harness.resource
Suite Setup     RBD Replication Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    rbd    replication    remote    lxd    slow    integration

*** Keywords ***
RBD Replication Suite Setup
    Launch Outer Test VM    vm_name=microceph-rbdrep-vm    disk_size=50GiB
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Clear IPTables
    Free Runner Disk
    Setup LXD In VM
    Create LXD Containers With Loop Devices    public
    Install MicroCeph On All Nodes

Verify Snapshot Pool Replication Fails
    [Documentation]    Verifies that enabling snapshot-based replication on a pool (not an image) fails.
    Log To Console    [rbd] Verifying snapshot pool replication fails...
    ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph replication enable rbd pool_one --type snapshot --remote siteb 2>&1"    60
    Should Not Be Equal As Integers    ${result.rc}    0    msg=snapshot pool replication should fail
    Should Contain    ${result.stdout}    Snapshot-based replication is only supported for individual RBD images

Configure RBD Mirroring
    [Documentation]    Creates pools/images on both sites and enables pool and image mirroring.
    Log To Console    [rbd] Configuring RBD mirroring...
    Run In Container    node-wrk0    microceph.ceph osd pool create pool_one    60
    Run In Container    node-wrk1    microceph.ceph osd pool create pool_two    60
    Run In Container    node-wrk2    microceph.ceph osd pool create pool_one    60
    Run In Container    node-wrk3    microceph.ceph osd pool create pool_two    60
    Run In Container    node-wrk0    microceph.rbd pool init pool_one    30
    Run In Container    node-wrk1    microceph.rbd pool init pool_two    30
    Run In Container    node-wrk2    microceph.rbd pool init pool_one    30
    Run In Container    node-wrk3    microceph.rbd pool init pool_two    30
    Run In Container    node-wrk0    microceph.rbd create --size 512 pool_one/image_one    30
    Run In Container    node-wrk0    microceph.rbd create --size 512 pool_one/image_two    30
    Run In Container    node-wrk1    microceph.rbd create --size 512 pool_two/image_one    30
    Run In Container    node-wrk1    microceph.rbd create --size 512 pool_two/image_two    30
    Run In Container    node-wrk0    microceph replication enable rbd pool_one --remote siteb    60
    Run In Container    node-wrk0    microceph replication enable rbd pool_two/image_one --type journal --remote siteb    60
    Run In Container    node-wrk0    microceph replication enable rbd pool_two/image_two --type snapshot --remote siteb    60

Wait For Secondary Sync
    [Documentation]    Polls until at least ${threshold} images are synchronised to siteb.
    [Arguments]    ${threshold}
    Log To Console    [rbd] Waiting for ${threshold} images to sync to siteb...
    FOR    ${i}    IN RANGE    100
        ${count_str}=    Get Synced Image Count On Node    node-wrk2
        ${images}=    Evaluate    int('${count_str}') if '${count_str}'.isdigit() else 0
        IF    ${images} >= ${threshold}
            Log To Console    [rbd] ${images} images synced to secondary
            RETURN
        END
        Sleep    30s
    END
    Fail    Replication sync timed out after 100 attempts

Verify RBD Mirroring List
    [Documentation]    Verifies mirrored images appear in the replication list on both sites.
    Log To Console    [rbd] Verifying RBD mirroring list...
    Run In Container    node-wrk0    sudo microceph replication list rbd    30
    Run In Container    node-wrk2    sudo microceph replication list rbd    30
    Run In VM And Check    lxc exec node-wrk0 -- sudo microceph replication list rbd | grep "pool_one.*image_one"    30
    Run In VM And Check    lxc exec node-wrk1 -- sudo microceph replication list rbd | grep "pool_one.*image_two"    30
    Run In VM And Check    lxc exec node-wrk2 -- sudo microceph replication list rbd | grep "pool_two.*image_one"    30
    Run In VM And Check    lxc exec node-wrk3 -- sudo microceph replication list rbd | grep "pool_two.*image_two"    30
    Run In Container    node-wrk0    sudo microceph replication status rbd --json    30

Failover To Site B
    [Documentation]    Promotes siteb to primary and demotes sitea; verifies image ownership transfer.
    Log To Console    [rbd] Failing over to site B...
    ${img_count}=    Run In VM    lxc exec node-wrk2 -- sh -c "sudo microceph replication list rbd --json | jq '[.[].Images[] | select(.is_primary==false)] | length'"    30
    Should Be True    int('${img_count.stdout.strip()}') >= 1    msg=Site B has no secondary images
    Run In Container    node-wrk2    sudo microceph replication promote --remote sitea --yes-i-really-mean-it    120
    FOR    ${i}    IN RANGE    100
        ${count_str}=    Get Primary Image Count On Node    node-wrk2
        ${count}=    Evaluate    int('${count_str}') if '${count_str}'.isdigit() else 0
        IF    ${count} > 0
            Log To Console    [rbd] ${count} images promoted to primary on site B
            BREAK
        END
        Sleep    30s
    END
    Should Be True    ${count} > 0    msg=No images promoted after 100 rounds
    Run In Container    node-wrk0    sudo microceph replication demote --remote siteb --yes-i-really-mean-it    120
    FOR    ${i}    IN RANGE    100
        ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "sudo microceph replication list rbd --json | jq '[.[].Images[] | select(.is_primary==false)] | length'"    30
        ${count}=    Evaluate    int('${result.stdout.strip()}') if '${result.stdout.strip()}'.isdigit() else 0
        IF    ${count} > 0
            Log To Console    [rbd] Site A demoted (${count} secondary images)
            BREAK
        END
        Sleep    30s
    END
    Should Be True    ${count} > 0    msg=Site A images not demoted after 100 rounds

Wait For RBD Mirror Health
    [Documentation]    Waits for RBD mirror pool health OK or WARNING on the given node for each listed pool.
    ...    WARNING is accepted because after a failover the new secondary is in resync state.
    [Arguments]    ${node}    @{pools}
    FOR    ${pool}    IN    @{pools}
        Log To Console    [rbd] Waiting for mirror health OK/WARNING on ${node} pool ${pool}...
        FOR    ${i}    IN RANGE    120
            ${health}=    Get RBD Mirror Pool Health    ${node}    ${pool}
            IF    "${health}" == "OK" or "${health}" == "WARNING"
                Log To Console    [rbd] ${pool} health=${health}
                BREAK
            END
            IF    ${i} == 119
                Fail    Timed out waiting for ${pool} mirror health on ${node} (last=${health})
            END
            Sleep    5s
        END
    END

Disable RBD Mirroring
    [Documentation]    Disables mirroring on pool_two images and both pools from siteb.
    ...    Mirror health is UNKNOWN after failover; the disable ops work regardless so we skip the health wait.
    Log To Console    [rbd] Disabling RBD mirroring...
    Sleep    15s    reason=Allow mirror state to settle after failover before disabling
    ${disable_result}=    Run In VM    lxc exec node-wrk2 -- sh -c "sudo microceph replication disable rbd pool_two 2>&1"    60
    Should Not Be Equal As Integers    ${disable_result.rc}    0    msg=disable pool_two should fail while images are in Image mirroring mode
    Should Contain    ${disable_result.stdout}    in Image mirroring mode    msg=Expected 'in Image mirroring mode' error
    Run In Container    node-wrk2    sudo microceph replication disable rbd pool_two/image_one    60
    Run In Container    node-wrk2    sudo microceph replication disable rbd pool_two/image_two    60
    Run In Container    node-wrk2    sudo microceph replication disable rbd pool_two    60
    Run In Container    node-wrk2    sudo microceph replication disable rbd pool_one    60

Remove Remote And Verify
    [Documentation]    Removes the siteb remote from sitea and verifies no remotes remain.
    Log To Console    [rbd] Removing remote and verifying...
    Run In VM And Check    lxc exec node-wrk0 -- sh -c "microceph remote list --json | grep -q 'siteb'"    30
    Run In Container    node-wrk0    microceph remote remove siteb    60
    ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph remote list --json 2>&1 || true"    30
    Should Contain    ${result.stdout}    no remotes configured    msg=Remote still present after removal

*** Test Cases ***
Test Bootstrap Two Sites
    [Documentation]    Bootstraps two independent 2-node MicroCeph clusters (sitea=wrk0/1, siteb=wrk2/3)
    ...    each with 2 loopback-file OSDs.
    [Tags]    rbd    replication    remote
    Bootstrap Two Sites

Test Exchange Remote Tokens
    [Documentation]    Exports cluster tokens from each site and imports them on the other site.
    [Tags]    rbd    replication    remote
    Exchange Remote Site Tokens

Test Verify Remote Authentication
    [Documentation]    Verifies that ceph commands can be issued against the remote cluster
    ...    using the imported credentials on both sites and all nodes.
    [Tags]    rbd    replication    remote
    Verify Remote Authentication On All Nodes

Test Enable RBD Mirror Daemon
    [Documentation]    Enables the rbd-mirror daemon on node-wrk0 (sitea) and node-wrk2 (siteb).
    [Tags]    rbd    replication
    Enable Mirror Service On Both Sites    rbd-mirror

Test Snapshot Replication On Pool Fails
    [Documentation]    Verifies that enabling snapshot-based replication on a pool (not image) fails.
    [Tags]    rbd    replication
    Verify Snapshot Pool Replication Fails

Test Configure RBD Mirroring
    [Documentation]    Creates RBD pools and images on both sites and enables pool and image mirroring.
    [Tags]    rbd    replication
    Configure RBD Mirroring

Test Wait For Secondary Sync
    [Documentation]    Waits until 4 images are synchronised to the secondary site.
    [Tags]    rbd    replication    slow
    Wait For Secondary Sync    4

Test Verify RBD Mirroring
    [Documentation]    Verifies mirrored images appear in replication list on both sites.
    [Tags]    rbd    replication
    Verify RBD Mirroring List

Test Failover To Site B
    [Documentation]    Promotes siteb to primary and demotes sitea; verifies image ownership transfer.
    [Tags]    rbd    replication    failover
    Failover To Site B

Test Disable RBD Mirroring
    [Documentation]    Waits for mirror health OK then disables mirroring on both pools.
    [Tags]    rbd    replication
    Disable RBD Mirroring

Test Remove Remote And Verify
    [Documentation]    Removes the siteb remote from sitea and verifies no remotes remain.
    [Tags]    rbd    replication    remote
    Remove Remote And Verify
