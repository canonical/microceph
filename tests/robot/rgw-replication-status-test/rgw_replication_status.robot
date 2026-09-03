*** Settings ***
Documentation    rgw-replication-status-test
...    Exercises the /1.0/ops/replication/rgw/site status API on a single node by
...    fabricating a two-zonegroup multisite topology with radosgw-admin: no second
...    cluster, no imported remotes, no running sync. This covers the API routing,
...    the FSM, PreFill's radosgw-admin reads and the brief rendering - everything
...    except an actual peer comparison, which needs a second cluster.
...
...    Every fabricated endpoint is a connection-refused address (127.0.0.1:9) on
...    purpose: sync status reads against them fail instantly and surface as
...    local-unavailable, whereas an unroutable address would block each read for
...    radosgw-admin's full 300s curl timeout.
Resource        ../resources/microceph_harness.resource
Suite Setup     RGW Replication Status Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    rgw    replication    api    lxd    integration

*** Variables ***
${DEAD_ENDPOINT}    http://127.0.0.1:9

*** Keywords ***
RGW Replication Status Suite Setup
    Launch Outer Test VM    vm_name=microceph-rgw-rep-vm
    Copy Scripts To VM
    Copy Snap To VM
    Free Runner Disk
    Install And Bootstrap MicroCeph
    Run In VM And Check    sudo microceph disk add loop,2G,3    300
    Create Fabricated Multisite Topology

Create Fabricated Multisite Topology
    [Documentation]    One realm, two zonegroups, one cluster. Zonegroup us (the
    ...    realm's master) holds the local zone us-east plus the fabricated peers
    ...    us-west (a configured sync source) and us-archive (excluded from
    ...    us-east's sync_from). Zonegroup eu (non-master) holds eu-central and
    ...    eu-west for the cross-zonegroup metadata master case.
    Run In VM And Check    sudo microceph.radosgw-admin realm create --rgw-realm=verify --default    60
    Run In VM And Check    sudo microceph.radosgw-admin zonegroup create --rgw-zonegroup=us --endpoints=${DEAD_ENDPOINT} --master --default    60
    Run In VM And Check    sudo microceph.radosgw-admin zone create --rgw-zonegroup=us --rgw-zone=us-east --endpoints=${DEAD_ENDPOINT} --master --default    60
    Run In VM And Check    sudo microceph.radosgw-admin zone create --rgw-zonegroup=us --rgw-zone=us-west --endpoints=${DEAD_ENDPOINT}    60
    Run In VM And Check    sudo microceph.radosgw-admin zone create --rgw-zonegroup=us --rgw-zone=us-archive --endpoints=${DEAD_ENDPOINT}    60
    Run In VM And Check    sudo microceph.radosgw-admin zone modify --rgw-zonegroup=us --rgw-zone=us-east --sync-from-all=false --sync-from=us-west    60
    Run In VM And Check    sudo microceph.radosgw-admin zonegroup create --rgw-zonegroup=eu --endpoints=${DEAD_ENDPOINT}    60
    Run In VM And Check    sudo microceph.radosgw-admin zone create --rgw-zonegroup=eu --rgw-zone=eu-central --endpoints=${DEAD_ENDPOINT} --master    60
    Run In VM And Check    sudo microceph.radosgw-admin zone create --rgw-zonegroup=eu --rgw-zone=eu-west --endpoints=${DEAD_ENDPOINT}    60
    Run In VM And Check    sudo microceph.radosgw-admin period update --commit    120

*** Test Cases ***
Site Status On The Metadata Master
    [Documentation]    us-east is the master zone of the realm's master zonegroup:
    ...    metadata sync reports master, the configured source us-west surfaces its
    ...    failed local sync read as local-unavailable, and us-archive - not named
    ...    in us-east's sync_from - is reported not-a-source without being queried.
    ${status}=    Get Rgw Replication Status
    Should Be Equal    ${status['realm']}    verify
    Should Be Equal    ${status['zonegroup']}    us
    Should Be Equal    ${status['zone']}    us-east
    Should Be True    ${status['is_master_zone']}
    Should Be Equal    ${status['master_zone']}    us-east
    Length Should Be    ${status['zones']}    ${3}
    Should Be Equal    ${status['metadata_sync']['state']}    master
    ${data_states}=    Rgw Data Sync States    ${status}
    Should Be Equal    ${data_states['us-west']}    local-unavailable
    Should Be Equal    ${data_states['us-archive']}    not-a-source

Site Status In A Non Master Zonegroup
    [Documentation]    With the cluster defaults switched to eu/eu-central - the
    ...    master of a NON-master zonegroup - is_master_zone must be false and the
    ...    metadata master must resolve across zonegroups to us-east via the realm
    ...    period. The never-started metadata sync and the eu-west data stream both
    ...    surface their failed local reads as local-unavailable.
    Run In VM And Check    sudo microceph.radosgw-admin zonegroup default --rgw-zonegroup=eu    60
    Run In VM And Check    sudo microceph.radosgw-admin zone default --rgw-zone=eu-central    60
    ${status}=    Get Rgw Replication Status
    Should Be Equal    ${status['zonegroup']}    eu
    Should Be Equal    ${status['zone']}    eu-central
    Should Not Be True    ${status['is_master_zone']}
    Should Be Equal    ${status['master_zone']}    us-east
    Should Be Equal    ${status['metadata_sync']['state']}    local-unavailable
    ${data_states}=    Rgw Data Sync States    ${status}
    Should Be Equal    ${data_states['eu-west']}    local-unavailable
