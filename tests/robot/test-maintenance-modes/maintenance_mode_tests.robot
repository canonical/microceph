*** Settings ***
Documentation    Translated from: .github/workflows/tests.yml — test-maintenance-modes
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
    ${elapsed}=    Set Variable    0
    WHILE    int('${elapsed}') < int('${timeout}')
        ${state}=    Is OSD Noout Set On    ${node}
        IF    "${expected}" == "set" and "${state}" == "yes"    RETURN
        IF    "${expected}" == "unset" and "${state}" == "no"    RETURN
        Sleep    5s
        ${elapsed}=    Evaluate    int('${elapsed}') + 5
    END
    Fail    Timed out waiting for osd noout to be '${expected}' on ${node}

Wait For Snap Service State On
    [Documentation]    Polls until microceph.<service> reaches expected active/enabled state on node.
    [Arguments]    ${node}    ${service}    ${expected_active}    ${expected_enabled}    ${timeout}=120
    ${elapsed}=    Set Variable    0
    WHILE    int('${elapsed}') < int('${timeout}')
        ${state}=    Check Snap Service State On    ${node}    ${service}    ${expected_active}    ${expected_enabled}
        IF    "${state}" == "yes"    RETURN
        Sleep    5s
        ${elapsed}=    Evaluate    int('${elapsed}') + 5
    END
    Fail    Timed out waiting for microceph.${service} on ${node} to be ${expected_active}/${expected_enabled}

Assert Output Contains Pattern
    [Documentation]    Fails if pattern is not found in output string.
    [Arguments]    ${output}    ${pattern}    ${context}
    ${match}=    Run Process    bash -c "echo ${output} | grep -E '${pattern}'"    shell=True
    Should Be Equal As Integers    ${match.rc}    0    msg=${context}: pattern '${pattern}' not found

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

Test Maintenance Enter And Exit Inline
    [Documentation]    Enters/exits maintenance (--set-noout=false --stop-osds=false) 3× each.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing maintenance enter/exit (no-noout no-stop-osds) on ${node}...
    Run In Container    ${node}    microceph status    30
    FOR    ${i}    IN    1    2    3
        Log To Console    [maintenance] Enter count ${i}...
        Run In Container    ${node}    microceph cluster maintenance enter --set-noout=false --stop-osds=false ${node}    120
        Wait For Noout State On    ${node}    unset    120
        Wait For Snap Service State On    ${node}    osd    active    enabled    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    no    msg=noout should be unset
        ${svc}=    Check Snap Service State On    ${node}    osd    active    enabled
        Should Be Equal As Strings    ${svc}    yes    msg=OSD service should be active/enabled
    END
    FOR    ${i}    IN    1    2    3
        Log To Console    [maintenance] Exit count ${i}...
        Run In Container    ${node}    microceph cluster maintenance exit ${node}    120
        Wait For Noout State On    ${node}    unset    120
        Wait For Snap Service State On    ${node}    osd    active    enabled    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    no    msg=noout should be unset after exit
        ${svc}=    Check Snap Service State On    ${node}    osd    active    enabled
        Should Be Equal As Strings    ${svc}    yes    msg=OSD service should be active/enabled after exit
    END
    Log To Console    [maintenance] PASSED: maintenance enter/exit (no-noout no-stop-osds)

Test Maintenance Enter Set Noout Stop OSDs And Exit Inline
    [Documentation]    Enters/exits maintenance (--set-noout=true --stop-osds=true) 3× each.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing maintenance enter/exit (set-noout stop-osds) on ${node}...
    Run In Container    ${node}    microceph status    30
    FOR    ${i}    IN    1    2    3
        Log To Console    [maintenance] Enter count ${i}...
        Run In Container    ${node}    microceph cluster maintenance enter --set-noout=true --stop-osds=true ${node}    120
        Wait For Noout State On    ${node}    set    120
        Wait For Snap Service State On    ${node}    osd    inactive    disabled    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    yes    msg=noout should be set
        ${svc}=    Check Snap Service State On    ${node}    osd    active    enabled
        Should Be Equal As Strings    ${svc}    no    msg=OSD service should NOT be active/enabled
    END
    FOR    ${i}    IN    1    2    3
        Log To Console    [maintenance] Exit count ${i}...
        Run In Container    ${node}    microceph cluster maintenance exit ${node}    120
        Wait For Noout State On    ${node}    unset    120
        Wait For Snap Service State On    ${node}    osd    active    enabled    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    no    msg=noout should be unset after exit
        ${svc}=    Check Snap Service State On    ${node}    osd    active    enabled
        Should Be Equal As Strings    ${svc}    yes    msg=OSD service should be active/enabled after exit
    END
    Log To Console    [maintenance] PASSED: maintenance enter/exit (set-noout stop-osds)

Test Maintenance Enter And Exit Force Inline
    [Documentation]    Enters/exits maintenance with --force (--set-noout=false --stop-osds=false) 3× each.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing --force maintenance enter/exit (no-noout no-stop-osds) on ${node}...
    Run In Container    ${node}    microceph status    30
    FOR    ${i}    IN    1    2    3
        Log To Console    [maintenance] Force Enter count ${i}...
        Run In Container    ${node}    microceph cluster maintenance enter --set-noout=false --stop-osds=false --force ${node}    120
        Wait For Noout State On    ${node}    unset    120
        Wait For Snap Service State On    ${node}    osd    active    enabled    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    no    msg=noout should be unset
        ${svc}=    Check Snap Service State On    ${node}    osd    active    enabled
        Should Be Equal As Strings    ${svc}    yes    msg=OSD service should be active/enabled
    END
    FOR    ${i}    IN    1    2    3
        Log To Console    [maintenance] Force Exit count ${i}...
        Run In Container    ${node}    microceph cluster maintenance exit ${node}    120
        Wait For Noout State On    ${node}    unset    120
        Wait For Snap Service State On    ${node}    osd    active    enabled    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    no    msg=noout should be unset after exit
        ${svc}=    Check Snap Service State On    ${node}    osd    active    enabled
        Should Be Equal As Strings    ${svc}    yes    msg=OSD service should be active/enabled after exit
    END
    Log To Console    [maintenance] PASSED: --force maintenance enter/exit (no-noout no-stop-osds)

Test Maintenance Enter Set Noout Stop OSDs And Exit Force Inline
    [Documentation]    Enters/exits maintenance with --force (--set-noout=true --stop-osds=true) 3× each.
    [Arguments]    ${node}
    Log To Console    [maintenance] Testing --force maintenance enter/exit (set-noout stop-osds) on ${node}...
    Run In Container    ${node}    microceph status    30
    FOR    ${i}    IN    1    2    3
        Log To Console    [maintenance] Force Enter count ${i}...
        Run In Container    ${node}    microceph cluster maintenance enter --set-noout=true --stop-osds=true --force ${node}    120
        Wait For Noout State On    ${node}    set    120
        Wait For Snap Service State On    ${node}    osd    inactive    disabled    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    yes    msg=noout should be set
        ${svc}=    Check Snap Service State On    ${node}    osd    active    enabled
        Should Be Equal As Strings    ${svc}    no    msg=OSD service should NOT be active/enabled
    END
    FOR    ${i}    IN    1    2    3
        Log To Console    [maintenance] Force Exit count ${i}...
        Run In Container    ${node}    microceph cluster maintenance exit ${node}    120
        Wait For Noout State On    ${node}    unset    120
        Wait For Snap Service State On    ${node}    osd    active    enabled    120
        Run In Container    ${node}    microceph.ceph -s    30
        ${noout}=    Is OSD Noout Set On    ${node}
        Should Be Equal As Strings    ${noout}    no    msg=noout should be unset after exit
        ${svc}=    Check Snap Service State On    ${node}    osd    active    enabled
        Should Be Equal As Strings    ${svc}    yes    msg=OSD service should be active/enabled after exit
    END
    Log To Console    [maintenance] PASSED: --force maintenance enter/exit (set-noout stop-osds)

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
    FOR    ${i}    IN RANGE    8
        ${gone}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph osd info osd.4 2>/dev/null && echo exists || echo gone"    30
        IF    "${gone.stdout.strip()}" == "gone"
            Log To Console    [maintenance] osd.4 confirmed removed
            BREAK
        END
        Sleep    5s
    END
    FOR    ${attempt}    IN RANGE    3
        ${result}=    Run In VM    lxc exec node-wrk0 -- bash -eo pipefail -c "microceph cluster remove node-wrk3"    120
        IF    ${result.rc} == 0    BREAK
        Log To Console    [maintenance] Remove node-wrk3 attempt ${attempt} failed: ${result.stderr.strip()} — retrying in 10s
        IF    ${attempt} == 2    Fail    Failed to remove node-wrk3 after 3 attempts: ${result.stderr}
        Sleep    10s
    END
    FOR    ${i}    IN RANGE    8
        ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph -s | grep -q 'mon: .*daemons.*node-wrk3' && echo yes || echo no"    30
        IF    "${result.stdout.strip()}" == "no"
            Log To Console    [maintenance] node-wrk3 no longer in mon list
            BREAK
        END
        Sleep    5s
    END
    Run In VM And Check    lxc exec node-wrk2 -- sh -c "sudo systemctl stop snap.microceph.mon && sudo systemctl disable snap.microceph.mon"    30
    Log To Console    [maintenance] Waiting for ceph to detect quorum warning (up to 100s)...
    FOR    ${i}    IN RANGE    100
        ${health}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph health 2>/dev/null || true"    15
        IF    "quorum" in $health.stdout
            Log To Console    [maintenance] Quorum warning detected after ${i}s
            BREAK
        END
        Sleep    1s
    END
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
