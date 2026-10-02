*** Settings ***
Documentation    rgw-placement-multivm-tests
...    Role-managed RGW placement on three INDEPENDENT guest VMs (real LXD
...    virtual machines, not containers inside one outer VM), so member
...    unreachability, per-machine service state, and cross-member API traffic
...    behave as they do on a real cluster edge.
...    Covers the multi-machine contract that containers cannot prove:
...      - a policy submitted from one member drives a gateway on another,
...        and S3 traffic crosses VM boundaries over the observed public port,
...      - migrating a gateway keeps the object it serves readable for the whole
...        apply (add-before-remove), proven by sampling real S3 object reads --
...        not a Ceph daemon count, which can lag the real service state --
...        while the PUT is in flight,
...      - concurrent cross-member applies serialize on the cluster-wide apply
...        lock: one succeeds, the other gets an exact HTTP 409,
...      - an unreachable member surfaces an operational failure (500), does
...        not starve later additions, holds the removals a failed addition
...        would have taken, and is named in the recorded refusal,
...      - a down member's last-successful frontend stays observable from
...        other members without contacting it,
...      - a combined failure (unreachable-member RGW add + keep-one control
...        refusal) retains BOTH causes in one response and refusal while
...        removing nothing,
...      - scale-to-zero removes every gateway across machines and retries
...        converge.
...    Guests are bounded and overridable via ${RGW_VM_CPU}/${RGW_VM_MEMORY}/
...    ${RGW_VM_DISK} (defaults sized for an 8 GiB / 25 GiB tooling host);
...    they are launched and provisioned sequentially to keep peak usage flat.
Resource        ../resources/microceph_harness.resource
Resource        ../resources/rgw_placement.resource
Suite Setup     RGW MultiVM Suite Setup
Suite Teardown  RGW MultiVM Suite Teardown
Test Tags       multi-node    rgw    placement    vm    lxd    integration    slow

*** Variables ***
${GUEST_VM0}    rgw-mvm-coordinator
${GUEST_VM1}    rgw-mvm-later
${GUEST_VM2}    rgw-mvm-first
# Bounded guest profile: 3 guests fit beside each other on one small tooling
# host. Override per environment, e.g. --variable RGW_VM_MEMORY:3GiB.
${RGW_VM_CPU}       2
${RGW_VM_MEMORY}    2GiB
${RGW_VM_DISK}      6GiB

*** Keywords ***
RGW MultiVM Suite Setup
    [Documentation]    Launches the three guest VMs sequentially (launch, snap
    ...    copy, tools, install, socket), bootstraps MicroCeph on vm0, joins vm1
    ...    and vm2 as named members, adds one loop-file OSD per member so RGW
    ...    pools can place, and waits for HEALTH_OK.
    FOR    ${vm}    IN    ${GUEST_VM0}    ${GUEST_VM1}    ${GUEST_VM2}
        Launch Guest Test VM    ${vm}
        Copy Snap To VM    vm_name=${vm}
        Install Tools    vm_name=${vm}
        Install MicroCeph From Local Snap    vm_name=${vm}
        Wait For MicroCeph Control Socket    tries=36    vm_name=${vm}
    END
    Log To Console    [rgw-mvm] Bootstrapping cluster on ${GUEST_VM0}...
    Run In VM And Check    sudo microceph cluster bootstrap    300    vm_name=${GUEST_VM0}
    # Debug logging on every member so the RGW leak/journal checks that run later
    # are meaningful: at the default level the daemon never logs anything a leak
    # could hide in.
    Run In VM And Check    sudo microceph log set-level debug    30    vm_name=${GUEST_VM0}
    ${tok1}=    Run In VM    sudo microceph cluster add ${GUEST_VM1}    60    vm_name=${GUEST_VM0}
    Run In VM And Check    sudo microceph cluster join ${tok1.stdout.strip()}    300    vm_name=${GUEST_VM1}
    Run In VM And Check    sudo microceph log set-level debug    30    vm_name=${GUEST_VM1}
    Wait For Cluster Members In VM    ${GUEST_VM0}    ${GUEST_VM1}    vm_name=${GUEST_VM0}
    ${tok2}=    Run In VM    sudo microceph cluster add ${GUEST_VM2}    60    vm_name=${GUEST_VM0}
    Run In VM And Check    sudo microceph cluster join ${tok2.stdout.strip()}    300    vm_name=${GUEST_VM2}
    Run In VM And Check    sudo microceph log set-level debug    30    vm_name=${GUEST_VM2}
    Wait For Cluster Members In VM    ${GUEST_VM0}    ${GUEST_VM1}    ${GUEST_VM2}    vm_name=${GUEST_VM0}
    FOR    ${vm}    IN    ${GUEST_VM0}    ${GUEST_VM1}    ${GUEST_VM2}
        Run In VM And Check    sudo microceph disk add loop,1G,1    300    vm_name=${vm}
    END
    Wait For OSD Count In VM    3    vm_name=${GUEST_VM0}
    Wait For Cluster Health OK In VM    vm_name=${GUEST_VM0}
    Log To Console    [rgw-mvm] 3-member cluster healthy on independent VMs

RGW MultiVM Suite Teardown
    [Documentation]    Best-effort diagnostics on failure, then destroy all guest VMs.
    Teardown Named VMs    ${GUEST_VM0}    ${GUEST_VM1}    ${GUEST_VM2}

Get Guest VM IP
    [Documentation]    Returns the primary bridge IP of a guest VM (its public
    ...    RGW address for cross-machine traffic).
    [Arguments]    ${vm}
    ${res}=    Run In VM    hostname -I | cut -d ' ' -f1    15    vm_name=${vm}
    RETURN    ${res.stdout.strip()}

*** Test Cases ***
Test Cross Machine Enable Serves S3 From Another Member
    [Documentation]    A policy PUT submitted through vm0's own control socket enables
    ...    RGW on vm1; the observed frontend reports the public port, and S3
    ...    content round-trips from vm2 to vm1's address: the gateway serves
    ...    real traffic across machine boundaries, not only on localhost.
    [Tags]    placement    rgw
    ${ip1}=    Get Guest VM IP    ${GUEST_VM1}
    Set Suite Variable    ${VM1_IP}    ${ip1}
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM1}":{"rgw":{"enabled":true,"ssl":false,"port":8080}}}}
    ${resp}=    MicroCeph API Put In VM    ${GUEST_VM0}    placement    ${policy}    timeout=600
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=cross-member enable failed: ${resp}
    Wait For RGW Count In VM    1    vm_name=${GUEST_VM0}
    Wait For Member RGW Frontend    ${GUEST_VM1}    port=8080    ssl=${False}    vm_name=${GUEST_VM0}
    Exercise RGW S3 In VM    ${VM1_IP}    8080    vm_name=${GUEST_VM2}    filename=crossvm

Test Port Update Requested From Another Member
    [Documentation]    The same policy is then updated through vm2's socket: the observed
    ...    port moves to 8081, the old port closes as seen from another member,
    ...    and the new port serves S3.
    [Tags]    placement    rgw
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM1}":{"rgw":{"enabled":true,"ssl":false,"port":8081}}}}
    ${resp}=    MicroCeph API Put In VM    ${GUEST_VM2}    placement    ${policy}    timeout=600
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=cross-member port update failed: ${resp}
    Wait For Member RGW Frontend    ${GUEST_VM1}    port=8081    ssl=${False}    vm_name=${GUEST_VM0}
    RGW Endpoint Closed In VM    ${VM1_IP}    8080    vm_name=${GUEST_VM2}
    Exercise RGW S3 In VM    ${VM1_IP}    8081    vm_name=${GUEST_VM2}    filename=crossvm2

Test Migration Keeps A Gateway Serving Throughout
    [Documentation]    One policy moves the gateway vm1 -> vm2. A sampler running
    ...    inside vm0 reads the real S3 object /testbucket/crossvm2.txt (uploaded
    ...    earlier through vm1) every 100 ms from vm2's then vm1's address for the
    ...    whole time the background PUT is in flight: genuine object reads, not
    ...    a Ceph daemon count (which lags the real service state), and fast
    ...    enough that even a sub-second apply yields samples. add-before-remove
    ...    means every in-flight sample must be served by one of the two
    ...    gateways, and the replacement (vm2) must be seen serving before the
    ...    apply finishes. Afterwards vm2 serves on the moved port, vm1's unit is
    ...    stopped, and vm1's observed frontend is gone.
    [Tags]    placement    rgw    migration
    ${ip2}=    Get Guest VM IP    ${GUEST_VM2}
    Set Suite Variable    ${VM2_IP}    ${ip2}
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM1}":{"rgw":{"enabled":false}},"${GUEST_VM2}":{"rgw":{"enabled":true,"ssl":false,"port":8081}}}}
    Start RGW Migration Sampler In VM    ${GUEST_VM0}    migrate    ${VM1_IP}    ${VM2_IP}    8081
    ...    /testbucket/crossvm2.txt    hello-rgw-placement-crossvm2
    Start Placement Put In VM    ${GUEST_VM0}    ${policy}    migrate
    ${resp}=    Wait For Background Put In VM    ${GUEST_VM0}    migrate    timeout=600
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=gateway migration failed: ${resp}
    ${observation}=    Collect RGW Migration Samples In VM    ${GUEST_VM0}    migrate
    Should Be True    ${observation}[samples] > 0    msg=no in-flight object-read samples were taken during the migration: ${observation}
    Should Be Equal    ${observation}[available]    ${True}
    ...    msg=the object went unavailable during migration (add-before-remove violated): ${observation}
    Should Be Equal    ${observation}[replacement_ready]    ${True}
    ...    msg=the replacement (vm2) was never observed serving the object before the apply finished: ${observation}
    Wait For Member RGW Frontend    ${GUEST_VM2}    port=8081    ssl=${False}    vm_name=${GUEST_VM0}
    Wait For Member RGW Frontend    ${GUEST_VM1}    present=${False}    vm_name=${GUEST_VM0}
    Wait For RGW Unit State In VM    inactive=${True}    vm_name=${GUEST_VM1}
    Exercise RGW S3 In VM    ${VM2_IP}    8081    vm_name=${GUEST_VM1}    filename=migrated

Test Concurrent Cross Member Applies Return 409
    [Documentation]    Two placement PUTs applied by different members are made to
    ...    genuinely OVERLAP: PUT A (two gateway additions, applied by vm0) and
    ...    PUT B (forwarded to and applied by vm1 through ?target=) are launched
    ...    from one shell milliseconds apart, far inside the time A needs. The
    ...    cluster-wide lock is only ever held while an apply is running, so an
    ...    exact HTTP 409 for B is itself the proof that B arrived while A was
    ...    applying; A must then finish 200. Whether A was still unfinished when
    ...    B's response was read back is logged for context only, because that
    ...    read-back costs another round trip A can complete within. One retry
    ...    is allowed for the rare case where A completes before B reaches the
    ...    lock; a second failure to observe the 409 is a real lock defect.
    [Tags]    placement    concurrency
    ${clear}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM1}":{"rgw":{"enabled":false}},"${GUEST_VM2}":{"rgw":{"enabled":false}}}}
    ${policy_a}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM1}":{"rgw":{"enabled":true,"ssl":false,"port":8080}},"${GUEST_VM2}":{"rgw":{"enabled":true,"ssl":false,"port":8080}}}}
    ${policy_b}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM2}":{"rgw":{"enabled":true,"ssl":false,"port":8081}}}}
    ${overlapped}=    Set Variable    ${False}
    FOR    ${attempt}    IN RANGE    2
        # Stand down first so the winner's apply performs fresh (slow) additions,
        # giving B a real window to land while A is still running.
        ${resp}=    MicroCeph API Put In VM    ${GUEST_VM0}    placement    ${clear}    timeout=600
        ${code}=    Response Status Code    ${resp}
        Should Be Equal As Integers    ${code}    200    msg=conflict reset failed: ${resp}
        Wait For RGW Unit State In VM    inactive=${True}    vm_name=${GUEST_VM1}
        Wait For RGW Unit State In VM    inactive=${True}    vm_name=${GUEST_VM2}
        Start Concurrent Placement Puts In VM    ${GUEST_VM0}    ${policy_a}    conflict-a    ${policy_b}    conflict-b    ${GUEST_VM1}
        ${resp_b}=    Wait For Background Put In VM    ${GUEST_VM0}    conflict-b    timeout=600
        ${a_done_at_b}=    Background Put Done In VM    ${GUEST_VM0}    conflict-a
        ${code_b}=    Response Status Code    ${resp_b}
        ${resp_a}=    Wait For Background Put In VM    ${GUEST_VM0}    conflict-a    timeout=600
        ${code_a}=    Response Status Code    ${resp_a}
        Log To Console    [conflict] attempt ${attempt}: code_a=${code_a} code_b=${code_b} a_done_when_b_finished=${a_done_at_b}
        IF    ${code_b} == 409 and ${code_a} == 200
            ${overlapped}=    Set Variable    ${True}
            Exit For Loop
        ELSE IF    ${attempt} == 0
            Log To Console    [conflict] attempt ${attempt}: no overlap observed, retrying once
        ELSE
            Fail    concurrent applies never overlapped after 2 attempts: code_a=${code_a} code_b=${code_b}
        END
    END
    Should Be True    ${overlapped}    msg=concurrent cross-member applies never produced a proven-overlapping 409

Test Down Member Frontend Stays Observable
    [Documentation]    vm2 serves on 8081, then its VM is stopped. GET /placement on
    ...    vm0 still reports vm2's rgw=true with its last-successful frontend
    ...    exactly as before: the observed status is database-backed and needs
    ...    no member contact. (It reports last successfully applied settings,
    ...    not live health.)
    [Tags]    placement    observation
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM1}":{"rgw":{"enabled":false}},"${GUEST_VM2}":{"rgw":{"enabled":true,"ssl":false,"port":8081}}}}
    ${resp}=    MicroCeph API Put In VM    ${GUEST_VM0}    placement    ${policy}    timeout=600
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=pre-stop policy failed: ${resp}
    Wait For Member RGW Frontend    ${GUEST_VM2}    port=8081    ssl=${False}    vm_name=${GUEST_VM0}
    ${status_before}=    Get Placement Status JSON In VM    ${GUEST_VM0}
    ${fe_before}=    Member RGW Frontend Status    ${status_before}    ${GUEST_VM2}
    Stop Named VM    ${GUEST_VM2}
    ${status_after}=    Get Placement Status JSON In VM    ${GUEST_VM0}
    ${fe_after}=    Member RGW Frontend Status    ${status_after}    ${GUEST_VM2}
    Dictionaries Should Be Equal    ${fe_after}    ${fe_before}    msg=down member's last-successful frontend must stay observable without member contact
    ${flags}=    Observed RGW Flags    ${status_after}
    Should Be Equal    ${flags}[${GUEST_VM2}]    ${True}    msg=down member must still be reported hosting RGW

Test Unreachable Member Fails Operationally And Does Not Starve Later Additions
    [Documentation]    The first sorted target is down. A later target must still
    ...    receive its gateway, while the failed target's intent stays visible.
    [Tags]    placement    rgw    faults
    # Dropping a member triggers a dqlite election; the member list the apply
    # validates against is only answerable again once that has settled.
    Wait For Microceph Ready In VM    ${GUEST_VM0}    tries=36
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM1}":{"rgw":{"enabled":true,"ssl":false,"port":8080}},"${GUEST_VM2}":{"rgw":{"enabled":true,"ssl":false,"port":8080}}}}
    ${resp}=    MicroCeph API Put In VM    ${GUEST_VM0}    placement    ${policy}    timeout=600
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    500    msg=unreachable member must surface an operational failure (500): ${resp}
    Wait For Member RGW Frontend    ${GUEST_VM1}    port=8080    ssl=${False}    vm_name=${GUEST_VM0}
    Wait For RGW Unit State In VM    vm_name=${GUEST_VM1}
    Exercise RGW S3 In VM    ${VM1_IP}    8080    vm_name=${GUEST_VM0}    filename=starved
    ${status}=    Get Placement Status JSON In VM    ${GUEST_VM0}
    ${refusal}=    Placement Refusal Text    ${status}
    Should Not Be Empty    ${refusal}    msg=failed apply must record its refusal
    Should Contain    ${refusal}    ${GUEST_VM2}    msg=refusal must name the unreachable member: ${refusal}
    ${intent}=    Stored RGW Intent    ${status}    ${GUEST_VM2}
    Should Be Equal    ${intent}[enabled]    ${True}    msg=validated intent for the unreachable member must be retained

Test Failed Addition Holds The Removal
    [Documentation]    With vm2 still down, a policy disabling vm1's gateway while
    ...    enabling one on vm2 must NOT remove vm1's: removals are deferred
    ...    while any addition fails, so a failed replacement never takes the
    ...    last serving gateway offline. The 500, the refusal naming vm2, and
    ...    the retained desired intent are all asserted.
    [Tags]    placement    rgw    faults
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM1}":{"rgw":{"enabled":false}},"${GUEST_VM2}":{"rgw":{"enabled":true,"ssl":false,"port":8081}}}}
    ${resp}=    MicroCeph API Put In VM    ${GUEST_VM0}    placement    ${policy}    timeout=600
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    500    msg=held-removal policy must fail operationally: ${resp}
    Wait For Member RGW Frontend    ${GUEST_VM1}    port=8080    ssl=${False}    tries=6    vm_name=${GUEST_VM0}
    RGW Endpoint Serves In VM    ${VM1_IP}    8080    /testbucket/starved.txt    hello-rgw-placement-starved    vm_name=${GUEST_VM0}
    ${status}=    Get Placement Status JSON In VM    ${GUEST_VM0}
    ${refusal}=    Placement Refusal Text    ${status}
    Should Contain    ${refusal}    ${GUEST_VM2}    msg=refusal must retain the addition failure cause: ${refusal}
    ${intent}=    Stored RGW Intent    ${status}    ${GUEST_VM1}
    Should Be Equal    ${intent}[enabled]    ${False}    msg=the deferred disable must remain the stored desired intent

Test Combined Refusal Retains Both Causes
    [Documentation]    One policy that removes ALL control services (keep-one must
    ...    refuse: every remaining retainer is itself a removal target) while
    ...    its RGW addition fails on the unreachable vm2 and its RGW removal
    ...    would take vm1's gateway: the response is an operational 500, the
    ...    recorded refusal retains BOTH the keep-one cause and the RGW member
    ...    failure, vm1 keeps serving (the removal is held), and the cluster
    ...    keeps its mons (the removals are refused).
    [Tags]    placement    control    faults
    ${mons_before}=    Mon Count In VM    vm_name=${GUEST_VM0}
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM0}":{"control":false},"${GUEST_VM1}":{"control":false,"rgw":{"enabled":false}},"${GUEST_VM2}":{"control":false,"rgw":{"enabled":true,"ssl":false,"port":8081}}}}
    ${resp}=    MicroCeph API Put In VM    ${GUEST_VM0}    placement    ${policy}    timeout=600
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    500    msg=combined refusal must surface the operational failure: ${resp}
    ${status}=    Get Placement Status JSON In VM    ${GUEST_VM0}
    ${refusal}=    Placement Refusal Text    ${status}
    Should Contain    ${refusal}    keep-one    msg=refusal must retain the control safety refusal: ${refusal}
    Should Contain    ${refusal}    ${GUEST_VM2}    msg=refusal must retain the RGW failure cause: ${refusal}
    RGW Endpoint Serves In VM    ${VM1_IP}    8080    /testbucket/starved.txt    hello-rgw-placement-starved    vm_name=${GUEST_VM0}
    ${mons}=    Mon Count In VM    vm_name=${GUEST_VM0}
    Should Be Equal As Integers    ${mons}    ${mons_before}    msg=keep-one must have refused every control removal: ${mons_before} mon(s) before, ${mons} after

Test Scale To Zero Removes Every Gateway And Retry Converges
    [Documentation]    vm2 is restarted and responsive again. A pure scale-to-zero
    ...    policy (no additions, so removals are NOT deferred) removes the
    ...    gateways from every member across machines: units stop, observed
    ...    rgw flags drop to false everywhere, and repeating the policy is an
    ...    idempotent success.
    [Tags]    placement    rgw
    Start Named VM    ${GUEST_VM2}
    Wait For Microceph Ready In VM    ${GUEST_VM2}    tries=36
    ${policy}=    Set Variable    {"mode":"reconcile","members":{"${GUEST_VM0}":{"rgw":{"enabled":false}},"${GUEST_VM1}":{"rgw":{"enabled":false}},"${GUEST_VM2}":{"rgw":{"enabled":false}}}}
    ${resp}=    MicroCeph API Put In VM    ${GUEST_VM0}    placement    ${policy}    timeout=600
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=scale-to-zero failed: ${resp}
    Wait For RGW Unit State In VM    inactive=${True}    vm_name=${GUEST_VM1}
    Wait For RGW Unit State In VM    inactive=${True}    vm_name=${GUEST_VM2}
    ${status}=    Get Placement Status JSON In VM    ${GUEST_VM0}
    ${flags}=    Observed RGW Flags    ${status}
    FOR    ${vm}    IN    ${GUEST_VM0}    ${GUEST_VM1}    ${GUEST_VM2}
        Should Be Equal    ${flags}[${vm}]    ${False}    msg=${vm} must not be observed running RGW after scale-to-zero
    END
    ${resp}=    MicroCeph API Put In VM    ${GUEST_VM0}    placement    ${policy}    timeout=600
    ${code}=    Response Status Code    ${resp}
    Should Be Equal As Integers    ${code}    200    msg=scale-to-zero retry must converge: ${resp}
