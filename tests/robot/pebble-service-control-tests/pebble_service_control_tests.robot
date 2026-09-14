*** Settings ***
Documentation    Temporary stop, start and restart of individual Pebble OSD services.
...    Uses three real loopback OSDs in an isolated VM and targets the middle OSD.
...    Checks child state, Ceph up/in membership, process groups and sibling isolation.
Resource        ../resources/microceph_harness.resource
Library         ../resources/pebble_services.py
Suite Setup     Pebble Service Control Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Setup      Record Running OSDs
Test Teardown   Restore Target OSD
Test Timeout    15 minutes
Test Tags       single-node    osd    pebble    service-control    lxd    integration

*** Variables ***
${TARGET_OSD}    1

*** Test Cases ***
Stop An Individual OSD
    [Documentation]    A named stop must leave the target down/in and both siblings untouched.
    Control Pebble OSD    stop    ${TARGET_OSD}
    ${stopped}=    Wait For Pebble OSD State    ${TARGET_OSD}    inactive
    Only Target OSD Should Be Stopped    ${BEFORE}    ${stopped}

Start An Individual OSD
    [Documentation]    Start a verified-stopped OSD without restarting the supervisor or siblings.
    Control Pebble OSD    stop    ${TARGET_OSD}
    ${stopped}=    Wait For Pebble OSD State    ${TARGET_OSD}    inactive
    Only Target OSD Should Be Stopped    ${BEFORE}    ${stopped}
    Control Pebble OSD    start    ${TARGET_OSD}
    ${started}=    Wait For Pebble OSD State    ${TARGET_OSD}    active
    Only Target OSD Should Have A New Process    ${BEFORE}    ${started}

Restart An Individual OSD
    [Documentation]    A named restart must replace only the target and retire its old group.
    Control Pebble OSD    restart    ${TARGET_OSD}
    ${restarted}=    Wait For Pebble OSD State    ${TARGET_OSD}    active
    Only Target OSD Should Have A New Process    ${BEFORE}    ${restarted}

*** Keywords ***
Pebble Service Control Suite Setup
    Launch Outer Test VM    vm_name=microceph-pebble-control-vm
    Copy Snap To VM
    Install And Bootstrap MicroCeph
    Run In VM And Check    sudo microceph disk add loop,1G,3    120
    Wait For OSD Count Up In    3

Record Running OSDs
    [Documentation]    Every case starts with all three OSDs running; no test-order dependency.
    Wait For OSD Count Up In    3
    ${snapshot}=    Get Pebble OSD Snapshot
    Snapshot Should Have Three OSDs    ${snapshot}
    OSD Should Be In State    ${snapshot}    0    active
    OSD Should Be In State    ${snapshot}    1    active
    OSD Should Be In State    ${snapshot}    2    active
    Set Test Variable    ${BEFORE}    ${snapshot}

Restore Target OSD
    [Documentation]    Always restore the target, including after a failed stop/start assertion.
    Control Pebble OSD    start    ${TARGET_OSD}
    Wait For Pebble OSD State    ${TARGET_OSD}    active
    Wait For OSD Count Up In    3

Snapshot Should Have Three OSDs
    [Arguments]    ${snapshot}
    Length Should Be    ${snapshot}[services]    3
    Length Should Be    ${snapshot}[osds]    3
    Should Be Equal    ${snapshot}[supervisor][state]    active
    Should Be True    ${snapshot}[supervisor][pid] > 1
    Should Be True    ${snapshot}[supervisor][started] > 0

OSD Should Be In State
    [Arguments]    ${snapshot}    ${osd_id}    ${state}
    ${matches}=    Pebble OSD Is In State    ${snapshot}    ${osd_id}    ${state}
    Should Be True    ${matches}
    ...    msg=osd-${osd_id} is not ${state} in Pebble, Ceph and the live process table

Sibling OSD Should Be Unchanged
    [Arguments]    ${before}    ${after}    ${osd_id}
    Dictionaries Should Be Equal    ${before}[identities][${osd_id}]    ${after}[identities][${osd_id}]
    OSD Should Be In State    ${after}    ${osd_id}    active

Only Target OSD Should Be Affected
    [Arguments]    ${before}    ${after}
    Snapshot Should Have Three OSDs    ${after}
    Dictionaries Should Be Equal    ${before}[supervisor]    ${after}[supervisor]
    Sibling OSD Should Be Unchanged    ${before}    ${after}    0
    Sibling OSD Should Be Unchanged    ${before}    ${after}    2

Only Target OSD Should Be Stopped
    [Arguments]    ${before}    ${after}
    Only Target OSD Should Be Affected    ${before}    ${after}
    OSD Should Be In State    ${after}    ${TARGET_OSD}    inactive
    Dictionaries Should Be Equal
    ...    ${before}[identities][${TARGET_OSD}]    ${after}[identities][${TARGET_OSD}]
    Dictionary Should Not Contain Key    ${after}[groups]    ${before}[identities][${TARGET_OSD}][pid]

Only Target OSD Should Have A New Process
    [Arguments]    ${before}    ${after}
    Only Target OSD Should Be Affected    ${before}    ${after}
    OSD Should Be In State    ${after}    ${TARGET_OSD}    active
    Should Not Be Equal    ${before}[identities][${TARGET_OSD}]    ${after}[identities][${TARGET_OSD}]
    Dictionary Should Not Contain Key    ${after}[groups]    ${before}[identities][${TARGET_OSD}][pid]
