*** Settings ***
Documentation    rgw-datalog-crash-tests
...    Regression coverage for issue #810: a signed request to RGW's datalog
...    admin endpoints segfaults radosgw when Ceph is built against a Boost
...    whose asio spawn handler rethrows a completion's std::exception_ptr
...    without checking it holds an exception. rgw::run_coro's stackful-yield
...    branch is the only co_spawn(..., yield_context) user in the Ceph tree
...    and the datalog admin ops are its only callers, so every
...    GET /admin/log?type=data&id=N kills the gateway on an affected build.
...    One node, one zone: multisite is how the bug surfaced in the field
...    (data sync polls these endpoints every cycle) but the datalog is
...    started for every non-raw RGW driver, so no peer zone is needed to
...    trigger it.
Resource        ../resources/microceph_harness.resource
Suite Setup     RGW Datalog Crash Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    rgw    regression    issue-810    lxd    integration

*** Variables ***
${RGW_ADMIN_UID}      datalogprobe
${RGW_LOG}            /var/snap/microceph/common/logs/ceph-client.radosgw.gateway.log

*** Keywords ***
RGW Datalog Crash Suite Setup
    Launch Outer Test VM    vm_name=microceph-rgw-datalog-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install And Bootstrap MicroCeph
    Run In VM And Check    sudo microceph disk add loop,1G,3    120
    Wait For OSD Count    3
    Enable RGW
    # Fail here, not mid-assertion, if the gateway's log is not where the crash
    # checks will look for it.
    Run In VM And Check    test -f ${RGW_LOG}    30
    ${access_key}    ${secret_key}=    Create RGW System User    ${RGW_ADMIN_UID}    ${RGW_ADMIN_UID}
    Set Suite Variable    ${RGW_ACCESS_KEY}    ${access_key}
    Set Suite Variable    ${RGW_SECRET_KEY}    ${secret_key}

RGW Must Survive Datalog Query
    [Documentation]    Sends one signed GET to /admin/log?<query> and fails if
    ...    radosgw died servicing it. Returns the response body so the caller
    ...    can check the handler actually answered.
    ...
    ...    Two independent signals, both required. The primary is the rgw log:
    ...    the dying process writes its own fatal-signal banner and backtrace,
    ...    and only the log names the crashing frames, so it cannot be confused
    ...    with an OOM kill or a deliberate restart. Only the log written since
    ...    this request is examined. The corroborating signal is systemd's
    ...    restart counter, which owes nothing to log wording and so still
    ...    fires if a future Ceph reword breaks the pattern.
    ...
    ...    The HTTP status is checked last and is not a crash signal -- curl
    ...    reports the same "empty reply" for a dropped connection as for a
    ...    dead server -- but asserting it here is what stops the test passing
    ...    when the request never reached the handler at all.
    [Arguments]    ${query}
    Wait For RGW    1
    ${restarts_before}=    Get Snap Service Restart Count    rgw
    ${log_size_before}=    Get VM File Size    ${RGW_LOG}
    ${result}=    Send Signed RGW Admin Request    ${RGW_ACCESS_KEY}    ${RGW_SECRET_KEY}    ${query}
    # Let the dying process finish flushing its backtrace and systemd notice the
    # exit before either signal is read.
    Sleep    3s
    ${new_log}=    Read VM File From Offset    ${RGW_LOG}    ${log_size_before}
    ${crash}=    Datalog Crash Signature    ${new_log}
    Should Be Empty    ${crash}
    ...    msg=radosgw segfaulted servicing /admin/log?${query} (issue #810):\n${crash}
    ${restarts_after}=    Get Snap Service Restart Count    rgw
    Should Be Equal As Integers    ${restarts_before}    ${restarts_after}
    ...    msg=snap.microceph.rgw restarted (NRestarts ${restarts_before} -> ${restarts_after}) while servicing /admin/log?${query}
    ${body}    ${status}=    Curl Body And Status    ${result.stdout}
    Should Be Equal As Strings    ${status}    200
    ...    msg=/admin/log?${query} answered HTTP ${status}, not 200 (curl rc ${result.rc})
    RETURN    ${body}

*** Test Cases ***
Test Datalog Info Query Succeeds
    [Documentation]    Control case. GET /admin/log?type=data dispatches to
    ...    RGWOp_DATALog_Info, which reads the shard count straight from config
    ...    and never enters a coroutine, so it answers even on an affected
    ...    build. A failure here means the cluster or the request signing is
    ...    broken, not that issue #810 is present -- fix that before reading
    ...    anything into the other two cases.
    ${body}=    RGW Must Survive Datalog Query    type=data
    ${num_objects}=    Datalog Num Objects    ${body}
    Should Be True    ${num_objects} > 0
    ...    msg=datalog reports ${num_objects} shards, so there is no shard 0 for the other cases to query

Test Datalog Shard Info Query Does Not Crash RGW
    [Documentation]    The request from issue #810. Having both id and info
    ...    dispatches to RGWOp_DATALog_ShardInfo, which fetches the shard's
    ...    marker through rgw::run_coro's stackful-yield branch -- the
    ...    co_spawn(..., yield) call a mismatched Boost turns into a null
    ...    exception_ptr rethrow on the success path.
    ${body}=    RGW Must Survive Datalog Query    type=data&id=0&info
    ${marker}=    Datalog Shard Info Marker    ${body}
    Log    datalog shard 0 marker: '${marker}'

Test Datalog List Query Does Not Crash RGW
    [Documentation]    The sibling endpoint. id without info dispatches to
    ...    RGWOp_DATALog_List, which drives the same run_coro branch twice
    ...    (list_entries then get_info) and is equally exposed. Multisite data
    ...    sync polls this alongside shard-info, so leaving it uncovered would
    ...    let half the reported failure back in.
    ${body}=    RGW Must Survive Datalog Query    type=data&id=0
    ${entries}=    Datalog List Entry Count    ${body}
    Log    datalog shard 0 returned ${entries} entries
