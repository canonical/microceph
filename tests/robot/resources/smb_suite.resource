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
    Run SMB Checked    0    microceph    disk    add    /dev/sdb    --wipe
    Run SMB Checked    1    microceph    disk    add    /dev/sdb    --wipe
    Run SMB Checked    2    microceph    disk    add    /dev/sdb    --wipe
    Wait For SMB Ceph Health
    Run SMB Checked    0    microceph.ceph    config    set    mgr    mgr/smb/update_orchestration    false
    Run SMB Checked    0    microceph.ceph    mgr    module    enable    smb
    Run SMB Checked    0    microceph.ceph    fs    volume    create    robotsmb
    Wait For SMB Ceph Health
    ${prepared}=    Prepare SMB Data Paths
    Should Be Equal As Integers    ${prepared.rc}    0    msg=${prepared.stderr}
    Run SMB Checked    0    microceph.ceph    mgr    module    enable    microceph
    Run SMB Checked    0    microceph.ceph    orch    set    backend    microceph

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

