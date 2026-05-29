*** Settings ***
Documentation    Translated from: .github/workflows/tests.yml — static-checks
...    Runs Go static analysis on the host runner. Go, libdqlite, and shellcheck
...    are installed by the CI YAML before Robot runs; no VM or snap needed.
Resource        ../resources/microceph_harness.resource
Suite Setup     Check Host Dependencies
Test Tags       static    smoke    fast

*** Keywords ***
Check Host Dependencies
    Require Host Commands    go    make    shellcheck

*** Test Cases ***
Test GolangCI Lint
    [Documentation]    Runs make check-static (golangci-lint, auto-installed by the Makefile if absent).
    [Tags]    golangci-lint
    ${gopath}=    Run Process    go env GOPATH    shell=True
    ${gopath_bin}=    Set Variable    ${gopath.stdout.strip()}/bin
    ${result}=    Run Process    bash -c "cd ${REPO_ROOT}/microceph && make check-static"
    ...    shell=True    timeout=600    env:PATH=${gopath_bin}:%{PATH}
    Log    ${result.stdout}
    Log    STDERR: ${result.stderr}
    Should Be Equal As Integers    ${result.rc}    0    msg=make check-static failed:\n${result.stderr}

Test Go Vet
    [Documentation]    Runs go vet on the microceph package directly.
    [Tags]    go-vet
    ${result}=    Run Process    bash -c "cd ${REPO_ROOT}/microceph && go vet ./..."
    ...    shell=True    timeout=120
    Log    ${result.stdout}
    Log    STDERR: ${result.stderr}
    Should Be Equal As Integers    ${result.rc}    0    msg=go vet failed:\n${result.stderr}
