*** Settings ***
Documentation    vaultlocker-test
...    Real Vaultlocker snap + private Vault KV v2/AppRole, without Juju or a charm.
...    Vaultlocker owns encryption/keys; MicroCeph receives only its mapper path.
...    All disks are owned loop-backed scratch devices inside a disposable LXD VM.
...    Reboot with sealed Vault must recover the original object after unseal.
Resource        ../resources/microceph_harness.resource
Library         ../resources/vaultlocker_ci.py
Suite Setup     Vaultlocker Suite Setup
Suite Teardown  Teardown Vaultlocker Test VM
Test Timeout    30 minutes
Test Tags       single-node    disk-management    pristine    vaultlocker    lxd    integration

*** Variables ***
${OUTER_VM}                 microceph-vaultlocker-ci
${VAULT_REVISION}           2487
${VAULTLOCKER_REVISION}     12
${KEEP_VAULTLOCKER_VM}      ${False}

*** Keywords ***
Vaultlocker Suite Setup
    Launch Outer Test VM
    Copy Snap To VM
    Install And Bootstrap MicroCeph
    Enable DM Crypt
    Prepare Vaultlocker Fixture
    Start Vaultlocker Test Vault
    ${vault}=    Run Vaultlocker Fixture    setup
    Should Be Equal As Integers    ${vault}[kv_version]    2
    Should Be Equal    ${vault}[auth]    approle

Reject Existing Data Without Wiping
    [Documentation]    Check refusal and compare signatures plus the first 4 MiB.
    [Arguments]    ${kind}
    ${before}=    Run Vaultlocker Fixture    prepare    ${kind}
    ${refusal}=    Run In VM Must Fail    microceph disk add ${before}[path]    120
    Should Contain    ${refusal.stderr}    not pristine
    ${after}=    Run Vaultlocker Fixture    snapshot    ${kind}
    Should Be Equal    ${before}[checksum]    ${after}[checksum]
    Should Be Equal    ${before}[signatures]    ${after}[signatures]

*** Test Cases ***
Enroll Vaultlocker Mapper Without Wipe Or Native Encryption
    ${encrypted}=    Run Vaultlocker Fixture    encrypt
    Should Be True    ${encrypted}[key_escrowed]
    Should Be Equal As Integers    ${encrypted}[kv_version]    2
    Should Be Equal As Integers    ${encrypted}[secret_version]    1
    ${fresh}=    Run Vaultlocker Fixture    snapshot
    Should Be True    ${fresh}[nonzero]
    Should Be Empty    ${fresh}[signatures][signatures]

    # The regression: no --wipe and no --encrypt, only the Vaultlocker mapper.
    Run In VM And Check    microceph disk add ${encrypted}[mapper_path]    180
    Wait For OSD Count Up In    1
    ${disks}=    Run In VM And Check    microceph disk list --json    30
    ${configured}=    Evaluate    json.loads($disks.stdout)["ConfiguredDisks"]    modules=json
    Length Should Be    ${configured}    1
    Should Be Equal    ${configured}[0][path]    ${encrypted}[mapper_path]
    ${io}=    Run Vaultlocker Fixture    io
    Should Be Equal As Integers    ${io}[restored_bytes]    1048576
    Should Be Equal    ${io}[source_sha256]    ${io}[restored_sha256]

    Reject Existing Data Without Wiping    filesystem
    Reject Existing Data Without Wiping    partitioned
    Reject Existing Data Without Wiping    bluestore
    Reject Existing Data Without Wiping    raw

    # Cold boot loses loop associations and seals the file-backed test Vault.
    # The fixture restores only its owned devices; Vaultlocker must unlock them.
    ${armed}=    Run Vaultlocker Fixture    arm-reboot
    Should Be True    ${armed}[armed]
    Restart Outer Test VM
    Wait For Vaultlocker Test Vault
    ${observation}=    Sample Locked Vaultlocker Mapper
    Should Not Contain    ${observation}[samples]    ${False}
    Should Be True    ${observation}[seconds] >= 90
    ${unsealed}=    Run Vaultlocker Fixture    unseal
    Should Be True    ${unsealed}[unsealed]
    ${recovered}=    Wait For Vaultlocker Object
    Should Be Equal As Integers    ${recovered}[restored_bytes]    1048576
    Should Be Equal    ${recovered}[source_sha256]    ${io}[source_sha256]
    Should Be Equal    ${recovered}[restored_sha256]    ${io}[source_sha256]
    Wait For OSD Count Up In    1
    ${after_reboot}=    Run In VM And Check    microceph disk list --json    30
    ${configured}=    Evaluate    json.loads($after_reboot.stdout)["ConfiguredDisks"]    modules=json
    Length Should Be    ${configured}    1
    Should Be Equal    ${configured}[0][path]    ${encrypted}[mapper_path]
