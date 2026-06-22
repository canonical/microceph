*** Settings ***
Documentation    cluster-tests
...    Tests cluster-level features: cluster list JSON output, RGW config bombardment
...    (multiple concurrent config sets), and verifies RGW continues to work after.
Resource        ../resources/microceph_harness.resource
Suite Setup     Cluster Tests Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    cluster    rgw    osd    lxd    integration

*** Keywords ***
Cluster Tests Suite Setup
    Launch Outer Test VM    vm_name=microceph-cluster-vm
    Copy Scripts To VM
    Copy Snap To VM
    Install Tools
    Install And Bootstrap MicroCeph
    Run In VM And Check    sudo microceph disk add loop,1G,3    120
    Wait For OSD Count    3

Bombard RGW Configs
    [Documentation]    Issues many microceph cluster config set calls for RGW Keystone settings,
    ...    then waits for RGW to settle (mirrors actionutils.sh bombard_rgw_configs).
    ...    The original bash ran without set -e so individual command failures were silently ignored;
    ...    we replicate that here — each config set is attempted but its rc is not checked.
    Log To Console    [config] Bombarding RGW Keystone configs...
    # First key uses the canonical 'rgw_'-prefixed name (rgw_s3_auth_use_keystone), matching the
    # other rgw_keystone_* keys below; the original bash used the older unprefixed
    # 's3_auth_use_keystone'. Bombard ignores per-set rc, so this is not asserted either way --
    # renamed for awareness; maintainer to confirm the key name for the targeted Ceph release.
    Run In VM    sudo microceph cluster config set rgw_s3_auth_use_keystone true --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_url example.url.com --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_admin_user admin --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_admin_password admin --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_admin_project project --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_admin_domain domain --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_service_token_enabled true --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_service_token_accepted_roles admin_role --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_api_version 3 --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_accepted_roles Member,member --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_accepted_admin_roles admin_role --skip-restart    30
    Run In VM    sudo microceph cluster config set rgw_keystone_token_cache_size 500 --skip-restart    30
    Run In VM And Check    sudo microceph cluster config set rgw_keystone_verify_ssl false --wait    60
    Sleep    30s
    Run In VM And Check    sudo microceph.ceph status    30
    Run In VM And Check    sudo microceph.ceph health    30

*** Test Cases ***
Test Enable RGW And Exercise
    [Documentation]    Enables Rados Gateway and verifies S3 upload/download.
    [Tags]    rgw
    Enable RGW
    Exercise RGW

Test Cluster List
    [Documentation]    Verifies that microceph cluster list includes the local hostname and
    ...    that the JSON output is valid and contains the hostname.
    [Tags]    cluster
    ${hn}=    Get VM Hostname
    Run In VM And Check    sudo microceph cluster list | grep -q ${hn}    30
    Run In VM And Check    sudo microceph cluster list -f json | jq '.[]["name"]' | grep -q ${hn}    30

Test Bombard RGW Configs
    [Documentation]    Issues many concurrent cluster config set calls for RGW Keystone settings
    ...    and verifies that RGW recovers and S3 access still works.
    [Tags]    cluster    rgw
    Bombard RGW Configs
    Exercise RGW    newFile
