*** Settings ***
Documentation    Shared keywords for the multi-site replication suites (RBD + CephFS).
Resource         microceph_harness.resource

*** Keywords ***

Bootstrap Two Sites
    [Documentation]    Bootstraps sitea (node-wrk0/1) and siteb (node-wrk2/3) each with 2 loop-file OSDs.
    ...    Used by both RBD and CephFS replication suites.
    Log To Console    [replication] Bootstrapping sitea (node-wrk0/1)...
    Run In Container    node-wrk0    microceph cluster bootstrap    120
    Run In Container    node-wrk0    microceph disk add loop,1G,2    120
    ${tok}=    Exec In Container    node-wrk0    microceph    cluster    add    node-wrk1    timeout=60
    Run In Container    node-wrk1    microceph cluster join ${tok.stdout.strip()}    120
    Run In Container    node-wrk1    microceph disk add loop,1G,2    120
    Sleep    10s
    Log To Console    [replication] Bootstrapping siteb (node-wrk2/3)...
    Run In Container    node-wrk2    microceph cluster bootstrap    120
    Run In Container    node-wrk2    microceph disk add loop,1G,2    120
    ${tok2}=    Exec In Container    node-wrk2    microceph    cluster    add    node-wrk3    timeout=60
    Run In Container    node-wrk3    microceph cluster join ${tok2.stdout.strip()}    120
    Run In Container    node-wrk3    microceph disk add loop,1G,2    120
    Sleep    10s

Exchange Remote Site Tokens
    [Documentation]    Exports tokens from each site and imports them cross-site with --local-name.
    ...    After this call, sitea knows siteb and siteb knows sitea.
    Log To Console    [replication] Exchanging site tokens...
    ${sitea_token}=    Exec In Container    node-wrk0    microceph    cluster    export    siteb    timeout=60
    ${siteb_token}=    Exec In Container    node-wrk2    microceph    cluster    export    sitea    timeout=60
    Run In Container    node-wrk0    microceph remote import siteb ${siteb_token.stdout.strip()} --local-name=sitea    60
    Run In Container    node-wrk2    microceph remote import sitea ${sitea_token.stdout.strip()} --local-name=siteb    60
    Sleep    15s    reason=Allow remote credentials to propagate across the cluster

Verify Remote Authentication On All Nodes
    [Documentation]    Verifies ceph commands against the remote cluster work on all 4 nodes.
    Log To Console    [replication] Verifying remote authentication on all nodes...
    Run In Container    node-wrk0    microceph.ceph -s    30
    Run In Container    node-wrk0    microceph.ceph -s --cluster siteb --id sitea    30
    Assert Remote List Has    node-wrk0    name    siteb
    Run In Container    node-wrk1    microceph.ceph -s    30
    Run In Container    node-wrk1    microceph.ceph -s --cluster siteb --id sitea    30
    Assert Remote List Has    node-wrk1    local_name    sitea
    Run In Container    node-wrk2    microceph.ceph -s    30
    Run In Container    node-wrk2    microceph.ceph -s --cluster sitea --id siteb    30
    Assert Remote List Has    node-wrk2    name    sitea
    Run In Container    node-wrk3    microceph.ceph -s    30
    Run In Container    node-wrk3    microceph.ceph -s --cluster sitea --id siteb    30
    Assert Remote List Has    node-wrk3    local_name    siteb

Mount CephFS From Container
    [Documentation]    Pulls ceph.conf and keyring from ${container} into /etc/ceph/ on the outer VM,
    ...    then mounts the CephFS volume ${vol} at ${mountpoint}.
    [Arguments]    ${container}    ${mountpoint}    ${vol}
    Run In VM And Check    sudo lxc file pull ${container}/var/snap/microceph/current/conf/ceph.conf /etc/ceph/    30
    Run In VM And Check    sudo lxc file pull ${container}/var/snap/microceph/current/conf/ceph.keyring /etc/ceph/    30
    Run In VM And Check    sudo mkdir -p ${mountpoint}    10
    Run In VM And Check    sudo mount -t ceph :/ ${mountpoint} -o name=admin,fs=${vol}    60

Enable Mirror Service On Both Sites
    [Documentation]    Enables the given mirror service (rbd-mirror or cephfs-mirror) on node-wrk0 and node-wrk2.
    [Arguments]    ${service}
    Log To Console    [replication] Enabling ${service} on both sites...
    Run In Container    node-wrk0    microceph enable ${service}    120
    Run In Container    node-wrk2    microceph enable ${service}    120

Disable Mirror Service On Both Sites
    [Documentation]    Disables the given mirror service on node-wrk0 and node-wrk2.
    [Arguments]    ${service}
    Log To Console    [replication] Disabling ${service} on both sites...
    Run In Container    node-wrk0    microceph disable ${service}    60
    Run In Container    node-wrk2    microceph disable ${service}    60
