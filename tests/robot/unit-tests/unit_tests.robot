*** Settings ***
Documentation    unit-tests
...    Runs Go unit tests on the host runner. Go and libdqlite are installed by the
...    CI YAML before Robot runs; no VM or snap needed.
Resource        ../resources/microceph_harness.resource
Suite Setup     Check Host Dependencies
Test Tags       unit    fast    smoke

*** Keywords ***
Check Host Dependencies
    Require Host Commands    go    make

*** Test Cases ***
Run Go Unit Tests
    [Documentation]    Runs make check-unit (go test ./...) in the microceph/ directory.
    [Tags]    unit    fast    smoke
    ${result}=    Run Process    bash -c "cd ${REPO_ROOT}/microceph && make check-unit"
    ...    shell=True    timeout=600
    Log    ${result.stdout}
    Log    STDERR: ${result.stderr}
    Should Be Equal As Integers    ${result.rc}    0    msg=make check-unit failed:\n${result.stderr}
