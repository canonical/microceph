*** Settings ***
Documentation    Simple test to verify robot framework works
Library         OperatingSystem
Library         Process

*** Variables ***
${TEST_VM}    test-simple-vm

*** Test Cases ***
Test LXD List Command
    [Documentation]    Test that LXD is working
    ${result}=    Run Process    lxc    list    --format    csv    shell=false
    Log    LXC list output: ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=LXC list failed: ${result.stderr}

Test File Exists Check
    [Documentation]    Test file operations work
    File Should Exist    /etc/passwd
    Log    /etc/passwd exists