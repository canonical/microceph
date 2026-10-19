*** Settings ***
Documentation    Native SMB integration on three real VMs and the supplied CI snap.
...    One request per mutation. Polls observe convergence only; no retry-to-green.
...    VM0 is also the SMB client. Run the whole ordered suite, not individual cases.
Resource        ../resources/microceph_harness.resource
Library         ../resources/smb_ops.py
Suite Setup     SMB Suite Setup
Suite Teardown  SMB Suite Cleanup
Test Teardown   SMB Failure Diagnostics
Test Timeout    15 minutes
Test Tags       smb    multi-node    lxd    slow    integration

*** Variables ***
${SMB_VM_IMAGE}          ubuntu:26.04
${SMB_SNAPD_CHANNEL}     latest/stable
${SMB_DEPLOYED}          ${False}
${SMB_NONCLUSTERED}      ${False}

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
    ${ctdb_before}=    SMB Role Tokens    ${before}    ctdbd    ctdb-nodes
    ${applied}=    Apply SMB Resource    refresh
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    Wait For SMB Ready    0    port=2445
    Wait For SMB Ready    1    port=2445
    Wait For SMB Ready    2    port=2445
    ${after}=    SMB Snapshots    robot-smb    0    1    2
    ${ctdb_after}=    SMB Role Tokens    ${after}    ctdbd    ctdb-nodes
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

Deploy Independent Gateways On A Separate Data Path
    Require Clustered Deployment
    # The prior reboot verdict is retained. This is explicit fixture retirement,
    # not a repair/retry of that test; independent mode coverage can still run.
    Retire SMB Fixture Placements
    ${applied}=    Apply SMB Resource    nonclustered
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    Enable And Check SMB Member    0    robot-never    3445    ${False}
    Enable And Check SMB Member    1    robot-never    3445    ${False}
    SMB Exact Transfer    0    never-0.bin    cluster=robot-never
    SMB Exact Transfer    1    never-1.bin    cluster=robot-never
    ${before}=    SMB Snapshots    robot-never    0    1
    ${tokens}=    SMB Role Tokens    ${before}    smbd    smbmetrics
    Enable And Check SMB Member    1    robot-never    3445    ${False}
    ${after}=    SMB Snapshots    robot-never    0    1
    ${after_tokens}=    SMB Role Tokens    ${after}    smbd    smbmetrics
    Should Be Equal    ${after_tokens}    ${tokens}
    Set Suite Variable    ${SMB_NONCLUSTERED}    ${True}

Purge Only The Stopped Local Gateway State
    Skip If    not $SMB_NONCLUSTERED    Independent gateway prerequisite failed
    ${before}=    Capture SMB Shared State
    Length Should Be    ${before.resources}    2
    ${expected}=    SMB Payload Digest
    ${data_before}=    SMB Backing File Digest    never-0.bin
    Should Be Equal    ${data_before}    ${expected}
    ${peer}=    SMB Snapshots    robot-never    1
    ${peer_tokens}=    SMB Role Tokens    ${peer}    smbd    smbmetrics
    Run SMB Checked    0    microceph    disable    smb    --cluster-id    robot-never    --purge
    Check Retired SMB Member    0    robot-never
    ${removed}=    SMB Snapshot    0    robot-never
    Should Not Be True    ${removed}[local_exists]
    Wait For SMB Ready    1    robot-never    3445    ${False}
    SMB Exact Transfer    1    never-1.bin    ${False}    robot-never
    ${peer_after}=    SMB Snapshots    robot-never    1
    ${peer_after_tokens}=    SMB Role Tokens    ${peer_after}    smbd    smbmetrics
    Should Be Equal    ${peer_after_tokens}    ${peer_tokens}
    ${after}=    Capture SMB Shared State
    Should Be Equal    ${after}    ${before}    msg=Shared resources/authentication changed during local purge
    ${data_after}=    SMB Backing File Digest    never-0.bin
    Should Be Equal    ${data_after}    ${data_before}
    Wait For SMB Ceph Health
