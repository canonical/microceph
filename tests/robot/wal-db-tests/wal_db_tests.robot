*** Settings ***
Documentation    wal-db-tests
...    Tests MicroCeph WAL/DB device OSD configuration: adds loop-file OSDs,
...    then adds an OSD with dedicated WAL and DB devices; verifies disk list hides WAL/DB block devs;
...    tests encrypted WAL/DB startup (LUKS volume close and reopen).
Resource        ../resources/microceph_harness.resource
Suite Setup     WAL DB Tests Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    osd    wal-db    disk-management    lxd    integration

*** Keywords ***
WAL DB Tests Suite Setup
    Launch Outer Test VM    vm_name=microceph-wal-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install And Bootstrap MicroCeph

Test Encrypted WAL DB Startup Inline
    [Arguments]    ${expected_osds}
    [Documentation]    Adds an OSD with encrypted data/WAL/DB devices, simulates a reboot by
    ...    closing LUKS volumes, restarts the OSD service, and verifies all LUKS volumes reopen.
    Log To Console    [osd] Testing encrypted WAL/DB startup (expected_osds=${expected_osds})...
    Run In VM And Check    sudo snap connect microceph:dm-crypt    30
    Run In VM And Check    sudo snap restart microceph.daemon    60
    # Create 3 loop devices for data (sdid), WAL (sdie), DB (sdif)
    FOR    ${l}    IN    d    e    f
        Create Loop Device At    /dev/sdi${l}
    END
    Run In VM And Check    sudo microceph disk add /dev/sdid --encrypt --wal-device /dev/sdie --wal-encrypt --db-device /dev/sdif --db-encrypt    120
    # Wait for expected_osds up AND in (mirrors bash wait_for_osds_up_in)
    Wait For OSD Count Up In    ${expected_osds}
    # Find the OSD ID just created (highest numbered)
    ${osd_id_result}=    Run In VM    sudo ls -1 /var/snap/microceph/common/data/osd/ | sort -t- -k2 -n | tail -1 | sed 's/ceph-//'    30
    ${osd_id}=    Set Variable    ${osd_id_result.stdout.strip()}
    Log To Console    [osd] Simulating reboot for OSD ID ${osd_id}...
    # Simulate reboot: stop OSD and close LUKS volumes
    Run In VM And Check    sudo snap stop microceph.osd    30
    Run In VM And Check    sudo cryptsetup close "luksosd-${osd_id}" || true    30
    Run In VM And Check    sudo cryptsetup close "luksosd.wal-${osd_id}" || true    30
    Run In VM And Check    sudo cryptsetup close "luksosd.db-${osd_id}" || true    30
    Run In VM And Check    sudo snap start microceph.osd    30
    # Poll for all 3 LUKS volumes to reopen (up to 120s)
    FOR    ${vol}    IN    luksosd-${osd_id}    luksosd.wal-${osd_id}    luksosd.db-${osd_id}
        FOR    ${i}    IN RANGE    24
            ${exists}=    Run In VM    test -e /dev/mapper/${vol} && echo yes || echo no    15
            IF    "${exists.stdout.strip()}" == "yes"    BREAK
            IF    ${i} == 23    Fail    LUKS volume ${vol} not reopened after 120s
            Sleep    5s
        END
        Log To Console    [osd] LUKS volume ${vol} is open
    END
    # Wait for OSDs to come back up AND in — an OSD that is "in" but not "up" means the
    # LUKS volume did not reopen, which is exactly the failure this test must catch.
    Wait For OSD Count Up In    ${expected_osds}

*** Test Cases ***
Test Add Loop File OSDs
    [Documentation]    Adds 3 loopback-file OSDs as base storage.
    [Tags]    osd    loop-files
    Run In VM And Check    sudo microceph disk add loop,1G,3    120
    Wait For OSD Count    3
    Run In VM And Check    sudo microceph.ceph -s    30

Test Add WAL DB OSD
    [Documentation]    Creates loop devices /dev/sdia (data), /dev/sdib (WAL), /dev/sdic (DB)
    ...    and adds an OSD with dedicated WAL and DB devices.
    [Tags]    osd    wal-db
    Create Loop Devices
    Run In VM And Check    sudo microceph disk list    30
    Run In VM And Check    sudo microceph disk add /dev/sdia --wal-device /dev/sdib --db-device /dev/sdic    120
    Wait For OSD Count    4
    Run In VM And Check    sudo microceph.ceph -s    30

Test Disk List Hides WAL DB Block Devices
    [Documentation]    Verifies that microceph disk list does not expose the WAL/DB block devices.
    [Tags]    osd    wal-db
    Run In VM And Check    sudo microceph disk list    30
    Run In VM Must Fail    sudo microceph disk list | grep /dev/sdib
    Run In VM Must Fail    sudo microceph disk list | grep /dev/sdic

Test Encrypted WAL DB Startup
    [Documentation]    Adds an OSD with encrypted data/WAL/DB devices, simulates a reboot by
    ...    closing LUKS volumes, restarts the OSD service, and verifies all LUKS volumes reopen.
    [Tags]    osd    wal-db    encryption
    Test Encrypted WAL DB Startup Inline    5

Test Enable And Exercise RGW
    [Documentation]    Enables RGW and verifies S3 upload/download works with WAL/DB OSD present.
    [Tags]    rgw
    Enable RGW
    Exercise RGW
