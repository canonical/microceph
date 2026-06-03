*** Settings ***
Documentation    test-maintenance-modes
...    Tests cluster maintenance enter/exit with various flag combinations: dry-run,
...    --set-noout, --stop-osds, --force, and the quorum-safety guardrail.
Resource        ../resources/microceph_harness.resource
Suite Setup     Maintenance Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    maintenance    cluster    lxd    slow    integration

*** Keywords ***
Maintenance Suite Setup
    Launch Outer Test VM    vm_name=microceph-maint-vm    disk_size=50GiB
    Copy Scripts To VM
    Copy Snap To VM
    Clear IPTables
    Free Runner Disk
    Setup LXD In VM
    Create LXD Containers With Loop Devices    internal
    Install MicroCeph On All Nodes
    Bootstrap Head Node    internal
    Join Worker Nodes To Cluster    internal
    FOR    ${i}    IN RANGE    4
        Add OSD To Node    node-wrk${i}
    END
    Wait For OSD Count Head    4
    Sleep    30s    reason=Allow cluster to settle after OSD addition
    Verify Cluster Health Head Node

Is OSD Noout Set On
    [Documentation]    Returns yes if osd noout flag is set on the specified node.
    [Arguments]    ${node}
    ${result}=    Run In VM    lxc exec ${node} -- sh -c "microceph.ceph osd dump | grep noout > /dev/null 2>&1 && echo yes || echo no"    30
    RETURN    ${result.stdout.strip()}

Check Snap Service State On
    [Documentation]    Checks that microceph.<service> has given active/enabled state on node.
    [Arguments]    ${node}    ${service}    ${expected_active}    ${expected_enabled}
    ${result}=    Run In VM    lxc exec ${node} -- sh -c "systemctl is-active snap.microceph.${service} 2>/dev/null | grep -Fx '${expected_active}' > /dev/null 2>&1 && systemctl is-enabled snap.microceph.${service} 2>/dev/null | grep -Fx '${expected_enabled}' > /dev/null 2>&1 && echo yes || echo no"    30
    RETURN    ${result.stdout.strip()}

Wait For Noout State On
    [Documentation]    Polls until osd noout is in expected state (set/unset) on node.
    [Arguments]    ${node}    ${expected}    ${timeout}=120
    ${tries}=    Evaluate    int(${timeout}) // 5
    FOR    ${i}    IN RANGE    ${tries}
        ${state}=    Is OSD Noout Set On    ${node}
        IF    "${expected}" == "set" and "${state}" == "yes"    RETURN
        IF    "${expected}" == "unset" and "${state}" == "no"    RETURN
        Sleep    5s
    END
    Fail    Timed out waiting for osd noout to be '${expected}' on ${node}

Wait For Snap Service State On
    [Documentation]    Polls until microceph.<service> reaches expected active/enabled state on node.
    [Arguments]    ${node}    ${service}    ${expected_active}    ${expected_enabled}    ${timeout}=120
    ${tries}=    Evaluate    int(${timeout}) // 5
    FOR    ${i}    IN RANGE    ${tries}
        ${state}=    Check Snap Service State On    ${node}    ${service}    ${expected_active}    ${expected_enabled}
        IF    "${state}" == "yes"    RETURN
        Sleep    5s
    END
    Fail    Timed out waiting for microceph.${service} on ${node} to be ${expected_active}/${expected_enabled}

Wait For OSD Removed From Cluster
    [Documentation]    Polls until osd.${osd_id} is no longer visible in ceph osd info via ${head_node}.
    [Arguments]    ${head_node}    ${osd_id}
    FOR    ${i}    IN RANGE    8
        ${gone}=    Run In VM    lxc exec ${head_node} -- sh -c "microceph.ceph osd info osd.${osd_id} 2>/dev/null && echo exists || echo gone"    30
        IF    "${gone.stdout.strip()}" == "gone"
            Log To Console    [maintenance] osd.${osd_id} confirmed removed
            RETURN
        END
        Sleep    5s
    END
    Fail    OSD osd.${osd_id} was not removed from cluster within timeout

Remove Cluster Node With Retry
    [Documentation]    Removes ${node} from the cluster via ${head_node}, retrying on transient
    ...    'context canceled' RPC errors. The target may be busy rebalancing OSDs and not respond in time.
    [Arguments]    ${head_node}    ${node}    ${attempts}=3
    FOR    ${attempt}    IN RANGE    ${attempts}
        ${result}=    Run In VM    lxc exec ${head_node} -- bash -eo pipefail -c "microceph cluster remove ${node}"    120
        IF    ${result.rc} == 0    RETURN
        Log To Console    [maintenance] Remove ${node} attempt ${attempt} failed: ${result.stderr.strip()} — retrying in 10s
        IF    ${attempt} == ${attempts} - 1    Fail    Failed to remove ${node} after ${attempts} attempts: ${result.stderr}
        Sleep    10s
    END

Wait For Node Absent From Mons
    [Documentation]    Polls until ${node} no longer appears in the mon daemons list via ${head_node}.
    [Arguments]    ${head_node}    ${node}
    FOR    ${i}    IN RANGE    8
        ${in_mon}=    Node Is In Mon List    ${node}    ${head_node}
        IF    "${in_mon}" == "no"
            Log To Console    [maintenance] ${node} no longer in mon list
            RETURN
        END
        Sleep    5s
    END

Stop And Disable Mon On Container
    [Documentation]    Stops and disables the microceph mon service on ${container}.
    [Arguments]    ${container}
    Run In VM And Check    lxc exec ${container} -- sh -c "sudo systemctl stop snap.microceph.mon && sudo systemctl disable snap.microceph.mon"    30

Wait For Health To Mention
    [Documentation]    Polls ceph health via ${head_node} until ${substring} appears in the output.
    [Arguments]    ${head_node}    ${substring}    ${tries}=100
    Log To Console    [maintenance] Waiting for ceph health to mention '${substring}' (up to ${tries}s)...
    FOR    ${i}    IN RANGE    ${tries}
        ${health}=    Run In VM    lxc exec ${head_node} -- sh -c "microceph.ceph health 2>/dev/null || true"    15
        IF    "${substring}" in $health.stdout
            Log To Console    [maintenance] '${substring}' detected in health after ${i}s
            RETURN
        END
        Sleep    1s
    END
    Fail    ceph health never mentioned '${substring}' after ${tries} attempts

Run Dry Run Maintenance Enter
    [Documentation]    Runs maintenance enter --dry-run with given flags on node from itself.
    [Arguments]    ${node}    ${flags}
    ${result}=    Run In VM    lxc exec ${node} -- sh -c "microceph cluster maintenance enter ${node} ${flags} --dry-run 2>&1"    60
    Should Be Equal As Integers    ${result.rc}    0    msg=dry-run should succeed: ${result.stdout}
    RETURN    ${result.stdout}

Test Dry Run Maintenance Enter Inline
    [Documentation]    Verifies dry-run prints the expected action plan for all flag combos.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing dry-run maintenance enter on ${node}...
    Run In Container    ${node}    microceph status    30
    Run In Container    ${node}    microceph.ceph -s    30
    ${out}=    Run Dry Run Maintenance Enter    ${node}    --set-noout=false --stop-osds=false
    Should Contain    ${out}    Check if osds    msg=Expected ok-to-stop preflight
    Should Contain    ${out}    Check if there are at least a majority of mon services    msg=Expected non-OSD service preflight
    Should Not Contain    ${out}    Run `ceph osd set noout`    msg=Unexpected noout action
    Should Not Contain    ${out}    Stop osd service in node '${node}'    msg=Unexpected stop-osd action
    ${out}=    Run Dry Run Maintenance Enter    ${node}    --set-noout=false --stop-osds=true
    Should Contain    ${out}    Check if osds    msg=Expected ok-to-stop preflight
    Should Contain    ${out}    Stop osd service in node '${node}'    msg=Expected stop-osd action
    Should Not Contain    ${out}    Run `ceph osd set noout`    msg=Unexpected noout action with set-noout=false
    ${out}=    Run Dry Run Maintenance Enter    ${node}    --set-noout=true --stop-osds=false
    Should Contain    ${out}    Run `ceph osd set noout`    msg=Expected set-noout action
    Should Contain    ${out}    Assert osd has 'noout' flag set    msg=Expected noout assertion
    Should Not Contain    ${out}    Stop osd service in node '${node}'    msg=Unexpected stop-osd action
    ${out}=    Run Dry Run Maintenance Enter    ${node}    --set-noout=true --stop-osds=true
    Should Contain    ${out}    Check if osds    msg=Expected ok-to-stop preflight
    Should Contain    ${out}    Check if there are at least a majority of mon services    msg=Expected mon preflight
    Should Contain    ${out}    Run `ceph osd set noout`    msg=Expected set-noout action
    Should Contain    ${out}    Assert osd has 'noout' flag set    msg=Expected noout assertion
    Should Contain    ${out}    Stop osd service in node '${node}'    msg=Expected stop-osd action
    Log To Console    [maintenance] PASSED: dry-run maintenance enter

Test Dry Run Maintenance Exit Inline
    [Documentation]    Verifies dry-run exit prints expected action plan.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing dry-run maintenance exit on ${node}...
    Run In Container    ${node}    microceph status    30
    Run In Container    ${node}    microceph.ceph -s    30
    ${result}=    Run In VM    lxc exec ${node} -- sh -c "microceph cluster maintenance exit ${node} --dry-run 2>&1"    60
    Should Be Equal As Integers    ${result.rc}    0    msg=dry-run exit should succeed
    Should Contain    ${result.stdout}    Run `ceph osd unset noout`    msg=Expected unset-noout action
    Should Contain    ${result.stdout}    Assert osd has 'noout' flag unset    msg=Expected noout assertion
    Should Contain    ${result.stdout}    Start osd service in node '${node}'    msg=Expected start-osd action
    Log To Console    [maintenance] PASSED: dry-run maintenance exit

Run Maintenance Enter Exit Cycle
    [Documentation]    Runs ${count} idempotent enter/exit cycles on ${node}.
    ...    ${enter_flags} is appended to the enter command (e.g. "--set-noout=true --stop-osds=true").
    ...    ${enter_noout} ("set"/"unset") and ${enter_svc_active}/${enter_svc_enabled}
    ...    ("active"/"inactive", "enabled"/"disabled") specify expected states after enter.
    ...    Exit always restores noout=unset and osd=active/enabled.
    [Arguments]    ${node}    ${enter_flags}    ${enter_noout}    ${enter_svc_active}    ${enter_svc_enabled}    ${count}=3
    Run In Container    ${node}    microceph status    30
    ${noout_result}=    Set Variable If    "${enter_noout}" == "set"    yes    no
    FOR    ${i}    IN RANGE    ${count}
        Log To Console    [maintenance] Enter ${i}...
        Run In Container    ${node}    microceph cluster maintenance enter ${enter_flags} ${node}    120
        Wait For Noout State On    ${node}    ${enter_noout}    120
        Wait For Snap Service State On    ${node}    osd    ${enter_svc_active}    ${enter_svc_enabled}    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    ${noout_result}    msg=Unexpected noout state after enter
        ${svc}=    Check Snap Service State On    ${node}    osd    ${enter_svc_active}    ${enter_svc_enabled}
        Should Be Equal As Strings    ${svc}    yes    msg=Unexpected OSD service state after enter
    END
    FOR    ${i}    IN RANGE    ${count}
        Log To Console    [maintenance] Exit ${i}...
        Run In Container    ${node}    microceph cluster maintenance exit ${node}    120
        Wait For Noout State On    ${node}    unset    120
        Wait For Snap Service State On    ${node}    osd    active    enabled    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    no    msg=noout should be unset after exit
        ${svc}=    Check Snap Service State On    ${node}    osd    active    enabled
        Should Be Equal As Strings    ${svc}    yes    msg=OSD service should be active/enabled after exit
    END

Test Maintenance Enter And Exit Inline
    [Documentation]    Enters/exits maintenance (--set-noout=false --stop-osds=false) 3× each.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing maintenance enter/exit (no-noout no-stop-osds) on ${node}...
    Run Maintenance Enter Exit Cycle    ${node}    --set-noout=false --stop-osds=false    unset    active    enabled

Test Maintenance Enter Set Noout Stop OSDs And Exit Inline
    [Documentation]    Enters/exits maintenance (--set-noout=true --stop-osds=true) 3× each.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing maintenance enter/exit (set-noout stop-osds) on ${node}...
    Run Maintenance Enter Exit Cycle    ${node}    --set-noout=true --stop-osds=true    set    inactive    disabled

Test Maintenance Enter And Exit Force Inline
    [Documentation]    Enters/exits maintenance with --force (--set-noout=false --stop-osds=false) 3× each.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing --force maintenance enter/exit (no-noout no-stop-osds) on ${node}...
    Run Maintenance Enter Exit Cycle    ${node}    --set-noout=false --stop-osds=false --force    unset    active    enabled

Test Maintenance Enter Set Noout Stop OSDs And Exit Force Inline
    [Documentation]    Enters/exits maintenance with --force (--set-noout=true --stop-osds=true) 3× each.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing --force maintenance enter/exit (set-noout stop-osds) on ${node}...
    Run Maintenance Enter Exit Cycle    ${node}    --set-noout=true --stop-osds=true --force    set    inactive    disabled

*** Test Cases ***
Test Dry Run Maintenance Enter
    [Documentation]    Verifies that maintenance enter --dry-run prints the expected action plan
    ...    for all combinations of --set-noout and --stop-osds flags.
    [Tags]    maintenance    cluster
    Test Dry Run Maintenance Enter Inline    node-wrk1

Test Dry Run Maintenance Exit
    [Documentation]    Verifies that maintenance exit --dry-run prints the expected action plan.
    [Tags]    maintenance    cluster
    Test Dry Run Maintenance Exit Inline    node-wrk1

Test Maintenance Enter And Exit Without Noout Or Stop
    [Documentation]    Enters maintenance (--set-noout=false --stop-osds=false) idempotently 3×,
    ...    then exits idempotently 3×, verifying OSD service and noout flag state each time.
    [Tags]    maintenance    cluster
    Test Maintenance Enter And Exit Inline    node-wrk1

Test Maintenance Enter And Exit With Noout And Stop
    [Documentation]    Enters maintenance (--set-noout=true --stop-osds=true) idempotently 3×,
    ...    then exits idempotently 3×, verifying OSD service stops and noout flag is set.
    [Tags]    maintenance    cluster
    Test Maintenance Enter Set Noout Stop OSDs And Exit Inline    node-wrk1

Test Quorum Guardrail Blocks Enter
    [Documentation]    Scales down to 3 nodes, disables one mon (leaving 2/3 active), then
    ...    verifies that entering maintenance is blocked to protect quorum.
    [Tags]    maintenance    cluster    mon
    Run In Container    node-wrk0    microceph disk remove osd.4 --bypass-safety-checks    300
    Wait For OSD Removed From Cluster    node-wrk0    4
    Remove Cluster Node With Retry    node-wrk0    node-wrk3
    Wait For Node Absent From Mons    node-wrk0    node-wrk3
    Stop And Disable Mon On Container    node-wrk2
    Wait For Health To Mention    node-wrk0    quorum
    Run In VM Must Fail    lxc exec node-wrk0 -- sh -c "microceph cluster maintenance enter node-wrk1"

Test Force Maintenance Enter And Exit Without Noout Or Stop
    [Documentation]    Enters maintenance with --force (--set-noout=false --stop-osds=false)
    ...    idempotently and exits, verifying the quorum check is bypassed.
    [Tags]    maintenance    cluster
    Test Maintenance Enter And Exit Force Inline    node-wrk1

Test Force Maintenance Enter And Exit With Noout And Stop
    [Documentation]    Enters maintenance with --force (--set-noout=true --stop-osds=true)
    ...    idempotently and exits, verifying OSD service and noout behaviour.
    [Tags]    maintenance    cluster
    Test Maintenance Enter Set Noout Stop OSDs And Exit Force Inline    node-wrk1
