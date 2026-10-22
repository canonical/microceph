*** Settings ***
Documentation    External Samba AD authentication, not domain-management qualification.
...    Whole ordered suite: three owned gateway VMs (VM0 is client), one owned AD DC.
...    Every client invocation is fresh. Mutations run once; polls only observe.
Resource         ../resources/smb_suite.resource
Suite Setup      SMB Scenario Setup    ad
Suite Teardown   SMB Scenario Cleanup
Test Teardown    AD Failure Diagnostics
Test Timeout     15 minutes
Test Tags        smb    ad    multi-node    lxd    slow    integration

*** Variables ***
${SMB_VM_IMAGE}          ubuntu:26.04
${SMB_SNAPD_CHANNEL}     latest/stable
${AD_DEPLOYED}          ${False}
${SMB_AD_BAD_CLUSTER}   robot-bad

*** Keywords ***
AD Failure Diagnostics
    IF    '${TEST STATUS}' == 'FAIL'
        SMB AD Diagnostics
        Collect SMB Diagnostics
    END

Require AD Deployment
    Skip If    not $AD_DEPLOYED    AD deployment prerequisite failed; retain its first failure

Apply AD Checked
    [Arguments]    ${kind}
    ${applied}=    SMB AD Apply    ${kind}
    Should Be Equal As Integers    ${applied.rc}    0    msg=${applied.stdout}${applied.stderr}

AD Exact IO
    [Arguments]    ${node}    ${protocol}    ${action}    ${file}    ${share}=data    ${user}=alice
    ${result}=    SMB AD Client    ${node}    ${share}    ${user}    ${protocol}    ${action}    ${file}
    Should Be Equal As Integers    ${result}[rc]    0    msg=${result}[message]
    IF    '${action}' == 'get'
        ${expected}=    SMB Payload Digest
        Should Be Equal    ${result}[digest]    ${expected}
    END
    IF    '${protocol}' == 'kerberos'
        Should Contain    ${result}[ticket]    cifs/robot-ad.robot.test@ROBOT.TEST
    END

AD Group And Readonly Checks
    [Arguments]    ${node}    ${protocol}    ${file}
    # Bob is a real authenticated nonmember of writers, not a wrong-password probe.
    AD Exact IO    ${node}    ${protocol}    get    ${file}    auth    bob
    ${denied}=    SMB AD Client    ${node}    data    bob    ${protocol}    get    ${file}
    Should Not Be Equal As Integers    ${denied}[rc]    0
    Should Contain    ${denied}[message]    NT_STATUS_ACCESS_DENIED
    AD Exact IO    ${node}    ${protocol}    get    ${file}    read
    ${readonly}=    SMB AD Client    ${node}    read    alice    ${protocol}    put    forbidden.bin
    Should Not Be Equal As Integers    ${readonly}[rc]    0
    Should Match Regexp    ${readonly}[message]    NT_STATUS_(ACCESS_DENIED|MEDIA_WRITE_PROTECTED)

Check AD Persistence
    ${identity0}=    SMB AD Identity    0
    ${identity1}=    SMB AD Identity    1
    Should Be Equal    ${identity0}    ${AD_IDENTITY}
    Should Be Equal    ${identity1}    ${AD_IDENTITY}
    ${account}=    SMB AD Account
    Should Be Equal    ${account}    ${AD_ACCOUNT}    msg=Computer SID/password generation changed; do not label a rejoin as persistence
    AD Exact IO    0    kerberos    get    kerberos.bin
    AD Exact IO    1    ntlm    get    ntlm.bin
    AD Group And Readonly Checks    0    kerberos    kerberos.bin
    AD Group And Readonly Checks    1    ntlm    ntlm.bin

AD DC Checked
    [Arguments]    @{command}
    ${result}=    SMB AD DC Command    @{command}
    Should Be Equal As Integers    ${result.rc}    0    msg=${result.stderr}

*** Test Cases ***
Wrong Join Credentials Never Produce A Ready Gateway
    Apply AD Checked    ad-wrong
    ${rejected}=    SMB Node Command    0    microceph    enable    smb    --cluster-id    ${SMB_AD_BAD_CLUSTER}    --wait=true
    Should Not Be Equal As Integers    ${rejected.rc}    0
    ${state}=    SMB Snapshot    0    ${SMB_AD_BAD_CLUSTER}
    ${ready}=    SMB Snapshot Is Ready    ${state}    ${SMB_AD_BAD_CLUSTER}    1445
    Should Not Be True    ${ready}
    Should Not Be Equal    ${state}[roles][smbd][ActiveState]    active
    ${failure}=    SMB AD Join Error
    Should Match Regexp    ${failure}    (?i)(NT_STATUS_LOGON_FAILURE|WERR_LOGON_FAILURE|logon failure|NT_STATUS_WRONG_PASSWORD)
    # Retire without purge/reset/retry; ambiguous attempt/state remains for inspection.
    Run SMB Checked    0    microceph    disable    smb    --cluster-id    ${SMB_AD_BAD_CLUSTER}
    Check Retired SMB Member    0    ${SMB_AD_BAD_CLUSTER}

Deploy One Logical AD Identity On Two Gateways
    Apply AD Checked    ad
    # Controlled admission: finish the initial joiner before admitting a peer.
    Enable And Check SMB Member    0    robot-ad
    Enable And Check SMB Member    1    robot-ad
    ${identity0}=    SMB AD Identity    0
    ${identity1}=    SMB AD Identity    1
    Should Be Equal    ${identity0}    ${identity1}    msg=Domain SID/UID/primary GID/group GID differs across gateways
    Should Not Be Equal    ${identity0}[alice][uid]    ${identity0}[bob][uid]
    ${prepared}=    Prepare SMB AD Data    ${identity0}[alice][uid]    ${identity0}[writers][gid]
    Should Be Equal As Integers    ${prepared.rc}    0    msg=${prepared.stderr}
    ${account}=    SMB AD Account
    Set Suite Variable    ${AD_IDENTITY}    ${identity0}
    Set Suite Variable    ${AD_ACCOUNT}    ${account}
    Set Suite Variable    ${AD_DEPLOYED}    ${True}

Kerberos Required Uses Intended Service Ticket And Group Permissions On Both Gateways
    Require AD Deployment
    AD Exact IO    0    kerberos    put    kerberos.bin
    AD Exact IO    1    kerberos    get    kerberos.bin
    AD Exact IO    1    kerberos    put    kerberos-peer.bin
    AD Exact IO    0    kerberos    get    kerberos-peer.bin
    AD Group And Readonly Checks    0    kerberos    kerberos.bin
    AD Group And Readonly Checks    1    kerberos    kerberos-peer.bin
    ${stat}=    SMB AD Backing Stat    kerberos.bin
    Should Be Equal As Integers    ${stat}[uid]    ${AD_IDENTITY}[alice][uid]
    Should Be Equal As Integers    ${stat}[gid]    ${AD_IDENTITY}[alice][gid]
    ${expected}=    SMB Payload Digest
    Should Be Equal    ${stat}[digest]    ${expected}

Kerberos Disabled Domain NTLMv2 Is Separate From Kerberos And Negative Authentication
    Require AD Deployment
    AD Exact IO    1    ntlm    put    ntlm.bin
    AD Exact IO    0    ntlm    get    ntlm.bin
    AD Exact IO    0    ntlm    put    ntlm-peer.bin
    AD Exact IO    1    ntlm    get    ntlm-peer.bin
    AD Group And Readonly Checks    0    ntlm    ntlm.bin
    AD Group And Readonly Checks    1    ntlm    ntlm-peer.bin
    ${wrong}=    SMB AD Client    0    data    wrong    ntlm    get    ntlm.bin
    Should Not Be Equal As Integers    ${wrong}[rc]    0
    Should Contain    ${wrong}[message]    NT_STATUS_LOGON_FAILURE
    ${unknown}=    SMB AD Client    1    data    unknown    ntlm    get    ntlm.bin
    Should Not Be Equal As Integers    ${unknown}[rc]    0
    Should Contain    ${unknown}[message]    NT_STATUS_LOGON_FAILURE

Role Restart And Ordinary Gateway Reboot Preserve Fresh Authentication And Mappings
    Require AD Deployment
    Run SMB Checked    0    snap    restart    microceph.winbindd    microceph.smbd
    Wait For SMB Ready    0    robot-ad    1445
    Check AD Persistence
    ${before}=    SMB Snapshot    1    robot-ad
    Reboot SMB VM    1
    ${after}=    Wait For SMB Ready    1    robot-ad    1445
    Should Not Be Equal    ${after}[boot_id]    ${before}[boot_id]
    # No snap start, re-enable or repair after the reboot.
    Wait For SMB Ceph Health
    Check AD Persistence

Bootstrap Input Change Does Not Rejoin Or Restart Serving Roles
    Require AD Deployment
    ${before}=    SMB Snapshots    robot-ad    0    1
    ${tokens}=    SMB Role Tokens    ${before}    ctdbd    ctdb-nodes    winbindd    smbd    smbmetrics
    Apply AD Checked    ad-refresh
    ${after}=    Wait For SMB AD Refresh    ${before}
    ${after_tokens}=    SMB Role Tokens    ${after}    ctdbd    ctdb-nodes    winbindd    smbd    smbmetrics
    Should Be Equal    ${after_tokens}    ${tokens}
    Check AD Persistence

Serving Peer Survives Initial Joiner Stop And Retained State Reenable
    Require AD Deployment
    Run SMB Checked    0    microceph    disable    smb    --cluster-id    robot-ad
    Check Retired SMB Member    0    robot-ad
    Wait For SMB Ready    1    robot-ad    1445
    AD Exact IO    1    kerberos    put    survivor.bin
    AD Group And Readonly Checks    1    ntlm    ntlm.bin
    Enable And Check SMB Member    0    robot-ad
    AD Exact IO    0    ntlm    get    survivor.bin
    Check AD Persistence

Short Directory Outage Is Bounded And Recovers Without Rejoin
    Require AD Deployment
    ${before}=    SMB Snapshots    robot-ad    0    1
    ${tokens}=    SMB Role Tokens    ${before}    ctdbd    ctdb-nodes    winbindd    smbd    smbmetrics
    AD DC Checked    systemctl    stop    robot-ad-dc
    TRY
        # Sustain unavailability across an ordinary 30-second updater tick.
        ${observations}=    Collect SMB Observations    35    robot-ad    0    1
        Should Not Be Empty    ${observations}
        ${offline}=    SMB AD Client    1    auth    bob    ntlm    get    ntlm.bin
        Should Be True    ${offline}[elapsed] < 90    msg=Directory-unreachable request exceeded bounded budget
        # Offline success is not promised; if it succeeds it must return exact data.
        IF    $offline['rc'] == 0
            ${expected}=    SMB Payload Digest
            Should Be Equal    ${offline}[digest]    ${expected}
        END
    FINALLY
        AD DC Checked    systemctl    start    robot-ad-dc
    END
    Wait For SMB Ready    0    robot-ad    1445
    Wait For SMB Ready    1    robot-ad    1445
    ${after}=    SMB Snapshots    robot-ad    0    1
    ${after_tokens}=    SMB Role Tokens    ${after}    ctdbd    ctdb-nodes    winbindd    smbd    smbmetrics
    Should Be Equal    ${after_tokens}    ${tokens}    msg=Directory outage restarted serving/identity processes
    Check AD Persistence

Local Peer Purge Preserves Identity Shared Resources And Serving Peer
    Require AD Deployment
    ${shared}=    Capture SMB Shared State    robot-ad
    ${peer}=    SMB Snapshots    robot-ad    1
    ${tokens}=    SMB Role Tokens    ${peer}    ctdbd    ctdb-nodes    winbindd    smbd    smbmetrics
    Run SMB Checked    0    microceph    disable    smb    --cluster-id    robot-ad    --purge
    Check Retired SMB Member    0    robot-ad
    ${retained}=    SMB Snapshot    0    robot-ad
    Should Be True    ${retained}[local_exists]    msg=AD purge must retain identity databases
    Run SMB Checked    0    test    -f    /var/snap/microceph/common/smb/robot-ad/diagnostics/identity-retained
    Wait For SMB Ready    1    robot-ad    1445
    AD Exact IO    1    kerberos    get    survivor.bin
    AD Group And Readonly Checks    1    ntlm    ntlm.bin
    ${identity}=    SMB AD Identity    1
    Should Be Equal    ${identity}    ${AD_IDENTITY}
    ${account}=    SMB AD Account
    Should Be Equal    ${account}    ${AD_ACCOUNT}
    ${after}=    Capture SMB Shared State    robot-ad
    Should Be Equal    ${after}    ${shared}
    ${peer_after}=    SMB Snapshots    robot-ad    1
    ${after_tokens}=    SMB Role Tokens    ${peer_after}    ctdbd    ctdb-nodes    winbindd    smbd    smbmetrics
    Should Be Equal    ${after_tokens}    ${tokens}
    ${stat}=    SMB AD Backing Stat    survivor.bin
    ${expected}=    SMB Payload Digest
    Should Be Equal    ${stat}[digest]    ${expected}
