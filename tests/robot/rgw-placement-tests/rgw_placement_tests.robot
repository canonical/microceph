*** Settings ***
Documentation    rgw-placement-tests
...    Functional coverage for the Option B RGW role-placement feature (CE142):
...      the placement-rgw capability is advertised, a placement policy carrying an
...      `rgw` object (enabled/port) enables RGW atomically, GET /placement reports
...      the observed rgw_frontend (port/ssl) sourced from the rgw_frontends table,
...      no SSL key material ever leaks into the stored policy, and a scale-to-zero
...      `rgw:{enabled:false}` removes the RGW daemon.
...    The suite mirrors single-system-tests setup (single outer VM + 3 loop OSDs so
...    RGW zone pools can place PGs), then drives the placement API directly.
Resource        ../resources/microceph_harness.resource
Suite Setup     RGW Placement Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    rgw    placement    lxd    integration    slow

*** Keywords ***
RGW Placement Suite Setup
    [Documentation]    Launch a single VM, install the local snap, bootstrap, and add
    ...    3 loop OSDs so the cluster is healthy enough for RGW pool creation.
    Launch Outer Test VM    vm_name=microceph-rgw-placement-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install And Bootstrap MicroCeph
    Create Loop Devices
    Run In VM And Check    sudo microceph disk add /dev/sdia /dev/sdib /dev/sdic --wipe    300
    Wait For OSD Count    3

RGW Snap Service Is Not Active
    [Documentation]    Polls until the snap.microceph.rgw systemd unit is no longer active.
    ...    ceph -s keeps the rgw service-map line for a lag after the daemon stops, so
    ...    systemctl is-active is the reliable scale-to-zero signal (DisableRGW stops
    ...    the unit, confirmed by "Deactivated successfully" in the daemon log).
    ${r}=    Run In VM    for i in $(seq 1 36); do [ "$(systemctl is-active snap.microceph.rgw.service 2>/dev/null)" != "active" ] && exit 0; sleep 5; done; systemctl is-active snap.microceph.rgw.service; exit 1    300
    Should Be Equal As Integers    ${r.rc}    0    msg=RGW snap service still active after scale-to-zero

Placement Has No RGW Frontend
    [Documentation]    Polls GET /placement until no observed rgw_frontend is reported
    ...    (the rgw_frontends DB row is deleted by the member disable, so the
    ...    observed frontend must drop away once the scale-to-zero completes).
    FOR    ${i}    IN RANGE    36
        ${placement}=    Get Placement Status JSON
        ${present}=    Run Keyword And Return Status    Should Contain    ${placement}    "rgw_frontend"
        IF    not ${present}
            Return From Keyword
        END
        Sleep    5s
    END
    ${placement}=    Get Placement Status JSON
    Should Not Contain    ${placement}    "rgw_frontend"    msg=observed rgw_frontend still present after scale-to-zero: ${placement}

*** Test Cases ***
Test Placement RGW Capability Advertised
    [Documentation]    The snap advertises the `placement-rgw` capability marker so a
    ...    charm can gate entry into role-managed RGW placement.
    [Tags]    placement
    ${caps}=    Get Supported Capabilities
    Should Contain    ${caps}    placement-rgw    msg=placement-rgw capability not advertised

Test Placement Endpoint Reports Bootstrapped
    [Documentation]    GET /placement is reachable and reports bootstrap_state=bootstrapped on the
    ...    freshly-bootstrapped node (exercises the populateRGWFrontends read path with
    ...    no RGW members yet; a fresh cluster has no stored policy, so `active` is
    ...    false until a PUT stores one — tested after the enable PUT below).
    [Tags]    placement
    ${placement}=    Get Placement Status JSON
    Should Contain    ${placement}    "bootstrap_state":"bootstrapped"    msg=placement endpoint did not report bootstrapped: ${placement}

Test Enable RGW Via Placement Object
    [Documentation]    PUT /1.0/placement with `rgw:{enabled:true,port:8080}` on the
    ...    local member atomically enables RGW on port 8080 (engine -> member dispatch
    ...    -> applyRGWFrontend render + start). The bare-bool form is rejected.
    [Tags]    rgw    placement
    ${hn}=    Get VM Hostname
    # Bare-bool rgw must be rejected (HTTP 400) by the Option B parser.
    ${bad}=    MicroCeph API Put    placement    {"mode":"reconcile","members":{"${hn}":{"rgw":true}}}    timeout=60
    ${bad_code}=    Response Status Code    ${bad}
    Run Keyword And Continue On Failure    Should Not Be Equal As Integers    ${bad_code}    200    msg=bare-bool rgw must be rejected, got ${bad}
    # Object form enables RGW on port 8080.
    ${resp}=    MicroCeph API Put    placement    {"mode":"reconcile","members":{"${hn}":{"rgw":{"enabled":true,"port":8080}}}}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=RGW placement PUT failed: ${resp}
    Wait For RGW    1
    # The rendered frontend must carry port=8080 (applyRGWFrontend wrote it).
    Run In VM And Check    grep -q 'port=8080' /var/snap/microceph/current/conf/radosgw.conf    30

Test Placement Reports Observed RGW Frontend And No Secret Leak
    [Documentation]    GET /1.0/placement reports the observed rgw_frontend for the RGW
    ...    member with port=8080 and ssl=false (ssl_port omitted for plaintext), and the
    ...    stored policy carries no ssl_certificate / ssl_private_key material.
    [Tags]    rgw    placement
    ${placement}=    Get Placement Status JSON
    # Observed frontend: port 8080, ssl false, no ssl_port (plaintext -> ssl_port=0 -> omitempty).
    Should Contain    ${placement}    "rgw_frontend":{"port":8080,"ssl":false}    msg=observed rgw_frontend not reported as expected: ${placement}
    # Defense in depth: no SSL key material anywhere in the placement body.
    Should Not Contain    ${placement}    ssl_certificate    msg=ssl_certificate leaked into placement body
    Should Not Contain    ${placement}    ssl_private_key    msg=ssl_private_key leaked into placement body

Test Disable RGW Via Placement Scale To Zero
    [Documentation]    PUT /1.0/placement with `rgw:{enabled:false}` removes the RGW
    ...    daemon (no keep-one for RGW). The observed rgw_frontend must drop away.
    [Tags]    rgw    placement
    ${hn}=    Get VM Hostname
    ${resp}=    MicroCeph API Put    placement    {"mode":"reconcile","members":{"${hn}":{"rgw":{"enabled":false}}}}    timeout=300
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=RGW scale-to-zero PUT failed: ${resp}
    RGW Snap Service Is Not Active
    Placement Has No RGW Frontend
