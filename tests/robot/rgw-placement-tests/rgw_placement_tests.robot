*** Settings ***
Documentation    rgw-placement-tests
...    Snap-backed, single-node coverage of the role-managed RGW placement
...    contract:
...      - the placement-rgw capability is advertised,
...      - invalid rgw objects (missing enabled, missing ssl intent, half /
...        mismatched / malformed TLS pairs, bad ports, wrong mode, unknown
...        member) are rejected with exact HTTP 400 and mutate no accepted
...        state,
...      - enabled / ssl are explicit in placement objects; plaintext defaults
...        to port 80, TLS-only to 443, and dual listeners are supported,
...      - re-applying an unchanged policy restarts nothing (stable systemd
...        InvocationID), port changes restart correctly, and a stopped service
...        is recovered by a repeat request,
...      - TLS without a pair reuses the member's valid local material and
...        keeps serving the same certificate; rotation serves a new one that
...        the old CA no longer trusts,
...      - plaintext requires explicit ssl:false, disables close the TLS
...        listener and remove leftover material, and a TLS-only enable with
...        no usable local material fails closed (never silently downgrades),
...      - omission, the empty waiting policy, and DELETE never touch running
...        services; explicit disable works after partial cleanup and repeats,
...      - no submitted raw or base64 secret appears in the placement GET,
...        stored policy, database, daemon logs, journal, or refusal.
...    Assertions are linear Robot; all JSON decisions are parsed in Python
...    (placement_status.py via harness keywords), never by raw-substring or
...    JSON-key-order matching.
Resource        ../resources/microceph_harness.resource
Resource        ../resources/rgw_placement.resource
Suite Setup     RGW Placement Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    rgw    placement    lxd    integration    slow

*** Keywords ***
RGW Placement Suite Setup
    [Documentation]    Launch a single VM, install the local snap, bootstrap, add
    ...    3 loop OSDs so RGW zone pools can place PGs, and pre-generate two
    ...    disposable CA/server pairs (rgwa, rgwb) whose material only ever
    ...    lives inside the VM: the enable/rotation policies are assembled from
    ...    these files there, and the secret-absence scans derive their needles
    ...    from them, so no certificate value crosses the Robot boundary.
    Launch Outer Test VM    vm_name=microceph-rgw-placement-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install And Bootstrap MicroCeph
    # Debug logging so the material-leak probe's journal scan is meaningful:
    # at the default level the daemon never logs anything a leak could hide in.
    Run In VM And Check    sudo microceph log set-level debug    30
    Create Loop Devices
    Run In VM And Check    sudo microceph disk add /dev/sdia /dev/sdib /dev/sdic --wipe    300
    Wait For OSD Count    3
    ${pair_a}=    Generate TLS Pair In VM    rgwa    rgwa.example.com
    ${pair_b}=    Generate TLS Pair In VM    rgwb    rgwb.example.com
    Set Suite Variable    ${PAIR_A}    ${pair_a}
    Set Suite Variable    ${PAIR_B}    ${pair_b}
    ${hn}=    Get VM Hostname
    Set Suite Variable    ${MEMBER}    ${hn}

Reject Invalid Placement
    [Documentation]    PUTs an invalid policy and asserts the exact client-side
    ...    rejection status (400): the placement API validates the complete
    ...    request before storing intent or changing services.
    [Arguments]    ${body}
    ${resp}=    MicroCeph API Put    placement    ${body}    timeout=120
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    400    msg=invalid policy must be rejected with HTTP 400: ${body} -> ${resp}

*** Test Cases ***
Test Placement RGW Capability Advertised
    [Documentation]    The snap advertises the placement-rgw capability marker so a
    ...    caller can check for role-managed RGW placement before using it.
    [Tags]    placement
    ${caps}=    Get Supported Capabilities
    Should Contain    ${caps}    placement-rgw    msg=placement-rgw capability not advertised

Test Invalid RGW Objects Rejected Without State Change
    [Documentation]    Each malformed object is rejected with exact HTTP 400 and the
    ...    cluster keeps no accepted policy: a request that can never apply
    ...    must not replace the desired state. Covers the object-only contract
    ...    (bare booleans rejected), explicit enabled and ssl intent, port
    ...    ranges and port collisions, mode enforcement, and member validation.
    [Tags]    placement
    @{invalid}=    Create List
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":true}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{}}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true}}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":false,"ssl_certificate":"Y2VydA=="}}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":true,"ssl_certificate":"Y2VydA=="}}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":true,"ssl_private_key":"a2V5"}}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":true,"ssl_certificate":"not base64!!","ssl_private_key":"not base64!!"}}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":true,"ssl_certificate":"aGVsbG8gd29ybGQ=","ssl_private_key":"aGVsbG8gd29ybGQ="}}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":false,"port":65536}}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":false,"port":-1}}}}
    ...    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":true,"port":8080,"ssl_port":8080}}}}
    ...    {"mode":"apply","members":{}}
    ...    {"members":{}}
    ...    {"mode":"reconcile","members":{"not-a-cluster-member":{"rgw":{"enabled":true,"ssl":false}}}}
    FOR    ${body}    IN    @{invalid}
        Reject Invalid Placement    ${body}
    END
    ${active}=    Placement Policy Active
    Should Be Equal    ${active}    ${False}    msg=an invalid request must not leave an accepted policy behind

Test Enable RGW Plaintext With Explicit False On Port 8080
    [Documentation]    The object form with enabled:true and an explicit ssl:false
    ...    enables RGW on port 8080: the observed frontend reports the public
    ...    serving port with ssl false and no TLS port, the rendered config
    ...    carries the same listener, and S3 content round-trips over it.
    [Tags]    rgw    placement
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":false,"port":8080}}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=plaintext RGW placement failed: ${resp}
    Wait For RGW    1
    Wait For Member RGW Frontend    ${MEMBER}    port=8080    ssl=${False}
    ${status}=    Get Placement Status JSON
    ${fe}=    Member RGW Frontend Status    ${status}    ${MEMBER}
    Should Be Equal As Integers    ${fe}[port]    8080    msg=observed frontend port: ${fe}
    Should Be Equal    ${fe}[ssl]    ${False}    msg=observed frontend TLS flag: ${fe}
    Dictionary Should Not Contain Key    ${fe}    ssl_port    msg=plaintext frontend must not report a TLS port: ${fe}
    ${conf}=    Get RGW Frontend Conf Ports
    Should Be Equal As Integers    ${conf}[port]    8080
    Should Be Equal As Integers    ${conf}[ssl_port]    0
    Should Be Equal    ${conf}[ssl]    ${False}
    ${inv}=    RGW Service Invocation ID In VM
    Should Not Be Empty    ${inv}    msg=rgw unit must be running after enable
    Exercise RGW S3 In VM    host=localhost    port=8080    filename=explicit

Test Idempotent Reapply Restarts Nothing
    [Documentation]    Re-applying the identical policy succeeds and does not restart
    ...    the daemon: the systemd InvocationID stays the same, proving process-
    ...    start stability across frequent reconcile PUTs.
    [Tags]    rgw    placement    idempotency
    ${before}=    RGW Service Invocation ID In VM
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":false,"port":8080}}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=idempotent re-apply failed: ${resp}
    Wait For RGW    1
    ${after}=    RGW Service Invocation ID In VM
    Should Be Equal    ${after}    ${before}    msg=unchanged policy must not restart RGW (process-start stability)
    Wait For Member RGW Frontend    ${MEMBER}    port=8080    ssl=${False}    tries=6

Test Port Update Restarts RGW On The New Port Only
    [Documentation]    Changing the frontend port to 8081 restarts RGW onto the new
    ...    listener: the old port closes, the observed frontend and rendered
    ...    config move, the process actually restarts (new InvocationID), and
    ...    S3 serves on the new port.
    [Tags]    rgw    placement
    ${before}=    RGW Service Invocation ID In VM
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":false,"port":8081}}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=port update failed: ${resp}
    Wait For Member RGW Frontend    ${MEMBER}    port=8081    ssl=${False}
    RGW Endpoint Closed In VM    localhost    8080
    ${after}=    RGW Service Invocation ID In VM
    Should Not Be Equal    ${after}    ${before}    msg=a frontend port change must restart RGW
    ${conf}=    Get RGW Frontend Conf Ports
    Should Be Equal As Integers    ${conf}[port]    8081
    Exercise RGW S3 In VM    host=localhost    port=8081    filename=portupdate

Test Plaintext Defaults To Port 80
    [Documentation]    Omitting the port on a plaintext object normalizes to 80: the
    ...    observed frontend reports port 80 with ssl false and the previous
    ...    explicit port closes.
    [Tags]    rgw    placement
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":false}}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=default-port policy failed: ${resp}
    Wait For Member RGW Frontend    ${MEMBER}    port=80    ssl=${False}
    RGW Endpoint Closed In VM    localhost    8081
    Exercise RGW S3 In VM    host=localhost    port=80    filename=default80

Test Enable TLS Only With Certificate Pair
    [Documentation]    Submitting a real disposable CA/server pair (assembled inside
    ...    the VM, base64 by jq) enables a TLS-only frontend on the default 443:
    ...    the observed frontend reports ssl true with ssl_port 443 and no
    ...    plaintext port, the handshake validates against the generated CA, S3
    ...    content round-trips over trusted TLS, material files are 0600, and no
    ...    submitted secret value appears in the placement GET, stored policy,
    ...    database, daemon logs, journal, or refusal.
    [Tags]    rgw    placement    tls    secrets
    ${body_file}=    Write RGW TLS Policy In VM    ${MEMBER}    0    443    rgwa
    ${resp}=    MicroCeph API Put From File In VM    placement    ${body_file}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=TLS-only placement failed: ${resp}
    Wait For RGW    1
    Wait For RGW SSL Port    localhost    443
    Wait For Member RGW Frontend    ${MEMBER}    ssl=${True}
    ${status}=    Get Placement Status JSON
    ${fe}=    Member RGW Frontend Status    ${status}    ${MEMBER}
    Dictionary Should Not Contain Key    ${fe}    port    msg=TLS-only frontend must not report a plaintext port: ${fe}
    Should Be Equal As Integers    ${fe}[ssl_port]    443    msg=TLS port must default to 443: ${fe}
    Should Be Equal    ${fe}[ssl]    ${True}
    ${conf}=    Get RGW Frontend Conf Ports
    Should Be Equal As Integers    ${conf}[port]    0
    Should Be Equal As Integers    ${conf}[ssl_port]    443
    Should Be Equal    ${conf}[ssl]    ${True}
    ${fp}=    RGW TLS Fingerprint In VM    localhost    443
    Should Not Be Empty    ${fp}    msg=TLS listener did not serve a certificate
    Set Suite Variable    ${FINGERPRINT_A}    ${fp}
    Exercise RGW S3 In VM    host=localhost    port=443    ssl=${True}    ca_cert=${PAIR_A}[ca]    filename=tlsonly
    Assert RGW TLS Material Is Mode 600 In VM
    Assert RGW Material Absent In VM    rgwa
    ${leaks}=    RGW Policy Leaks Secrets    ${status}
    Should Be Equal    ${leaks}    ${False}    msg=stored policy carries RGW SSL material
    ${refusal}=    Placement Refusal Text    ${status}
    Should Be Empty    ${refusal}    msg=a successful apply must clear the refusal: ${refusal}

Test TLS Intent Without Material Reuses The Local Pair
    [Documentation]    A supplied ssl:true with NO pair requests reuse of the member's
    ...    valid local material: the frontend stays on 443, the served
    ...    certificate fingerprint is unchanged, the daemon is not restarted,
    ...    and the old CA still validates the handshake. Reuse never falls back
    ...    to plaintext.
    [Tags]    rgw    placement    tls
    ${before}=    RGW Service Invocation ID In VM
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":true}}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=TLS reuse policy failed: ${resp}
    Wait For Member RGW Frontend    ${MEMBER}    ssl=${True}
    ${fp}=    RGW TLS Fingerprint In VM    localhost    443
    Should Be Equal    ${fp}    ${FINGERPRINT_A}    msg=reuse must keep serving the same certificate, not disable TLS
    ${after}=    RGW Service Invocation ID In VM
    Should Be Equal    ${after}    ${before}    msg=reuse of unchanged material must not restart RGW
    RGW Endpoint Serves In VM    localhost    443    /testbucketssl/tlsonly.txt    hello-rgw-placement-tlsonly    ca_cert=${PAIR_A}[ca]

Test Certificate Rotation Serves The New Generation
    [Documentation]    Submitting a second pair on the same ports rotates the served
    ...    certificate: the fingerprint changes, the new CA validates a trusted
    ...    handshake, the old CA no longer does, and neither generation's
    ...    material leaks anywhere. The working generation is replaced wholesale;
    ...    nothing of the old one lingers.
    [Tags]    rgw    placement    tls    secrets
    ${body_file}=    Write RGW TLS Policy In VM    ${MEMBER}    0    443    rgwb
    ${resp}=    MicroCeph API Put From File In VM    placement    ${body_file}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=certificate rotation failed: ${resp}
    Wait For RGW SSL Port    localhost    443
    ${fp}=    RGW TLS Fingerprint In VM    localhost    443
    Should Not Be Equal    ${fp}    ${FINGERPRINT_A}    msg=rotation must serve the new certificate
    Exercise RGW S3 In VM    host=localhost    port=443    ssl=${True}    ca_cert=${PAIR_B}[ca]    filename=rotated
    Run In VM Must Fail    curl -sS --cacert ${PAIR_A}[ca] https://localhost/testbucketssl/rotated.txt    30
    Assert RGW Material Absent In VM    rgwb
    Assert RGW Material Absent In VM    rgwa
    ${status}=    Get Placement Status JSON
    ${leaks}=    RGW Policy Leaks Secrets    ${status}
    Should Be Equal    ${leaks}    ${False}
    ${refusal}=    Placement Refusal Text    ${status}
    Should Be Empty    ${refusal}

Test Dual Listeners On Explicit Ports
    [Documentation]    ssl:true with an explicit plaintext port runs both listeners:
    ...    the observed frontend reports port 8080 and ssl_port 8443 with ssl
    ...    true, and S3 content round-trips over each port with the right scheme.
    [Tags]    rgw    placement    tls
    ${body_file}=    Write RGW TLS Policy In VM    ${MEMBER}    8080    8443    rgwb
    ${resp}=    MicroCeph API Put From File In VM    placement    ${body_file}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=dual-listener placement failed: ${resp}
    Wait For Member RGW Frontend    ${MEMBER}    port=8080    ssl=${True}
    Wait For RGW SSL Port    localhost    8443
    ${status}=    Get Placement Status JSON
    ${fe}=    Member RGW Frontend Status    ${status}    ${MEMBER}
    Should Be Equal As Integers    ${fe}[port]    8080
    Should Be Equal As Integers    ${fe}[ssl_port]    8443
    ${conf}=    Get RGW Frontend Conf Ports
    Should Be Equal As Integers    ${conf}[port]    8080
    Should Be Equal As Integers    ${conf}[ssl_port]    8443
    Exercise RGW S3 In VM    host=localhost    port=8080    filename=dualhttp
    Exercise RGW S3 In VM    host=localhost    port=8443    ssl=${True}    ca_cert=${PAIR_B}[ca]    filename=dualtls

Test Explicit Downgrade To Plaintext Closes TLS
    [Documentation]    Downgrading requires the explicit ssl:false: the frontend returns
    ...    to plaintext on the default 80, the TLS ports close, and the leftover
    ...    certificate material files are removed. A policy that omits ssl
    ...    cannot silently downgrade TLS.
    [Tags]    rgw    placement    tls
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":false}}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=explicit downgrade failed: ${resp}
    Wait For Member RGW Frontend    ${MEMBER}    port=80    ssl=${False}
    RGW Endpoint Closed In VM    localhost    8443
    RGW Endpoint Closed In VM    localhost    443
    Assert No RGW TLS Material Remains In VM
    Exercise RGW S3 In VM    host=localhost    port=80    filename=downgraded

Test Stopped RGW Recovers On Reapply
    [Documentation]    A stopped-but-configured gateway is recovered by repeating the
    ...    policy: matching files are not enough, liveness is part of
    ...    reconciliation, and the service serves again on its configured port.
    [Tags]    rgw    placement    idempotency
    Run In VM And Check    sudo snap stop microceph.rgw    60
    Wait For RGW Unit State In VM    inactive=${True}
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":false}}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=stopped-service recovery failed: ${resp}
    Wait For RGW Unit State In VM
    Wait For RGW    1
    RGW Endpoint Serves In VM    localhost    80    /testbucket/downgraded.txt    hello-rgw-placement-downgraded

Test Invalid Request Mutates No Accepted State
    [Documentation]    With a policy active, a mismatched (valid-base64, wrong-key) pair is
    ...    still rejected with 400, and the previously accepted intent, running
    ...    service, and process identity are untouched: validation precedes both
    ...    persistence and dispatch.
    [Tags]    placement    tls
    ${status_before}=    Get Placement Status JSON
    ${intent_before}=    Stored RGW Intent    ${status_before}    ${MEMBER}
    ${inv_before}=    RGW Service Invocation ID In VM
    ${body_file}=    Write RGW TLS Policy In VM    ${MEMBER}    0    443    rgwa    key_prefix=rgwb
    ${resp}=    MicroCeph API Put From File In VM    placement    ${body_file}    timeout=120
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    400    msg=mismatched pair must be rejected with HTTP 400: ${resp}
    ${status_after}=    Get Placement Status JSON
    ${intent_after}=    Stored RGW Intent    ${status_after}    ${MEMBER}
    Dictionaries Should Be Equal    ${intent_after}    ${intent_before}    msg=invalid request changed the stored policy
    ${inv_after}=    RGW Service Invocation ID In VM
    Should Be Equal    ${inv_after}    ${inv_before}    msg=invalid request must not touch the running service
    Wait For Member RGW Frontend    ${MEMBER}    port=80    ssl=${False}    tries=6

Test Omitted RGW Field Leaves The Service Untouched
    [Documentation]    A member entry without an rgw key is unmanaged: the running
    ...    gateway, its observed frontend, and its process identity all stay
    ...    exactly as they were.
    [Tags]    placement
    ${inv_before}=    RGW Service Invocation ID In VM
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=omission policy failed: ${resp}
    ${inv_after}=    RGW Service Invocation ID In VM
    Should Be Equal    ${inv_after}    ${inv_before}    msg=omitted rgw must leave the service untouched
    Wait For Member RGW Frontend    ${MEMBER}    port=80    ssl=${False}    tries=6
    RGW Endpoint Serves In VM    localhost    80    /testbucket/downgraded.txt    hello-rgw-placement-downgraded

Test Empty Waiting Policy Leaves Services Running
    [Documentation]    The empty members map is the waiting policy: it is stored (active)
    ...    but performs no service operations, so the gateway keeps serving.
    [Tags]    placement
    ${policy}=    Set Variable    {"mode":"reconcile","members":{}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=empty policy failed: ${resp}
    ${active}=    Placement Policy Active
    Should Be Equal    ${active}    ${True}    msg=empty policy must be stored and active
    ${status}=    Get Placement Status JSON
    ${intent}=    Stored RGW Intent    ${status}    ${MEMBER}
    Should Be Empty    ${intent}    msg=empty policy must not carry member intent
    RGW Endpoint Serves In VM    localhost    80    /testbucket/downgraded.txt    hello-rgw-placement-downgraded

Test Deleting The Policy Stands Down Without Touching Services
    [Documentation]    DELETE clears the desired state entirely: the policy is gone
    ...    (inactive, no stored intent) but the running service is untouched.
    [Tags]    placement
    ${resp}=    MicroCeph API Delete    placement
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=policy deletion failed: ${resp}
    ${active}=    Placement Policy Active
    Should Be Equal    ${active}    ${False}    msg=deleted policy must leave no active state
    ${status}=    Get Placement Status JSON
    ${intent}=    Stored RGW Intent    ${status}    ${MEMBER}
    Should Be Empty    ${intent}
    RGW Endpoint Serves In VM    localhost    80    /testbucket/downgraded.txt    hello-rgw-placement-downgraded

Test Explicit Disable Works After Partial Cleanup And Repeats
    [Documentation]    enabled:false removes the gateway even from a partially
    ...    cleaned-up state (config file already deleted, unit already
    ...    stopped): removal is idempotent, the observed frontend drops away,
    ...    and repeating the disable succeeds with nothing left to clean.
    [Tags]    rgw    placement
    Run In VM And Check    sudo rm -f /var/snap/microceph/current/conf/radosgw.conf    30
    Run In VM And Check    sudo snap stop microceph.rgw    60
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":false}}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=disable after partial cleanup failed: ${resp}
    Wait For RGW Unit State In VM    inactive=${True}
    Wait For Member RGW Frontend    ${MEMBER}    present=${False}
    ${status}=    Get Placement Status JSON
    ${flags}=    Observed RGW Flags    ${status}
    Should Be Equal    ${flags}[${MEMBER}]    ${False}    msg=member must not be observed running RGW after disable
    ${intent}=    Stored RGW Intent    ${status}    ${MEMBER}
    Should Be Equal    ${intent}[enabled]    ${False}
    Assert No RGW TLS Material Remains In VM
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=repeated disable must succeed with nothing left to clean: ${resp}

Test Failed TLS Reuse Never Falls Back To Plaintext
    [Documentation]    With no valid local material on the member (the disable above
    ...    removed it), ssl:true without a pair must fail closed. The implementation
    ...    classifies a missing usable local certificate/key pair as an operational
    ...    failure (ceph.ErrPlacementOperationFailed), so the exact status is HTTP 500,
    ...    not merely "not 200". The contract also guarantees no plaintext listener,
    ...    TLS listener, or observed frontend appears as a side effect, and that the
    ...    validated intent (enabled, ssl, no material) is retained for observability.
    [Tags]    rgw    placement    tls
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${MEMBER}":{"rgw":{"enabled":true,"ssl":true}}}}
    ${resp}=    MicroCeph API Put    placement    ${policy}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    500
    ...    msg=TLS reuse without local material must fail with the operational-failure status (500), not silently downgrade or serve: ${resp}
    Wait For RGW Unit State In VM    inactive=${True}
    RGW Endpoint Closed In VM    localhost    443
    RGW Endpoint Closed In VM    localhost    80
    ${status}=    Get Placement Status JSON
    ${fe}=    Member RGW Frontend Status    ${status}    ${MEMBER}
    Should Be Empty    ${fe}    msg=no frontend may be reported after a failed TLS-only apply: ${fe}
    ${intent}=    Stored RGW Intent    ${status}    ${MEMBER}
    Should Be Equal    ${intent}[enabled]    ${True}    msg=validated desired intent must be retained for observability
    Should Be Equal    ${intent}[ssl]    ${True}
    ${leaks}=    RGW Policy Leaks Secrets    ${status}
    Should Be Equal    ${leaks}    ${False}    msg=failed apply must not store key material either
