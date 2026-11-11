*** Settings ***
Documentation    Ordered clustered SMB scenarios; no singleton/AD suite prerequisite.
Resource        ../resources/smb_suite.resource
Suite Setup     SMB Scenario Setup    clustered
Suite Teardown  SMB Scenario Cleanup
Test Teardown   SMB Failure Diagnostics
Test Timeout    15 minutes
Test Tags       smb    multi-node    lxd    slow    integration

*** Variables ***
${SMB_DEPLOYED}          ${False}

*** Test Cases ***
Deploy Clustered SMB And Authenticate Through Every Gateway
    ${applied}=    Apply SMB Resource    clustered
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    Enable And Check SMB Member    0
    Enable And Check SMB Member    1
    Enable And Check SMB Member    2
    SMB Exact Transfer    0    initial-0.bin
    SMB Exact Transfer    1    initial-1.bin
    SMB Exact Transfer    2    initial-2.bin
    SMB Exact Transfer    1    initial-0.bin    ${False}
    ${denied}=    SMB Client Transfer    0    initial-0.bin    ${False}    wrong
    Should Not Be Equal As Integers    ${denied.rc}    0
    Should Contain    ${denied.stdout}${denied.stderr}    NT_STATUS_LOGON_FAILURE
    Set Suite Variable    ${SMB_DEPLOYED}    ${True}

Refresh Endpoints And Prove Stable Unchanged Enable
    Require Clustered Deployment
    ${before}=    SMB Snapshots    robot-smb    0    1    2
    ${ctdb_before}=    SMB Role Tokens    ${before}    ctdbd
    ${applied}=    Apply SMB Resource    clustered-refresh
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    Wait For SMB Ready    0    port=2445
    Wait For SMB Ready    1    port=2445
    Wait For SMB Ready    2    port=2445
    ${after}=    SMB Snapshots    robot-smb    0    1    2
    ${ctdb_after}=    SMB Role Tokens    ${after}    ctdbd
    Should Be Equal    ${ctdb_after}    ${ctdb_before}    msg=Endpoint refresh restarted CTDB
    SMB Exact Transfer    0    refreshed.bin
    SMB Exact Transfer    2    refreshed.bin    ${False}
    SMB Exact Transfer    1    initial-0.bin    ${False}
    Enable And Check SMB Member    0    port=2445
    Enable And Check SMB Member    2    port=2445
    ${observations}=    Collect SMB Observations    35
    ${stable}=    SMB Observations Are Stable    ${after}    ${observations}
    Should Be True    ${stable}    msg=Unchanged enable/updater changed processes, applied state or reported an error
    SMB Exact Transfer    2    refreshed.bin    ${False}

Retire And Rejoin Once With Survivor IO
    Require Clustered Deployment
    ${before}=    Wait For SMB Ready    2
    Should Not Be Empty    ${before}[member]
    Should Not Be Empty    ${before}[rank]
    Run SMB Checked    2    microceph    disable    smb    --cluster-id    robot-smb
    Check Retired SMB Member    2
    Wait For SMB Ready    0
    Wait For SMB Ready    1
    SMB Exact Transfer    0    survivor.bin
    SMB Exact Transfer    1    survivor.bin    ${False}
    ${port}=    Set Variable    ${before}[context][smb_port]
    Enable And Check SMB Member    2    port=${port}
    ${after}=    SMB Snapshot    2
    Should Be Equal    ${after}[member]    ${before}[member]
    Should Be Equal    ${after}[rank]    ${before}[rank]
    SMB Exact Transfer    2    rejoined.bin
    SMB Exact Transfer    0    rejoined.bin    ${False}

Daemon Restart Does Not Restart Serving Roles
    Require Clustered Deployment
    Wait For SMB Ready    0
    ${before}=    SMB Snapshots    robot-smb    0
    ${roles_before}=    SMB Role Tokens    ${before}
    ${daemon_before}=    SMB Role Tokens    ${before}    daemon
    Run SMB Checked    0    snap    restart    microceph.daemon
    Wait For SMB Ready    0
    ${after}=    SMB Snapshots    robot-smb    0
    ${roles_after}=    SMB Role Tokens    ${after}
    ${daemon_after}=    SMB Role Tokens    ${after}    daemon
    Should Be Equal    ${roles_after}    ${roles_before}
    Should Not Be Equal    ${daemon_after}    ${daemon_before}
    SMB Exact Transfer    0    initial-0.bin    ${False}

Ordinary VM Reboot Recovers SMB Without Operator Intervention
    Require Clustered Deployment
    ${before}=    Wait For SMB Ready    2
    Should Not Be Empty    ${before}[boot_id]
    Reboot SMB VM    2
    # Observation only: no snap start/restart, re-enable or retry of the reboot.
    ${after}=    Wait For SMB Ready    2
    Should Not Be Empty    ${after}[boot_id]
    Should Not Be Equal    ${after}[boot_id]    ${before}[boot_id]
    Wait For SMB Ceph Health
    SMB Exact Transfer    2    after-reboot.bin
    SMB Exact Transfer    0    after-reboot.bin    ${False}

Purge Clustered Member Without Affecting Peers Or Shared State
    Require Clustered Deployment
    ${before}=    Capture SMB Shared State    robot-smb
    Length Should Be    ${before.resources}    2
    ${expected}=    SMB Payload Digest
    ${data_before}=    SMB Backing File Digest    initial-0.bin    robot-smb
    Should Be Equal    ${data_before}    ${expected}
    ${peer}=    SMB Snapshots    robot-smb    1
    ${peer_tokens}=    SMB Role Tokens    ${peer}    smbd    ctdbd
    Run SMB Checked    2    microceph    disable    smb    --cluster-id    robot-smb    --purge
    Check Retired SMB Member    2
    ${removed}=    SMB Snapshot    2
    Should Not Be True    ${removed}[local_exists]
    Wait For SMB Ready    0
    Wait For SMB Ready    1
    SMB Exact Transfer    1    initial-0.bin    ${False}
    ${peer_after}=    SMB Snapshots    robot-smb    1
    ${peer_after_tokens}=    SMB Role Tokens    ${peer_after}    smbd    ctdbd
    Should Be Equal    ${peer_after_tokens}    ${peer_tokens}
    ${after}=    Capture SMB Shared State    robot-smb
    Should Be Equal    ${after}    ${before}    msg=Shared resources/authentication changed during clustered local purge
    ${data_after}=    SMB Backing File Digest    initial-0.bin    robot-smb
    Should Be Equal    ${data_after}    ${data_before}

*** Keywords ***
SMB Scenario Setup
    [Arguments]    ${mode}
    SMB Suite Setup

SMB Scenario Cleanup
    SMB Suite Cleanup

Require Clustered Deployment
    Should Be True    ${SMB_DEPLOYED}    msg=Clustered deployment prerequisite failed

Enable And Check SMB Member
    [Arguments]    ${node}    ${port}=1445
    smb_suite.Enable And Check SMB Member    ${node}    robot-smb    ${port}

SMB Exact Transfer
    [Arguments]    ${node}    ${file}    ${upload}=${True}
    smb_suite.SMB Exact Transfer    ${node}    ${file}    ${upload}    robot-smb

SMB Client Transfer
    [Arguments]    ${node}    ${file}    ${upload}=${True}    ${auth}=alice    ${cluster}=robot-smb
    ${result}=    smb_ops.SMB Client Transfer    ${node}    ${file}    ${upload}    ${auth}    ${cluster}
    RETURN    ${result}

SMB Snapshot
    [Arguments]    ${node}    ${cluster}=robot-smb
    ${result}=    smb_ops.SMB Snapshot    ${node}    ${cluster}
    RETURN    ${result}

Wait For SMB Ready
    [Arguments]    ${node}    ${port}=${None}
    ${result}=    smb_ops.Wait For SMB Ready    ${node}    robot-smb    ${port}
    RETURN    ${result}

Check Retired SMB Member
    [Arguments]    ${node}
    smb_suite.Check Retired SMB Member    ${node}    robot-smb
    ${state}=    SMB Snapshot    ${node}
    Should Be Equal    ${state}[roles][ctdbd][ActiveState]    inactive
