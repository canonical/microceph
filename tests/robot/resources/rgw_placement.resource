*** Settings ***
Documentation    Shared RGW placement test actions and assertions.
...    Test case bodies and suite `*** Keywords ***` sections call these;
...    every JSON/body decision is made in Python (rgw_placement.py and
...    placement_status.py, via the library keywords rgw_placement.py
...    exposes) -- never by raw-substring or JSON-key-order matching here.
Library          rgw_placement.py

*** Keywords ***

# ---------------------------------------------------------------------------
# S3 / HTTP content round-trips
# ---------------------------------------------------------------------------

RGW Endpoint Serves In VM
    [Documentation]    Fetches *path* from host:port -- HTTPS with *ca_cert* verification
    ...    when given, otherwise plain HTTP -- and asserts the request succeeded (rc 0)
    ...    and the body is EXACTLY *expected*. Wait For RGW Fetch In VM applies a short
    ...    bounded retry so a listener that has not quite caught up with a preceding PUT
    ...    or upload does not flake the assertion; the equality checks themselves stay
    ...    in Robot.
    [Arguments]    ${host}    ${port}    ${path}    ${expected}    ${ca_cert}=${EMPTY}    ${vm_name}=${None}
    ${result}=    Wait For RGW Fetch In VM    ${host}    ${port}    ${path}    ca_cert=${ca_cert}    vm_name=${vm_name}
    Should Be Equal As Integers    ${result.rc}    0
    ...    msg=fetch of ${path} from ${host}:${port} failed (rc=${result.rc}): ${result.stderr}
    Should Be Equal    ${result.stdout}    ${expected}    msg=unexpected body from ${host}:${port}${path}

RGW Endpoint Closed In VM
    [Documentation]    Asserts *host*:*port* refuses TCP connections. Wait For RGW Endpoint
    ...    Closed In VM polls a short bounded number of times because a listener can take
    ...    a moment to unbind after a policy apply stops or moves it.
    [Arguments]    ${host}    ${port}    ${vm_name}=${None}
    ${reachable}=    Wait For RGW Endpoint Closed In VM    ${host}    ${port}    vm_name=${vm_name}
    Should Be Equal    ${reachable}    ${False}    msg=RGW endpoint ${host}:${port} is still reachable

Exercise RGW S3 In VM
    [Documentation]    Uploads <filename>.txt (content hello-rgw-placement-<filename>) via
    ...    s3cmd into testbucket (plaintext) or testbucketssl (ssl=True), then asserts a
    ...    trusted fetch of that same object returns exactly that content -- proving a
    ...    real S3 content round-trip, not just an open TCP port.
    [Arguments]    ${host}    ${port}    ${ssl}=${False}    ${ca_cert}=${EMPTY}    ${filename}=test    ${vm_name}=${None}
    ${bucket}=    Set Variable If    ${ssl}    testbucketssl    testbucket
    ${content}=    Set Variable    hello-rgw-placement-${filename}
    Upload RGW Object In VM    ${host}    ${port}    ${bucket}    ${filename}.txt    ${content}
    ...    ssl=${ssl}    ca_cert=${ca_cert}    vm_name=${vm_name}
    RGW Endpoint Serves In VM    ${host}    ${port}    /${bucket}/${filename}.txt    ${content}
    ...    ca_cert=${ca_cert}    vm_name=${vm_name}

# ---------------------------------------------------------------------------
# Secret-leak / cleanup assertions
# ---------------------------------------------------------------------------

Assert RGW Material Absent In VM
    [Documentation]    Scans the guest for the disposable pair generated at /tmp/<prefix>
    ...    (GET /placement, on-disk files outside the exact pair radosgw.conf currently
    ...    references, and the daemon journal) and asserts no leak location was found.
    ...    Only leak LOCATIONS are returned by the probe; the matching material itself
    ...    never leaves the guest.
    [Arguments]    ${prefix}    ${vm_name}=${None}
    ${leaks}=    Find RGW Material Leaks In VM    ${prefix}    vm_name=${vm_name}
    Should Be Empty    ${leaks}    msg=RGW material for /tmp/${prefix} leaked at: ${leaks}

Assert RGW TLS Material Is Mode 600 In VM
    [Documentation]    Asserts every file radosgw.conf currently references for TLS (the
    ...    exact certificate/key pair it names, not every file that happens to be called
    ...    server.key) has mode 0600.
    [Arguments]    ${vm_name}=${None}
    ${modes}=    RGW TLS File Modes In VM    vm_name=${vm_name}
    Should Not Be Empty    ${modes}    msg=no referenced RGW TLS files to check modes on
    FOR    ${mode}    IN    @{modes}
        Should Be Equal    ${mode}    600    msg=RGW TLS material file mode is not 0600 (saw ${modes})
    END

Assert No RGW TLS Material Remains In VM
    [Documentation]    Asserts no generated (rgw-tls/*) or legacy (server.crt/server.key)
    ...    RGW TLS material remains on disk.
    [Arguments]    ${vm_name}=${None}
    ${paths}=    RGW TLS Material Paths In VM    vm_name=${vm_name}
    Should Be Empty    ${paths}    msg=leftover RGW TLS material found: ${paths}
