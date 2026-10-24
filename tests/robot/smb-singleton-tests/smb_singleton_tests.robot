*** Settings ***
Documentation    Ordered singleton/independent non-AD scenarios; no clustered-suite prerequisite.
Resource         ../resources/smb_suite.resource
Suite Setup      SMB Scenario Setup    singleton
Suite Teardown   SMB Scenario Cleanup
Test Teardown    SMB Failure Diagnostics
Test Timeout     15 minutes
Test Tags        smb    multi-node    lxd    slow    integration

*** Variables ***
${SMB_NONCLUSTERED}      ${False}
${SMB_INDEPENDENT}       ${False}

*** Test Cases ***
Nonclustered Singleton Enforces Ownership And Retains Gateway Local Access
    [Documentation]    Local-user identities do not follow placement to another gateway.
    ...    Verify sequential placement, then existing-file access on the retained original gateway.
    Retire SMB Fixture Placements
    ${applied}=    Apply SMB Resource    nonclustered
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}\n${applied.stderr}
    Enable And Check SMB Member    1    robot-never    3445    ${False}
    SMB Exact Transfer    1    never-0.bin    cluster=robot-never
    ${data_before}=    SMB Backing File Digest    never-0.bin    robot-never
    ${expected}=    SMB Payload Digest
    Should Be Equal    ${data_before}    ${expected}
    ${before}=    SMB Snapshots    robot-never    1
    ${tokens}=    SMB Role Tokens    ${before}    smbd    smbmetrics
    ${rejected}=    SMB Node Command    0    microceph    enable    smb    --cluster-id    robot-never    --wait=true
    Should Not Be Equal As Integers    ${rejected.rc}    0
    Should Contain    ${rejected.stdout}${rejected.stderr}    Nonclustered SMB cluster
    Check Retired SMB Member    0    robot-never
    SMB Exact Transfer    1    never-0.bin    ${False}    robot-never
    Enable And Check SMB Member    1    robot-never    3445    ${False}
    ${after}=    SMB Snapshots    robot-never    1
    ${after_tokens}=    SMB Role Tokens    ${after}    smbd    smbmetrics
    Should Be Equal    ${after_tokens}    ${tokens}
    Run SMB Checked    1    microceph    disable    smb    --cluster-id    robot-never
    Check Retired SMB Member    1    robot-never
    Enable And Check SMB Member    0    robot-never    3445    ${False}
    # Placement succeeds, but access through another local SAM is not promised.
    ${data_moved}=    SMB Backing File Digest    never-0.bin    robot-never
    Should Be Equal    ${data_moved}    ${data_before}
    Run SMB Checked    0    microceph    disable    smb    --cluster-id    robot-never
    Check Retired SMB Member    0    robot-never
    Enable And Check SMB Member    1    robot-never    3445    ${False}
    SMB Exact Transfer    1    never-0.bin    ${False}    robot-never
    SMB Exact Transfer    1    never-1.bin    cluster=robot-never
    Set Suite Variable    ${SMB_NONCLUSTERED}    ${True}

Deploy Independent Logical IDs On Disjoint Data Paths
    Skip If    not $SMB_NONCLUSTERED    Singleton gateway-local access prerequisite failed
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
