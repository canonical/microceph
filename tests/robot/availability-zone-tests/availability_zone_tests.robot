*** Settings ***
Documentation    availability-zone-tests
...    Tests CRUSH rack-level failure domain with availability zones: rule transitions,
...    and full AZ lifecycle (remove and re-add a zone).
Resource        ../resources/microceph_harness.resource
Suite Setup     AZ Tests Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    crush    availability-zone    lxd    slow    integration

*** Keywords ***
AZ Tests Suite Setup
    Launch Outer Test VM    vm_name=microceph-az-vm    disk_size=50GiB
    Copy Scripts To VM
    Copy Snap To VM
    Clear IPTables
    Free Runner Disk
    Setup LXD In VM
    Create LXD Containers With Loop Devices    public
    Install MicroCeph On All Nodes

AZ Get Default Rule
    [Documentation]    Returns the current default CRUSH rule ID from node-wrk0.
    ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph config get mon osd_pool_default_crush_rule" | tr -d '[:space:]'    30
    RETURN    ${result.stdout.strip()}

AZ Get Rule ID
    [Documentation]    Returns the crush rule_id for a named rule from node-wrk0.
    [Arguments]    ${rule_name}
    ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph osd crush rule dump ${rule_name} -f json | jq -r '.rule_id'"    30
    RETURN    ${result.stdout.strip()}

AZ Wait Healthy
    [Documentation]    Polls HEALTH_OK on node-wrk0 (60 × 5 s = 300 s max).
    FOR    ${i}    IN RANGE    60
        ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph health"    30
        IF    "HEALTH_OK" in $result.stdout    RETURN
        Sleep    5s
    END
    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph -s"    30
    Fail    Cluster did not reach HEALTH_OK

AZ Get OSD ID For Node
    [Documentation]    Returns the numeric OSD ID for the first OSD on a given host node.
    ...    Runs lxc exec to get the OSD tree JSON on the outer VM, then pipes to jq (installed on outer VM).
    [Arguments]    ${node}
    ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph osd tree -f json 2>/dev/null" | jq -r '.nodes[] | select(.name=="${node}") | (.children // [])[] | select(. >= 0)' | head -1    30
    RETURN    ${result.stdout.strip()}

AZ Wait For OSD Count
    [Documentation]    Polls until OSD count reaches expected on node-wrk0 (20 × 5 s = 100 s max).
    [Arguments]    ${expected}
    Log To Console    [az] Waiting for ${expected} OSD(s)...
    FOR    ${i}    IN RANGE    20
        ${result}=    Run In VM    lxc exec node-wrk0 -- bash -c "microceph.ceph -s -f json 2>/dev/null | jq -r '.osdmap.num_in_osds // 0'"    30
        ${count}=    Evaluate    int('${result.stdout.strip()}') if '${result.stdout.strip()}'.isdigit() else 0
        IF    ${count} >= ${expected}
            Log To Console    [az] Found ${count} OSD(s)
            RETURN
        END
        Sleep    5s
    END
    Fail    Never reached ${expected} OSDs on node-wrk0

OSD Tree Should Contain AZ Rack Bucket
    [Documentation]    Asserts the CRUSH OSD tree on node-wrk0 contains the rack bucket for ${az_name}.
    [Arguments]    ${az_name}
    Run In VM And Check    lxc exec node-wrk0 -- sh -c "microceph.ceph osd tree" | grep -F "az.${az_name}"    30

Bootstrap AZ Cluster
    [Documentation]    Bootstraps 4-node cluster across 3 availability zones:
    ...    node-wrk0/1 in az-a, node-wrk2 in az-b, node-wrk3 in az-c.
    Log To Console    [az] Bootstrapping cluster with 3 availability zones...
    ${nw}=    Get Public Network CIDR
    Run In Container    node-wrk0    microceph cluster bootstrap --public-network=${nw} --availability-zone=az-a    120
    Sleep    5s
    ${tok1}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph cluster add node-wrk1"    60
    Run In Container    node-wrk1    microceph cluster join ${tok1.stdout.strip()} --availability-zone=az-a    120
    Sleep    3s
    ${tok2}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph cluster add node-wrk2"    60
    Run In Container    node-wrk2    microceph cluster join ${tok2.stdout.strip()} --availability-zone=az-b    120
    Sleep    3s
    ${tok3}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph cluster add node-wrk3"    60
    Run In Container    node-wrk3    microceph cluster join ${tok3.stdout.strip()} --availability-zone=az-c    120
    Sleep    3s
    Wait For N Nodes In Cluster    4
    Run In Container    node-wrk0    microceph status    30
    Log To Console    [az] Cluster ready with 4 nodes across 3 AZs

*** Test Cases ***
Test Bootstrap Cluster With Availability Zones
    [Documentation]    Bootstraps a 4-node cluster with node-wrk0/1 in az-a, wrk2 in az-b, wrk3 in az-c.
    ...    Verifies all 4 nodes joined successfully.
    [Tags]    crush    availability-zone
    Bootstrap AZ Cluster

Test CRUSH Default Rule Is OSD Level Before Any OSDs
    [Documentation]    Verifies the default CRUSH rule is the OSD-level rule before any OSDs are added.
    [Tags]    crush    availability-zone
    ${osd_rule_id}=    AZ Get Rule ID    microceph_auto_osd
    Log To Console    [az] OSD rule id=${osd_rule_id}
    ${default_rule}=    AZ Get Default Rule
    Should Be Equal As Strings    ${default_rule}    ${osd_rule_id}    msg=Expected OSD rule before first OSD

Test Add First OSD In AZ-A On Node Wrk0
    [Documentation]    Adds /dev/sdia as an OSD on node-wrk0 (az-a) and waits for 1 OSD.
    [Tags]    crush    availability-zone    osd
    Run In Container    node-wrk0    microceph disk add /dev/sdia --wipe    120
    AZ Wait For OSD Count    1
    Sleep    1s

Test CRUSH Rule Stays OSD Level With Single AZ
    [Documentation]    Verifies the default CRUSH rule remains at OSD level after one OSD in az-a.
    [Tags]    crush    availability-zone
    ${osd_rule_id}=    AZ Get Rule ID    microceph_auto_osd
    ${default_rule}=    AZ Get Default Rule
    Should Be Equal As Strings    ${default_rule}    ${osd_rule_id}    msg=Expected OSD rule after az-a first OSD

Test Add Second OSD In AZ-A On Node Wrk1
    [Documentation]    Adds /dev/sdia as an OSD on node-wrk1 (az-a) and waits for 2 OSDs.
    [Tags]    crush    availability-zone    osd
    Run In Container    node-wrk1    microceph disk add /dev/sdia --wipe    120
    AZ Wait For OSD Count    2
    Sleep    1s

Test CRUSH Rule Stays OSD Level With Two Hosts In Same AZ
    [Documentation]    Verifies the CRUSH rule remains OSD-level with 2 hosts both in az-a.
    [Tags]    crush    availability-zone
    ${osd_rule_id}=    AZ Get Rule ID    microceph_auto_osd
    ${default_rule}=    AZ Get Default Rule
    Should Be Equal As Strings    ${default_rule}    ${osd_rule_id}    msg=Expected OSD rule after 2nd az-a OSD

Test Add First OSD In AZ-B On Node Wrk2
    [Documentation]    Adds /dev/sdia as an OSD on node-wrk2 (az-b) and waits for 3 OSDs.
    [Tags]    crush    availability-zone    osd
    Run In Container    node-wrk2    microceph disk add /dev/sdia --wipe    120
    AZ Wait For OSD Count    3
    Sleep    1s

Test CRUSH Rule Stays OSD Level With Two AZs
    [Documentation]    Verifies the CRUSH rule remains OSD-level with OSDs in two AZs (az-a, az-b).
    [Tags]    crush    availability-zone
    ${osd_rule_id}=    AZ Get Rule ID    microceph_auto_osd
    ${default_rule}=    AZ Get Default Rule
    Should Be Equal As Strings    ${default_rule}    ${osd_rule_id}    msg=Expected OSD rule with only 2 AZs

Test Add OSD In AZ-C On Node Wrk3
    [Documentation]    Adds /dev/sdia as an OSD on node-wrk3 (az-c) and waits for 4 OSDs.
    [Tags]    crush    availability-zone    osd
    Run In Container    node-wrk3    microceph disk add /dev/sdia --wipe    120
    AZ Wait For OSD Count    4
    Sleep    1s

Test CRUSH Rule Upgraded To Rack Level With Three AZs
    [Documentation]    Verifies the default CRUSH rule upgrades to rack level once all 3 AZs have OSDs.
    [Tags]    crush    availability-zone
    ${rack_rule_id}=    AZ Get Rule ID    microceph_auto_rack
    Log To Console    [az] Rack rule id=${rack_rule_id}
    ${default_rule}=    AZ Get Default Rule
    Should Be Equal As Strings    ${default_rule}    ${rack_rule_id}    msg=Expected rack rule after az-c OSD added

Test OSD Tree Shows All Three AZ Rack Buckets
    [Documentation]    Verifies the CRUSH OSD tree contains rack buckets for az-a, az-b, and az-c.
    [Tags]    crush    availability-zone
    OSD Tree Should Contain AZ Rack Bucket    az-a
    OSD Tree Should Contain AZ Rack Bucket    az-b
    OSD Tree Should Contain AZ Rack Bucket    az-c

Test CRUSH Add Bucket Idempotent
    [Documentation]    Verifies that osd crush add-bucket is idempotent: calling it again for an
    ...    existing bucket must not fail. Guards against Ceph returning EEXIST in the future.
    [Tags]    crush    availability-zone
    Run In VM And Check    lxc exec node-wrk0 -- sh -c "microceph.ceph osd crush add-bucket az.az-a rack"    30

Test Remove AZ-C OSD Blocked By Rack Protection
    [Documentation]    Verifies that attempting to remove the only OSD in az-c is blocked to
    ...    protect the rack-level failure domain.
    [Tags]    crush    availability-zone
    ${wrk3_osd}=    AZ Get OSD ID For Node    node-wrk3
    Log To Console    [az] node-wrk3 has osd.${wrk3_osd}
    ${remove_out}=    Run In VM    lxc exec node-wrk3 -- sh -c "microceph disk remove osd.${wrk3_osd} 2>&1" || true    60
    Should Contain    ${remove_out.stdout}    rack-level failure domain    msg=Expected rack protection to block disk remove

Test Confirm Downgrade Flag Does Not Bypass Rack Protection
    [Documentation]    Verifies --confirm-failure-domain-downgrade still does not bypass rack protection.
    [Tags]    crush    availability-zone
    ${wrk3_osd}=    AZ Get OSD ID For Node    node-wrk3
    ${downgrade_out}=    Run In VM    lxc exec node-wrk3 -- sh -c "microceph disk remove osd.${wrk3_osd} --confirm-failure-domain-downgrade 2>&1" || true    60
    Should Contain    ${downgrade_out.stdout}    rack-level failure domain    msg=--confirm-failure-domain-downgrade should not bypass rack protection

Test Force Remove OSD From AZ-C
    [Documentation]    Force-removes the OSD from az-c using --bypass-safety-checks and waits for 3 OSDs.
    [Tags]    crush    availability-zone    osd
    ${wrk3_osd}=    AZ Get OSD ID For Node    node-wrk3
    Log To Console    [az] Force-removing osd.${wrk3_osd} from az-c...
    Run In Container    node-wrk3    microceph disk remove osd.${wrk3_osd} --bypass-safety-checks    120
    AZ Wait For OSD Count    3

Test Cluster Healthy After AZ-C OSD Removal
    [Documentation]    Waits for the cluster to reach HEALTH_OK after the az-c OSD was removed.
    [Tags]    crush    availability-zone
    AZ Wait Healthy
    Run In Container    node-wrk0    microceph.ceph osd tree    30

Test AZ-C Rack Bucket Persists After OSD Removal
    [Documentation]    Verifies the az.az-c rack bucket still exists in the CRUSH tree after the OSD was removed.
    [Tags]    crush    availability-zone
    OSD Tree Should Contain AZ Rack Bucket    az-c

Test Remove Node Wrk3 From Cluster
    [Documentation]    Removes node-wrk3 from the cluster with --force and verifies the cluster is healthy.
    [Tags]    crush    availability-zone
    Log To Console    [az] Removing node-wrk3 from cluster...
    Run In Container    node-wrk0    microceph cluster remove node-wrk3 --force    120
    Sleep    5s
    Run In Container    node-wrk0    microceph.ceph -s    30
    Run In Container    node-wrk0    microceph.ceph osd tree    30

Test AZ-C Rack Bucket Persists After Node Removal
    [Documentation]    Verifies the az.az-c rack bucket still exists after node-wrk3 was removed.
    [Tags]    crush    availability-zone
    OSD Tree Should Contain AZ Rack Bucket    az-c
    AZ Wait Healthy

Test Rejoin Node Wrk3 Into AZ-C
    [Documentation]    Re-adds node-wrk3 to the cluster with --availability-zone=az-c and waits for 4 nodes.
    [Tags]    crush    availability-zone
    Log To Console    [az] Re-joining node-wrk3 with az-c...
    Run In VM And Check    lxc exec node-wrk3 -- sh -c "fuser -k /dev/sdia" 2>/dev/null || true    10
    Sleep    2s
    ${tok}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph cluster add node-wrk3"    60
    Run In Container    node-wrk3    microceph cluster join ${tok.stdout.strip()} --availability-zone=az-c    120
    Wait For N Nodes In Cluster    4
    Run In Container    node-wrk0    microceph status    30
    Log To Console    [az] node-wrk3 rejoined in az-c

Test Add OSD To Rejoined AZ-C Node
    [Documentation]    Adds /dev/sdia to the rejoined node-wrk3 (az-c) and waits for 4 OSDs and healthy cluster.
    [Tags]    crush    availability-zone    osd
    Run In Container    node-wrk3    microceph disk add /dev/sdia --wipe    120
    AZ Wait For OSD Count    4
    AZ Wait Healthy
    Run In Container    node-wrk0    microceph.ceph -s    30
    Run In Container    node-wrk0    microceph.ceph osd tree    30

Test Rack CRUSH Rule Maintained After AZ Rejoin
    [Documentation]    Verifies the rack-level CRUSH rule is still the default after re-adding az-c,
    ...    and that the az.az-c bucket is present in the OSD tree.
    [Tags]    crush    availability-zone
    ${rack_rule_id}=    AZ Get Rule ID    microceph_auto_rack
    ${default_rule}=    AZ Get Default Rule
    Should Be Equal As Strings    ${default_rule}    ${rack_rule_id}    msg=Rack rule should still be active after re-add
    OSD Tree Should Contain AZ Rack Bucket    az-c
    Log To Console    [az] PASSED: rack rule maintained and az-c bucket present
