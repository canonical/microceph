*** Settings ***
Resource    microceph_harness.resource
Library     smb_ops.py

*** Keywords ***
Run SMB Checked
    [Arguments]    ${node}    @{command}
    ${result}=    SMB Node Command    ${node}    @{command}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}\n${result.stderr}
    RETURN    ${result.stdout}

SMB Suite Setup
    [Timeout]    30 minutes
    Provision SMB Lab
    Run SMB Checked    0    microceph    cluster    bootstrap
    ${join}=    Join SMB Member    1
    Should Be Equal As Integers    ${join.rc}    0    msg=First member join failed; no retry performed
    ${join}=    Join SMB Member    2
    Should Be Equal As Integers    ${join.rc}    0    msg=Second member join failed; no retry performed
    Add Verified SMB OSD    0
    Add Verified SMB OSD    1
    Add Verified SMB OSD    2
    Require SMB Ceph Health
    Run SMB Checked    0    microceph.ceph    config    set    mgr    mgr/smb/update_orchestration    false
    Run SMB Checked    0    microceph.ceph    mgr    module    enable    smb
    Run SMB Checked    0    microceph.ceph    fs    volume    create    robotsmb
    Require SMB Ceph Health
    ${prepared}=    Prepare SMB Data Paths
    Should Be Equal As Integers    ${prepared.rc}    0    msg=${prepared.stderr}
    Run SMB Checked    0    microceph.ceph    mgr    module    enable    microceph
    Run SMB Checked    0    microceph.ceph    orch    set    backend    microceph

Add Verified SMB OSD
    [Arguments]    ${node}
    ${disk}=    SMB OSD Path    ${node}
    Should Not Be Empty    ${disk}    msg=No exact owned 4GiB OSD identity; refusing disk add
    Run SMB Checked    ${node}    microceph    disk    add    ${disk}    --wipe

Require SMB Ceph Health
    # The owning gate reports clock skew but rejects other health/placement failures.
    Wait For SMB Ceph Health

SMB Suite Cleanup
    TRY
        Collect SMB Diagnostics
    FINALLY
        Destroy SMB Lab
    END

SMB Failure Diagnostics
    IF    '${TEST STATUS}' == 'FAIL'
        Collect SMB Diagnostics
    END

Require Clustered Deployment
    Skip If    not $SMB_DEPLOYED    Clustered deployment prerequisite failed; see its first failure

Enable And Check SMB Member
    [Arguments]    ${node}    ${cluster}=robot-smb    ${port}=1445    ${clustered}=${True}
    Run SMB Checked    ${node}    microceph    enable    smb    --cluster-id    ${cluster}    --wait=true
    # Do not poll away a post-enable applied-error (UPDATE-01).
    ${state}=    SMB Snapshot    ${node}    ${cluster}
    ${ready}=    SMB Snapshot Is Ready    ${state}    ${cluster}    ${port}    ${clustered}
    Should Be True    ${ready}    msg=Synchronous enable returned without clean readiness: ${state}

SMB Exact Transfer
    [Arguments]    ${node}    ${file}    ${upload}=${True}    ${cluster}=robot-smb
    ${result}=    SMB Client Transfer    ${node}    ${file}    ${upload}    alice    ${cluster}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stdout}\n${result.stderr}
    ${compare}=    Compare SMB Payload
    Should Be Equal As Integers    ${compare.rc}    0    msg=SMB content differs from the exact source payload

Check Retired SMB Member
    [Arguments]    ${node}    ${cluster}=robot-smb
    ${state}=    SMB Snapshot    ${node}    ${cluster}
    Should Be Empty    ${state}[owner]
    Should Be Equal    ${state}[roles][smbd][ActiveState]    inactive
    Should Be Equal    ${state}[roles][smbmetrics][ActiveState]    inactive
    Should Be Equal    ${state}[roles][ctdbd][ActiveState]    inactive
    Should Be Equal    ${state}[roles][ctdb-nodes][ActiveState]    inactive
    Should Be Equal    ${state}[roles][winbindd][ActiveState]    inactive

SMB Shared Lab Setup
    [Timeout]    40 minutes
    Require SMB AD Capacity
    SMB Suite Setup
    Provision SMB AD Directory
    Require SMB Ceph Health

SMB Scenario Setup
    [Arguments]    ${kind}
    [Timeout]    40 minutes
    ${shared}=    SMB Lab Is Shared
    IF    $shared
        Use SMB Lab
        Verify SMB Lab
    ELSE
        IF    '${kind}' == 'ad'
            Require SMB AD Capacity
        END
        SMB Suite Setup
        IF    '${kind}' == 'ad'
            Provision SMB AD Directory
        END
    END
    Assert SMB Scenario Idle
    Require SMB Ceph Health
    IF    '${kind}' == 'ad'
        Fresh SMB AD Inputs
        ${prepared}=    Prepare SMB AD Data    0    0
        Should Be Equal As Integers    ${prepared.rc}    0    msg=${prepared.stderr}
    END

SMB Scenario Cleanup
    TRY
        Collect SMB Diagnostics
    FINALLY
        TRY
            Verify SMB Lab
            Restore SMB Directory
            Retire SMB Scenario
        FINALLY
            ${shared}=    SMB Lab Is Shared
            IF    not $shared
                Destroy SMB Lab
            END
        END
    END

