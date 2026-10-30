*** Settings ***
Documentation    Local-user direct-CephFS singleton; three backing-Ceph VMs, no clustered SMB or DC.
Resource         ../resources/smb_suite.resource
Suite Setup      SMB Suite Setup
Suite Teardown   SMB Suite Cleanup
Test Teardown    SMB Failure Diagnostics
Test Timeout     15 minutes
Test Tags        smb    multi-node    lxd    slow    integration

*** Variables ***
${SMB_SINGLETON}      ${False}
${SMB_INDEPENDENT}    ${False}

*** Keywords ***
Check Automatic SMB Configuration Refresh
    ${sid}=    SMB User SID    1
    Run SMB Checked    1    snap    stop    --disable    microceph.smbd
    ${applied}=    Apply SMB Resource    refresh
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    ${disabled}=    Observe SMB Interval    1
    ${preserved}=    SMB Observations Disabled    ${disabled}
    Should Be True    ${preserved}    msg=Refresh changed a disabled gateway or its published endpoint
    Run SMB Checked    1    snap    start    --enable    microceph.smbd
    ${updated}=    Wait For SMB Ready    1    robot-never    3446
    ${updated_sid}=    SMB User SID    1
    Should Be Equal    ${updated_sid}    ${sid}
    ${old_password}=    SMB Client Transfer    1    never-1.bin    ${False}    alice
    Should Not Be Equal As Integers    ${old_password.rc}    0
    ${read}=    SMB Client Transfer    1    never-1.bin    ${False}    refreshed
    Should Be Equal As Integers    ${read.rc}    0    msg=${read.stdout}\n${read.stderr}
    ${compare}=    Compare SMB Payload
    Should Be Equal As Integers    ${compare.rc}    0
    ${write}=    SMB Client Transfer    1    readonly-refused.bin    ${True}    refreshed
    Should Not Be Equal As Integers    ${write.rc}    0
    ${tokens}=    SMB Role Tokens    ${updated}
    ${reapplied}=    Apply SMB Resource    refresh
    Should Be Equal As Integers    ${reapplied.rc}    0    msg=${reapplied.stdout}\n${reapplied.stderr}
    ${samples}=    Observe SMB Interval    1
    ${unchanged}=    SMB Observations Unchanged    ${samples}    ${tokens}    robot-never    3446
    Should Be True    ${unchanged}    msg=Equivalent sources caused a restart or lost confirmed readiness
    ${restored}=    Apply SMB Resource    nonclustered
    Should Be Equal As Integers    ${restored.rc}    0    msg=${restored.stdout}\n${restored.stderr}
    Wait For SMB Ready    1    robot-never    3445
    ${restored_sid}=    SMB User SID    1
    Should Be Equal    ${restored_sid}    ${sid}
    SMB Exact Transfer    1    never-1.bin    ${False}
    SMB Exact Transfer    1    refresh-restored.bin
    ${data}=    SMB Backing File Digest    never-1.bin
    ${expected}=    SMB Payload Digest
    Should Be Equal    ${data}    ${expected}

*** Test Cases ***
Singleton Enforces Ownership And Retains Gateway Local Access
    ${applied}=    Apply SMB Resource    nonclustered
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    Enable And Check SMB Member    1
    SMB Exact Transfer    1    never-0.bin
    ${wrong}=    SMB Client Transfer    1    never-0.bin    ${False}    wrong
    Should Not Be Equal As Integers    ${wrong.rc}    0
    ${expected}=    SMB Payload Digest
    ${data_before}=    SMB Backing File Digest    never-0.bin
    Should Be Equal    ${data_before}    ${expected}
    ${rejected}=    SMB Node Command    0    microceph    enable    smb    --cluster-id    robot-never    --wait=true
    Should Not Be Equal As Integers    ${rejected.rc}    0
    Should Contain    ${rejected.stdout}${rejected.stderr}    Nonclustered SMB cluster
    Check Retired SMB Member    0
    SMB Exact Transfer    1    never-0.bin    ${False}
    Run SMB Checked    1    microceph    disable    smb    --cluster-id    robot-never
    Check Retired SMB Member    1
    Enable And Check SMB Member    0
    ${data_moved}=    SMB Backing File Digest    never-0.bin
    Should Be Equal    ${data_moved}    ${data_before}
    # Gateway-local SAM/SID is not promised portable to another placement.
    Run SMB Checked    0    microceph    disable    smb    --cluster-id    robot-never
    Check Retired SMB Member    0
    Enable And Check SMB Member    1
    SMB Exact Transfer    1    never-0.bin    ${False}
    ${before}=    SMB Snapshot    1
    ${tokens}=    SMB Role Tokens    ${before}
    Run SMB Checked    1    snap    restart    microceph.daemon
    ${after}=    Wait For SMB Ready    1    robot-never    3445
    ${after_tokens}=    SMB Role Tokens    ${after}
    Should Be Equal    ${after_tokens}    ${tokens}
    Run SMB Checked    1    snap    restart    microceph.smbd
    Wait For SMB Ready    1    robot-never    3445
    SMB Exact Transfer    1    never-0.bin    ${False}
    Reboot SMB VM    1
    ${rebooted}=    Wait For SMB Ready    1    robot-never    3445
    Should Not Be Equal    ${rebooted}[boot_id]    ${before}[boot_id]
    SMB Exact Transfer    1    never-0.bin    ${False}
    SMB Exact Transfer    1    never-1.bin
    Check Automatic SMB Configuration Refresh
    Set Suite Variable    ${SMB_SINGLETON}    ${True}

Deploy Independent Logical IDs On Disjoint Data Paths
    Skip If    not $SMB_SINGLETON    Singleton gateway-local prerequisite failed
    ${applied}=    Apply SMB Resource    independent
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    Enable And Check SMB Member    0    robot-other    4445
    SMB Exact Transfer    0    other-0.bin    cluster=robot-other
    SMB Exact Transfer    1    never-1.bin    ${False}
    Set Suite Variable    ${SMB_INDEPENDENT}    ${True}

Purge Only The Stopped Independent Gateway State
    Skip If    not $SMB_INDEPENDENT    Independent logical service prerequisite failed
    ${before}=    Capture SMB Shared State    robot-never    robot-other
    Length Should Be    ${before.resources}    4
    ${expected}=    SMB Payload Digest
    ${data_before}=    SMB Backing File Digest    other-0.bin    robot-other
    Should Be Equal    ${data_before}    ${expected}
    ${peer}=    SMB Snapshot    1
    ${peer_tokens}=    SMB Role Tokens    ${peer}
    Run SMB Checked    0    microceph    disable    smb    --cluster-id    robot-other    --purge
    Check Retired SMB Member    0    robot-other
    ${removed}=    SMB Snapshot    0    robot-other
    Should Not Be True    ${removed}[local_exists]
    Wait For SMB Ready    1    robot-never    3445
    SMB Exact Transfer    1    never-1.bin    ${False}
    ${peer_after}=    SMB Snapshot    1
    ${peer_after_tokens}=    SMB Role Tokens    ${peer_after}
    Should Be Equal    ${peer_after_tokens}    ${peer_tokens}
    ${after}=    Capture SMB Shared State    robot-never    robot-other
    Should Be Equal    ${after}    ${before}    msg=Shared resources/authentication changed during local purge
    ${data_after}=    SMB Backing File Digest    other-0.bin    robot-other
    Should Be Equal    ${data_after}    ${data_before}
    Wait For SMB Ceph Health
