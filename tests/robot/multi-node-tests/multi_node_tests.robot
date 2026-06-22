*** Settings ***
Documentation    multi-node-tests
...    Full multi-node test: CRUSH host failure domain, OSD add/remove, node removal,
...    service migration, client config, RGW SSL, cross-node certificate rotation.
Resource        ../resources/microceph_harness.resource
Library         ../resources/cluster_ops.py
Suite Setup     Multi Node Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    cluster    osd    rgw    integration    lxd    slow

*** Keywords ***
Multi Node Suite Setup
    Provision Multinode VM    microceph-mn-vm    ${OUTER_VM_DISK}    public
    Bootstrap Head Node    public
    Join Worker Nodes To Cluster    public
    Verify Ceph Config Has Public Network

Verify Ceph Config Has Public Network
    [Documentation]    Checks that all nodes' ceph.conf contains public_network entry.
    Log To Console    [config] Verifying all nodes have public_network in ceph.conf...
    ${nodes}=    Run In VM    lxc ls -c n --format csv    30
    FOR    ${node}    IN    @{nodes.stdout.strip().split('\n')}
        Run In Container And Check    ${node.strip()}    grep -q public_network /var/snap/microceph/current/conf/ceph.conf    30
    END

Enable Services On Head Node For
    [Documentation]    Enables mon/mds/mgr on a target node, running commands from node-wrk0.
    [Arguments]    ${node}
    Log To Console    [cluster] Enabling mon/mds/mgr on ${node} via node-wrk0...
    Run In Container    node-wrk0    microceph enable mon --target ${node}    120
    Run In Container    node-wrk0    microceph enable mds --target ${node}    120
    Run In Container    node-wrk0    microceph enable mgr --target ${node}    120
    FOR    ${i}    IN RANGE    8
        ${in_mon}=    Node Is In Mon List    ${node}
        IF    "${in_mon}" == "yes"    BREAK
        IF    ${i} == 7    Fail    ${node} did not appear in the mon list after 16 s
        Sleep    2s
    END
    Run In Container    node-wrk0    microceph.ceph -s    30

Enable RGW SSL On Head Node
    [Documentation]    Generates SSL cert and enables RGW with SSL on node-wrk0.
    Log To Console    [rgw] Enabling RGW SSL on node-wrk0...
    Generate Self Signed CA And Server Cert In Container    node-wrk0
    ${cert}=    Read Base64 File From Container    node-wrk0    /tmp/server.crt
    ${key}=    Read Base64 File From Container    node-wrk0    /tmp/server.key
    Run In Container    node-wrk0    microceph enable rgw --ssl-certificate=${cert} --ssl-private-key=${key}    120
    Wait For RGW On Head Node    1


Rotate Certificates Cross Node
    [Documentation]    Rotates the RGW SSL certificate on target using --target from node-wrk0.
    [Arguments]    ${target}
    Log To Console    [rgw] Testing certificate rotation on ${target} from node-wrk0...
    Run In Container    node-wrk0    sudo openssl genrsa -out /tmp/target-cert.key 2048 && sudo openssl req -new -key /tmp/target-cert.key -out /tmp/target-cert.csr -subj '/CN=target-cert' && echo 'subjectAltName = DNS:localhost' > /tmp/target-cert-ext.cnf && sudo openssl x509 -req -in /tmp/target-cert.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/target-cert.crt -days 365 -extfile /tmp/target-cert-ext.cnf    60
    ${target_addr_result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph status | grep -F '${target}' | grep -oP '\\(\\K[^)]+' || true"    30
    ${target_addr}=    Set Variable    ${target_addr_result.stdout.strip()}
    ${cert}=    Read Base64 File From Container    node-wrk0    /tmp/target-cert.crt
    ${key}=    Read Base64 File From Container    node-wrk0    /tmp/target-cert.key
    Run In Container    node-wrk0    microceph certificate set rgw --ssl-certificate=${cert} --ssl-private-key=${key} --target ${target} --restart    120
    Wait For RGW On Head Node    1
    Wait For RGW SSL Port    ${target_addr}
    ${cn}=    Get RGW SSL CN    ${target_addr}
    Should Be Equal As Strings    ${cn}    target-cert    msg=Expected CN=target-cert on ${target}
    ${orig_cert}=    Read Base64 File From Container    node-wrk0    /tmp/server.crt
    ${orig_key}=    Read Base64 File From Container    node-wrk0    /tmp/server.key
    Run In Container    node-wrk0    microceph certificate set rgw --ssl-certificate=${orig_cert} --ssl-private-key=${orig_key} --target ${target} --restart    120
    Wait For RGW On Head Node    1
    Wait For RGW SSL Port    ${target_addr}

Wait For CRUSH Rule
    [Documentation]    Polls until osd_pool_default_crush_rule equals ${expected} on node-wrk0.
    [Arguments]    ${expected}    ${attempts}=30
    FOR    ${i}    IN RANGE    ${attempts}
        ${result}=    Run In VM    lxc exec node-wrk0 -- microceph.ceph config get mon osd_pool_default_crush_rule    30
        ${val}=    Set Variable    ${result.stdout.strip()}
        IF    "${val}" == "${expected}"
            Log To Console    [crush] CRUSH rule is now ${val}
            RETURN
        END
        Sleep    5s
    END
    Fail    CRUSH rule did not reach ${expected} after ${attempts} attempts (last=${val})

Remove Node Head Node
    [Documentation]    Removes specified node via node-wrk0.
    ...    Waits for cluster health before attempting removal and retries on transient
    ...    'context canceled' failures from the pre-remove hook RPC (the target node
    ...    may be busy rebalancing OSDs and not respond in time).
    [Arguments]    ${node}
    Log To Console    [cluster] Removing node ${node} via node-wrk0...
    Verify Cluster Health Head Node
    FOR    ${attempt}    IN RANGE    3
        ${result}=    Run In VM    lxc exec node-wrk0 -- microceph cluster remove ${node}    120
        IF    ${result.rc} == 0    BREAK
        Log To Console    [cluster] Remove attempt ${attempt} failed (rc=${result.rc}): ${result.stderr.strip()} — retrying in 10s
        IF    ${attempt} == 2    Fail    Failed to remove ${node} after 3 attempts: ${result.stderr}
        Sleep    10s
    END
    FOR    ${i}    IN RANGE    8
        ${in_mon}=    Node Is In Mon List    ${node}
        IF    "${in_mon}" != "yes"    BREAK
        IF    ${i} == 7    Fail    ${node} still in mon list 40 s after removal
        Sleep    5s
    END
    Sleep    1s
    Run In Container    node-wrk0    microceph.ceph -s    30
    Run In Container    node-wrk0    microceph status    30

CRUSH Rule Should Be
    [Documentation]    Verifies the default CRUSH rule matches expected_rule_id on node-wrk0.
    [Arguments]    ${expected_rule_id}
    Run In VM And Check    lxc exec node-wrk0 -- microceph.ceph config get mon osd_pool_default_crush_rule | fgrep -x ${expected_rule_id}    30

Wait For N OSDs Up And In On Head Node
    [Documentation]    Polls ceph status on node-wrk0 until N OSDs are all up and in.
    [Arguments]    ${n}    ${tries}=30
    FOR    ${i}    IN RANGE    ${tries}
        ${osd_check}=    Run In VM    lxc exec node-wrk0 -- microceph.ceph -s | egrep "osd: ${n} osds: ${n} up.*${n} in"    30
        IF    ${osd_check.rc} == 0    RETURN
        Sleep    5s
    END
    Fail    Timed out waiting for ${n} OSDs up and in on node-wrk0

Wait For CRUSH Auto Host Rule On Head Node
    [Documentation]    Polls until microceph_auto_host appears in crush rule list on node-wrk0.
    [Arguments]    ${tries}=20
    FOR    ${i}    IN RANGE    ${tries}
        ${result}=    Run In VM    lxc exec node-wrk0 -- microceph.ceph osd crush rule ls | grep -F microceph_auto_host    30
        IF    ${result.rc} == 0    RETURN
        Sleep    5s
    END
    Fail    microceph_auto_host crush rule never appeared in osd crush rule ls

Verify Node Removed From Cluster
    [Documentation]    Asserts the node is gone from microceph status and that the mon daemon
    ...    count is either 3 (clean removal) or 4 with the node still out of quorum (transitional).
    [Arguments]    ${node}
    Run In VM Must Fail    lxc exec node-wrk0 -- microceph status | grep "^- ${node} "
    ${ceph_s}=    Run In VM    lxc exec node-wrk0 -- microceph.ceph -s    30
    ${has_3}=    Evaluate    "mon: 3 daemons" in """${ceph_s.stdout}"""
    ${has_4_ooq}=    Evaluate    "mon: 4 daemons" in """${ceph_s.stdout}""" and "out of quorum: ${node}" in """${ceph_s.stdout}"""
    Should Be True    ${has_3} or ${has_4_ooq}    msg=Expected mon: 3 daemons or 4 with ${node} out-of-quorum after node removal

Check Client Configs
    [Documentation]    Sets cluster-wide and per-host client configs, verifies they land in
    ...    each worker's ceph.conf, then resets and verifies they are gone.
    Log To Console    [config] Checking client config set/reset across nodes...
    Run In Container    node-wrk0    microceph client config set rbd_cache true    30
    FOR    ${node}    ${size}    IN    node-wrk1    512    node-wrk2    1024
        Run In Container    ${node}    microceph client config set rbd_cache_size ${size} --target ${node}    30
    END
    FOR    ${node}    ${size}    IN    node-wrk1    512    node-wrk2    1024
        ${r1}=    Run In Container Unchecked    ${node}    cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache = true'    30
        ${r2}=    Run In Container Unchecked    ${node}    cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache_size = ${size}'    30
        Should Be Equal As Strings    ${r1.stdout.strip()}    1    msg=rbd_cache not set on ${node}
        Should Be Equal As Strings    ${r2.stdout.strip()}    1    msg=rbd_cache_size not set on ${node}
    END
    Run In Container    node-wrk0    microceph client config reset rbd_cache --yes-i-really-mean-it    30
    Run In Container    node-wrk0    microceph client config reset rbd_cache_size --yes-i-really-mean-it    30
    FOR    ${node}    IN    node-wrk1    node-wrk2
        ${r1}=    Run In Container Unchecked    ${node}    cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache '    30
        ${r2}=    Run In Container Unchecked    ${node}    cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache_size'    30
        Should Be Equal As Strings    ${r1.stdout.strip()}    0    msg=rbd_cache still in ceph.conf on ${node}
        Should Be Equal As Strings    ${r2.stdout.strip()}    0    msg=rbd_cache_size still in ceph.conf on ${node}
    END

Test Service Migration
    [Documentation]    Migrates services from ${src} to ${dst}, polls until placement settles,
    ...    then asserts ${src} has only OSD and ${dst} has mds, mgr, mon.
    [Arguments]    ${src}    ${dst}
    Log To Console    [cluster] Migrating services from ${src} to ${dst}...
    Run In Container    node-wrk0    microceph cluster migrate ${src} ${dst}    120
    FOR    ${i}    IN RANGE    8
        ${status}=    Run In Container Unchecked    node-wrk0    microceph status    30
        ${src_ok}    ${dst_ok}=    Parse Migration Status    ${status.stdout}    ${src}    ${dst}
        IF    ${src_ok} and ${dst_ok}
            Log To Console    [cluster] Services migrated successfully
            BREAK
        END
        Sleep    10s
    END
    Run In Container    node-wrk0    microceph status    30
    Run In Container    node-wrk0    microceph.ceph -s    30
    ${status}=    Run In Container Unchecked    node-wrk0    microceph status    30
    ${src_ok}    ${dst_ok}=    Parse Migration Status    ${status.stdout}    ${src}    ${dst}
    Should Be True    ${src_ok}    msg=${src} should have only OSD after migration
    Should Be True    ${dst_ok}    msg=${dst} should have mds,mgr,mon after migration

*** Test Cases ***
Test MicroCeph Status After Cluster Setup
    [Documentation]    Smoke-checks that microceph status succeeds on the fully-formed 4-node cluster
    ...    before any OSDs are added — mirrors the original "Exercise microceph status" CI step.
    [Tags]    multi-node    cluster
    Run In Container    node-wrk0    microceph status    30

Test CRUSH Host Failure Domain Auto Scaling
    [Documentation]    Adds OSDs one at a time and verifies the CRUSH rule transitions from
    ...    OSD-level (rule 1) to host-level (rule 2) once 3 OSDs across 3 hosts are present.
    [Tags]    multi-node    crush    osd
    CRUSH Rule Should Be    1
    Add OSD To Node    node-wrk1
    Add OSD To Node    node-wrk2
    Wait For OSD Count Head    2
    CRUSH Rule Should Be    1
    Add OSD To Node    node-wrk0
    Wait For OSD Count Head    3
    Wait For CRUSH Rule    2
    Wait For OSD Count Head    3
    Wait For N OSDs Up And In On Head Node    3
    Wait For CRUSH Auto Host Rule On Head Node
    Wait For Pool Crush Rule    2

Test OSD Add Remove
    [Documentation]    Adds a 4th OSD to node-wrk3 then removes OSD 4; verifies count returns to 3.
    [Tags]    multi-node    osd
    Add OSD To Node    node-wrk3
    Wait For OSD Count Head    4
    Run In Container    node-wrk0    microceph disk remove 4    120
    Wait For N OSDs Up And In On Head Node    3

Test Service Migration
    [Documentation]    Migrates services from node-wrk1 to node-wrk3 and verifies placement.
    [Tags]    multi-node    cluster
    Test Service Migration    node-wrk1    node-wrk3

Test Client Config Set Reset
    [Documentation]    Issues cluster-wide and per-host client config set, verifies, then resets.
    [Tags]    multi-node    mgr
    Check Client Configs

Test Multi Node RGW SSL
    [Documentation]    Enables RGW with SSL on node-wrk0 and node-wrk1; waits for both daemons.
    [Tags]    multi-node    rgw
    Enable Services On Head Node For    node-wrk1
    Enable RGW SSL On Head Node
    ${cert}=    Read Base64 File From Container    node-wrk0    /tmp/server.crt
    ${key}=    Read Base64 File From Container    node-wrk0    /tmp/server.key
    Run In VM And Check    lxc exec node-wrk0 -- microceph enable rgw --target node-wrk1 --ssl-certificate=${cert} --ssl-private-key=${key}    120
    Wait For RGW On Head Node    2

Test Cross Node Certificate Rotation
    [Documentation]    Rotates the RGW SSL certificate on node-wrk1 using --target from the head node.
    [Tags]    multi-node    rgw
    Rotate Certificates Cross Node    node-wrk1

Test Prohibit CRUSH Scaledown
    [Documentation]    Removes wrk0's OSD (OSD 3) with --prohibit-crush-scaledown and verifies
    ...    the host failure domain rule is not downgraded.
    [Tags]    multi-node    crush    osd
    Run In Container    node-wrk0    microceph disk remove 3 --prohibit-crush-scaledown --bypass-safety-checks    120
    Wait For CRUSH Auto Host Rule On Head Node

Test Node Removal
    [Documentation]    Re-adds wrk0's OSD then removes node-wrk3 from the cluster.
    ...    After removal verifies node-wrk3 is gone from microceph status and that the mon
    ...    daemon count is either 3 (wrk3 removed cleanly) or 4 with wrk3 out of quorum
    ...    (transitional state), mirroring the original bash "Test remove node wrk3" step.
    [Tags]    multi-node    cluster
    Add OSD To Node    node-wrk0
    Wait For OSD Count Head    3
    Remove Node Head Node    node-wrk3
    Verify Node Removed From Cluster    node-wrk3
