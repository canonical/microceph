*** Settings ***
Documentation    rgw-replication-test
...    Tests the RGW replication status API against a real multisite: two 2-node
...    sites (sitea=wrk0/1, siteb=wrk2/3) with remotes exchanged, the realm
...    configured manually with radosgw-admin (MicroCeph cannot enable RGW
...    multisite itself yet), rgw running on both sides and sync live. This is
...    the coverage the single-node rgw-replication-status-test cannot reach:
...    peer log reads through imported remotes and real caught-up verdicts.
...
...    The tests are an ordered ladder: multisite runs BEFORE any remotes are
...    imported, so the same live streams first render peer-unavailable (local
...    markers readable, peer log unreachable), then flip to caught-up once the
...    remotes land.
Resource        ../resources/microceph_harness.resource
Resource        ../resources/replication.resource
Suite Setup     RGW Replication Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    rgw    replication    remote    lxd    slow    integration

*** Variables ***
${REALM}          microceph
${ZONEGROUP}      microceph
# Fixed system-user keys for inter-zone sync auth; test-only credentials.
${SYNC_ACCESS}    rgwreptestaccesskey1
${SYNC_SECRET}    rgwreptestsecretkey1

*** Keywords ***
RGW Replication Suite Setup
    Provision Multinode VM    microceph-rgwrep-vm    ${OUTER_VM_DISK}    public
    Bootstrap Two Sites
    Configure Rgw Multisite Manually

Configure Rgw Multisite Manually
    [Documentation]    The standard squid manual multisite procedure with sitea as
    ...    the master zone: realm/zonegroup/zone plus the system user on sitea, then
    ...    realm pull and the secondary zone on siteb. Multisite is configured before
    ...    each site's rgw first starts, so no implicit "default" zone is ever
    ...    created. Zone names deliberately match the imported remote names - that
    ...    pairing is how the status API reaches a peer's sync logs.
    Log To Console    [rgw] Configuring multisite manually (sitea master, siteb secondary)...
    ${sitea_ip}=    Get Node Ip    node-wrk0
    ${siteb_ip}=    Get Node Ip    node-wrk2
    Run In Container    node-wrk0    microceph.radosgw-admin realm create --rgw-realm=${REALM} --default    60
    Run In Container    node-wrk0    microceph.radosgw-admin zonegroup create --rgw-zonegroup=${ZONEGROUP} --endpoints=http://${sitea_ip}:80 --rgw-realm=${REALM} --master --default    60
    Run In Container    node-wrk0    microceph.radosgw-admin zone create --rgw-zonegroup=${ZONEGROUP} --rgw-zone=sitea --endpoints=http://${sitea_ip}:80 --access-key=${SYNC_ACCESS} --secret=${SYNC_SECRET} --master --default    60
    Run In Container    node-wrk0    microceph.radosgw-admin user create --uid=sync --display-name=sync --access-key=${SYNC_ACCESS} --secret=${SYNC_SECRET} --system    60
    Run In Container    node-wrk0    microceph.radosgw-admin period update --commit    120
    Run In Container    node-wrk0    microceph enable rgw    120
    Wait For Rgw Endpoint    node-wrk2    http://${sitea_ip}:80
    Run In Container    node-wrk2    microceph.radosgw-admin realm pull --url=http://${sitea_ip}:80 --access-key=${SYNC_ACCESS} --secret=${SYNC_SECRET} --default    120
    Run In Container    node-wrk2    microceph.radosgw-admin zone create --rgw-zonegroup=${ZONEGROUP} --rgw-zone=siteb --endpoints=http://${siteb_ip}:80 --access-key=${SYNC_ACCESS} --secret=${SYNC_SECRET} --default    60
    Run In Container    node-wrk2    microceph.radosgw-admin period update --commit    120
    Run In Container    node-wrk2    microceph enable rgw    120
    Wait For Rgw Endpoint    node-wrk0    http://${siteb_ip}:80

*** Test Cases ***
Status Without Imported Remotes
    [Documentation]    Multisite is live but no remotes are imported yet, so the
    ...    local sync markers are readable while every peer log is not: each
    ...    stream must render peer-unavailable with an empty remote - not
    ...    caught-up, behind, or local-unavailable.
    Wait For Rgw Data Sync State    node-wrk0    siteb    peer-unavailable
    ${status}=    Get Rgw Replication Status In Container    node-wrk0
    Should Be Equal    ${status['metadata_sync']['state']}    master
    ${data_states}=    Rgw Data Sync States    ${status}
    Should Be Equal    ${data_states['siteb']}    peer-unavailable
    Wait For Rgw Metadata Sync State    node-wrk2    peer-unavailable
    ${status}=    Get Rgw Replication Status In Container    node-wrk2
    Should Be Equal    ${status['metadata_sync']['remote']}    ${EMPTY}
    ${data_states}=    Rgw Data Sync States    ${status}
    Should Be Equal    ${data_states['sitea']}    peer-unavailable

Importing Remotes Enables Peer Comparisons
    [Documentation]    Exchanges tokens so each site can read the other's sync
    ...    logs; the following tests assert the same streams now compare for real.
    Exchange Remote Site Tokens
    Verify Remote Authentication On All Nodes

Master Site Status
    [Documentation]    sitea is the metadata master: it syncs metadata from no one
    ...    and pulls data from siteb through the imported remote. caught-up here is
    ...    a real verdict - local markers compared against siteb's live datalog.
    Wait For Rgw Data Sync State    node-wrk0    siteb    caught-up
    ${status}=    Get Rgw Replication Status In Container    node-wrk0
    Should Be Equal    ${status['realm']}    ${REALM}
    Should Be Equal    ${status['zonegroup']}    ${ZONEGROUP}
    Should Be Equal    ${status['zone']}    sitea
    Should Be True    ${status['is_master_zone']}
    Should Be Equal    ${status['master_zone']}    sitea
    Length Should Be    ${status['zones']}    ${2}
    Should Be Equal    ${status['metadata_sync']['state']}    master
    ${data_states}=    Rgw Data Sync States    ${status}
    Should Be Equal    ${data_states['siteb']}    caught-up

Secondary Site Status
    [Documentation]    siteb syncs metadata from sitea and data from sitea, both
    ...    through the imported sitea remote, and must name sitea as the master.
    Wait For Rgw Metadata Sync State    node-wrk2    caught-up
    Wait For Rgw Data Sync State    node-wrk2    sitea    caught-up
    ${status}=    Get Rgw Replication Status In Container    node-wrk2
    Should Be Equal    ${status['zone']}    siteb
    Should Not Be True    ${status['is_master_zone']}
    Should Be Equal    ${status['master_zone']}    sitea
    Should Be Equal    ${status['metadata_sync']['state']}    caught-up
    Should Be Equal    ${status['metadata_sync']['remote']}    sitea
    ${data_states}=    Rgw Data Sync States    ${status}
    Should Be Equal    ${data_states['sitea']}    caught-up

Metadata Change Replicates And Status Recovers
    [Documentation]    A user created on the master must appear on the secondary via
    ...    live metadata sync, and the status must return to caught-up afterwards -
    ...    proving the verdict tracks a real stream rather than a vacuous one.
    Run In Container    node-wrk0    microceph.radosgw-admin user create --uid=repl-check --display-name=repl-check    60
    Wait For Rgw User In Container    node-wrk2    repl-check
    Wait For Rgw Metadata Sync State    node-wrk2    caught-up
