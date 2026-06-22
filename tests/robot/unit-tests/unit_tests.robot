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

Run Python Helper Unit Tests
    [Documentation]    Runs pytest over the pure Python harness helpers (parsers + _poll_until).
    ...    Host-only: no VM or snap needed. robotframework (imported by the harness module) and
    ...    pytest are installed in the tox venv that runs this suite.
    [Tags]    unit    fast    smoke    python
    ${result}=    Run Process    python3    -m    pytest    -q
    ...    ${REPO_ROOT}/tests/robot/resources/test_harness_helpers.py    timeout=300
    Log    ${result.stdout}
    Log    STDERR: ${result.stderr}
    Should Be Equal As Integers    ${result.rc}    0    msg=pytest failed:\n${result.stdout}\n${result.stderr}
