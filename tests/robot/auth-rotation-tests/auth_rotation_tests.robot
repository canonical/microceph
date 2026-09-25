*** Settings ***
Documentation    auth-rotation-tests
...    Verifies CephX authentication key rotation and cipher migration:
...    - Rotation to aes256k key type (CVE-2025-30156).
...    - Single-client rotation with --client.
...    - Unmanaged credential blocker detection.
...    - Auth status command formatting (text and JSON).
Resource        ../resources/microceph_harness.resource
Suite Setup     Auth Rotation Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    auth    rotation    lxd    integration

*** Keywords ***
Auth Rotation Suite Setup
    [Documentation]    Launch outer VM, install snap, bootstrap cluster with an OSD.
    Launch Outer Test VM    vm_name=microceph-auth-rotation-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install And Bootstrap MicroCeph
    Add Loop Device As OSD

Add Loop Device As OSD
    [Documentation]    Configures a loop disk as an OSD to ensure storage daemons are active.
    Run In VM And Check    sudo truncate -s 5G /tmp/loop-osd.img    30
    Run In VM And Check    sudo microceph disk add /tmp/loop-osd.img    120
    Wait For OSD Count    1

*** Test Cases ***
Test Initial Auth Status Reports Valid Ciphers
    [Documentation]    Verify 'microceph auth status' runs on a freshly bootstrapped cluster.
    [Tags]    auth    status
    ${json_status}=    Run In VM And Check    sudo microceph auth status --json    30
    Should Contain    ${json_status.stdout}    status
    ${text_status}=    Run In VM And Check    sudo microceph auth status    30
    Should Contain    ${text_status.stdout}    Status:

Test Single Client Rotation
    [Documentation]    Verify rotating a single client key via 'microceph auth rotate --client'.
    [Tags]    auth    rotate    client
    ${res}=    Run In VM And Check    sudo microceph auth rotate --client client.admin    120
    Should Contain    ${res.stdout}    Successfully rotated key for client.admin
    # Confirm admin commands still authenticate
    Run In VM And Check    sudo microceph.ceph status    30

Test Unmanaged Credential Blocker Detection
    [Documentation]    Creating an unmanaged client with an insecure cipher must be flagged
    ...    as a blocker by 'microceph auth status' during rotation.
    [Tags]    auth    rotate    blocker
    # Create an unmanaged client with legacy aes cipher
    Run In VM And Check    sudo microceph.ceph auth get-or-create client.external-app mon 'allow r' osd 'allow r'    30
    # Run rotation targeting aes256k
    ${rotate_res}=    Run In VM And Check    sudo microceph auth rotate --key-type aes256k    300
    # Rotation must pause at the blocker rather than claiming full completion
    Should Contain    ${rotate_res.stdout}    Rotation paused: Unmanaged credentials must be rotated manually
    # Verify auth status reports blocked state and blocker description
    ${status_res}=    Run In VM And Check    sudo microceph auth status    30
    Should Contain    ${status_res.stdout}    Status: blocked
    Should Contain    ${status_res.stdout}    Blocker: Unmanaged credentials must be rotated manually
    # Clean up unmanaged client so subsequent tests can complete
    Run In VM And Check    sudo microceph.ceph auth del client.external-app    30

Test Resume Rotation To Completion
    [Documentation]    Re-running rotation after resolving the blocker resumes and finishes.
    [Tags]    auth    rotate    resume
    ${res}=    Run In VM And Check    sudo microceph auth rotate --key-type aes256k    300
    Should Contain    ${res.stdout}    Successfully completed auth key rotation to aes256k
    # Verify auth status reports all clients on aes256k
    ${status_res}=    Run In VM And Check    sudo microceph auth status    30
    Should Contain    ${status_res.stdout}    Status: All client aes256k
    # Cluster health must be operational
    Run In VM And Check    sudo microceph.ceph status    30
