*** Settings ***
Documentation    loop-file-tests
...    Tests MicroCeph with loopback-file OSDs: add 4 via loop,1G,4, exercise RGW, remove one.
Resource        ../resources/microceph_harness.resource
Suite Setup     Loop File Tests Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    osd    rgw    loop-files    lxd    integration

*** Keywords ***
Loop File Tests Suite Setup
    Launch Outer Test VM    vm_name=microceph-lf-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install And Bootstrap MicroCeph

*** Test Cases ***
Test Add Loopback File OSDs
    [Documentation]    Adds 4 loopback-file OSDs using the loop,1G,4 shorthand and verifies count.
    [Tags]    osd    loop-files
    Run In VM And Check    sudo microceph disk add loop,1G,4    120
    Wait For OSD Count    4
    Run In VM And Check    sudo microceph.ceph -s    30

Test Enable And Exercise RGW
    [Documentation]    Enables RGW and verifies S3 upload/download works.
    [Tags]    rgw
    Enable RGW
    Exercise RGW

Test Remove Loopback OSD
    [Documentation]    Removes OSD osd.1 and verifies the count drops to 3.
    [Tags]    osd    loop-files
    Run In VM And Check    sudo microceph disk remove osd.1    120
    Wait For OSD Count    3
    Run In VM And Check    sudo microceph.ceph -s    30
