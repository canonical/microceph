*** Settings ***
Documentation    Native SMB integration on three real VMs and the supplied CI snap.
...    One request per mutation. Polls observe convergence only; no retry-to-green.
...    VM0 is also the SMB client. Run the whole ordered suite, not individual cases.
Resource        ../resources/smb_suite.resource
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
${SMB_INDEPENDENT}       ${False}

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

Purge Clustered Member Without Affecting Peers Or Shared State
    Require Clustered Deployment
    ${before}=    Capture SMB Shared State    robot-smb
    Length Should Be    ${before.resources}    2
    ${expected}=    SMB Payload Digest
    ${data_before}=    SMB Backing File Digest    initial-0.bin    robot-smb
    Should Be Equal    ${data_before}    ${expected}
    ${peer}=    SMB Snapshots    robot-smb    1
    ${peer_tokens}=    SMB Role Tokens    ${peer}    smbd    smbmetrics
    Run SMB Checked    2    microceph    disable    smb    --cluster-id    robot-smb    --purge
    Check Retired SMB Member    2
    ${removed}=    SMB Snapshot    2
    Should Not Be True    ${removed}[local_exists]
    Wait For SMB Ready    0
    Wait For SMB Ready    1
    SMB Exact Transfer    1    initial-0.bin    ${False}
    ${peer_after}=    SMB Snapshots    robot-smb    1
    ${peer_after_tokens}=    SMB Role Tokens    ${peer_after}    smbd    smbmetrics
    Should Be Equal    ${peer_after_tokens}    ${peer_tokens}
    ${after}=    Capture SMB Shared State    robot-smb
    Should Be Equal    ${after}    ${before}    msg=Shared resources/authentication changed during clustered local purge
    ${data_after}=    SMB Backing File Digest    initial-0.bin    robot-smb
    Should Be Equal    ${data_after}    ${data_before}

Nonclustered Singleton Rejects Second Owner And Allows Verified Relocation
    Require Clustered Deployment
    # Explicit fixture retirement does not repair or erase any earlier verdict.
    Retire SMB Fixture Placements
    ${applied}=    Apply SMB Resource    nonclustered
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    Enable And Check SMB Member    0    robot-never    3445    ${False}
    SMB Exact Transfer    0    never-0.bin    cluster=robot-never
    ${before}=    SMB Snapshots    robot-never    0
    ${tokens}=    SMB Role Tokens    ${before}    smbd    smbmetrics
    ${rejected}=    SMB Node Command    1    microceph    enable    smb    --cluster-id    robot-never    --wait=true
    Should Not Be Equal As Integers    ${rejected.rc}    0
    Should Contain    ${rejected.stdout}${rejected.stderr}    Nonclustered SMB cluster
    Check Retired SMB Member    1    robot-never
    SMB Exact Transfer    0    never-0.bin    ${False}    robot-never
    Enable And Check SMB Member    0    robot-never    3445    ${False}
    ${after}=    SMB Snapshots    robot-never    0
    ${after_tokens}=    SMB Role Tokens    ${after}    smbd    smbmetrics
    Should Be Equal    ${after_tokens}    ${tokens}
    Run SMB Checked    0    microceph    disable    smb    --cluster-id    robot-never
    Check Retired SMB Member    0    robot-never
    Enable And Check SMB Member    1    robot-never    3445    ${False}
    SMB Exact Transfer    1    never-0.bin    ${False}    robot-never
    SMB Exact Transfer    1    never-1.bin    cluster=robot-never
    Set Suite Variable    ${SMB_NONCLUSTERED}    ${True}

Deploy Independent Logical IDs On Disjoint Data Paths
    Skip If    not $SMB_NONCLUSTERED    Singleton relocation prerequisite failed
    ${applied}=    Apply SMB Resource    independent
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    Enable And Check SMB Member    0    robot-other    4445    ${False}
    SMB Exact Transfer    0    other-0.bin    cluster=robot-other
    SMB Exact Transfer    1    never-1.bin    ${False}    robot-never
    Set Suite Variable    ${SMB_INDEPENDENT}    ${True}

Purge Only The Stopped Independent Gateway State
    Skip If    not $SMB_INDEPENDENT    Independent logical service prerequisite failed
    ${before}=    Capture SMB Shared State    robot-never    robot-other
    Length Should Be    ${before.resources}    4
    ${expected}=    SMB Payload Digest
    ${data_before}=    SMB Backing File Digest    other-0.bin    robot-other
    Should Be Equal    ${data_before}    ${expected}
    ${peer}=    SMB Snapshots    robot-never    1
    ${peer_tokens}=    SMB Role Tokens    ${peer}    smbd    smbmetrics
    Run SMB Checked    0    microceph    disable    smb    --cluster-id    robot-other    --purge
    Check Retired SMB Member    0    robot-other
    ${removed}=    SMB Snapshot    0    robot-other
    Should Not Be True    ${removed}[local_exists]
    Wait For SMB Ready    1    robot-never    3445    ${False}
    SMB Exact Transfer    1    never-1.bin    ${False}    robot-never
    ${peer_after}=    SMB Snapshots    robot-never    1
    ${peer_after_tokens}=    SMB Role Tokens    ${peer_after}    smbd    smbmetrics
    Should Be Equal    ${peer_after_tokens}    ${peer_tokens}
    ${after}=    Capture SMB Shared State    robot-never    robot-other
    Should Be Equal    ${after}    ${before}    msg=Shared resources/authentication changed during local purge
    ${data_after}=    SMB Backing File Digest    other-0.bin    robot-other
    Should Be Equal    ${data_after}    ${data_before}
    Wait For SMB Ceph Health
