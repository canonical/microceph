*** Settings ***
Documentation    smb-multinode-test
...    Verifies the complete mixed three-node SMB lifecycle: one direct cluster on
...    node-wrk0 and one CTDB cluster on node-wrk1/node-wrk2, including placement
...    isolation, member loss, surviving-node I/O, recovery, and independent removal.
Resource        ../resources/microceph_harness.resource
Suite Setup     SMB Multinode Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    smb    ctdb    failover    cephfs    lxd    slow    integration    strict-confinement

*** Variables ***
${SMB_VOLUME}              smbsfs
${SMB_SINGLE_SUBVOLUME}    smbsingle
${SMB_DUAL_SUBVOLUME}      smbdual
${SMB_SINGLE_CLUSTER}      smbsingle
${SMB_DUAL_CLUSTER}        smbdual
${SMB_SINGLE_USER}         smbuser1
${SMB_SINGLE_PASSWORD}     SmbSinglePassword1
${SMB_DUAL_USER}           smbuser2
${SMB_DUAL_PASSWORD}       SmbDualPassword1
${SMB_SHARE}               cephfs

*** Keywords ***
SMB Multinode Suite Setup
    Provision Multinode VM    microceph-smbmn-vm    ${OUTER_VM_DISK}    public
    Bootstrap Head Node    public
    Join Worker Nodes To Cluster    public    2
    Add OSD To Node    node-wrk0
    Add OSD To Node    node-wrk1
    Add OSD To Node    node-wrk2
    Wait For OSD Count Head    3
    Wait For Cluster Health OK    node=node-wrk0
    Run In Head Node    microceph.ceph fs volume create ${SMB_VOLUME}    120
    Run In Head Node    microceph.ceph fs subvolume create ${SMB_VOLUME} ${SMB_SINGLE_SUBVOLUME} --mode 777    120
    Run In Head Node    microceph.ceph fs subvolume create ${SMB_VOLUME} ${SMB_DUAL_SUBVOLUME} --mode 777    120
    Wait For Cluster Health OK    node=node-wrk0
    FOR    ${node}    IN    node-wrk0    node-wrk1    node-wrk2
        Run In Container And Check    ${node}    snap connect microceph:smb-identity    30
        Run In Container And Check    ${node}    snap connect microceph:ctdb-run    30
    END
    Run In Head Node    microceph.ceph mgr module enable microceph    30
    Run In Head Node    microceph.ceph orch set backend microceph    30
    Run In Head Node    microceph.ceph mgr module enable smb    30
    Run In VM And Check    sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq    120
    Run In VM And Check    sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y smbclient    300

Create Mixed SMB Topology
    Run In Head Node    microceph.ceph smb cluster create ${SMB_SINGLE_CLUSTER} user --define-user-pass '${SMB_SINGLE_USER}%${SMB_SINGLE_PASSWORD}' --placement "1 node-wrk0"    120
    Run In Head Node    microceph.ceph smb cluster create ${SMB_DUAL_CLUSTER} user --define-user-pass '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' --placement "2 node-wrk1 node-wrk2"    120
    Run In Head Node    echo cmVzb3VyY2VfdHlwZTogY2VwaC5zbWIuc2hhcmUKY2x1c3Rlcl9pZDogc21ic2luZ2xlCnNoYXJlX2lkOiBjZXBoZnMKY2VwaGZzOgogIHZvbHVtZTogc21ic2ZzCiAgc3Vidm9sdW1lOiBzbWJzaW5nbGUKICBwcm92aWRlcjogc2FtYmEtdmZzL25ldwo= | base64 --decode | microceph.ceph smb apply -i -    120
    Run In Head Node    echo cmVzb3VyY2VfdHlwZTogY2VwaC5zbWIuc2hhcmUKY2x1c3Rlcl9pZDogc21iZHVhbApzaGFyZV9pZDogY2VwaGZzCmNlcGhmczoKICB2b2x1bWU6IHNtYnNmcwogIHN1YnZvbHVtZTogc21iZHVhbAogIHByb3ZpZGVyOiBzYW1iYS12ZnMvbmV3Cg== | base64 --decode | microceph.ceph smb apply -i -    120
    Wait For SMB Service    enabled    active    node=node-wrk0
    Wait For SMB Service    enabled    active    node=node-wrk1
    Wait For SMB Service    enabled    active    node=node-wrk2
    Wait For CTDB Service    disabled    inactive    node=node-wrk0
    Wait For CTDB Nodes Service    disabled    inactive    node=node-wrk0
    Wait For CTDB Service    enabled    active    node=node-wrk1
    Wait For CTDB Nodes Service    enabled    active    node=node-wrk1
    Wait For CTDB Service    enabled    active    node=node-wrk2
    Wait For CTDB Nodes Service    enabled    active    node=node-wrk2
    Wait For CTDB Healthy Nodes    2    2    node=node-wrk1
    Wait For CTDB Healthy Nodes    2    2    node=node-wrk2

Verify Mixed SMB Placement
    Run In Head Node    microceph.ceph orch ls --service_type smb | grep -F 'smb.${SMB_SINGLE_CLUSTER}'    30
    Run In Head Node    microceph.ceph orch ls --service_type smb | grep -F 'smb.${SMB_DUAL_CLUSTER}'    30
    Run In Container And Check    node-wrk0    grep -Fx '${SMB_SINGLE_CLUSTER}' /var/snap/microceph/current/samba/cluster-id    30
    Run In Container And Check    node-wrk0    grep -E '"vfs objects": ".*ceph_new' /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk0    grep -F '"ceph_new:proxy": "no"' /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk1    grep -Fx '${SMB_DUAL_CLUSTER}' /var/snap/microceph/current/samba/cluster-id    30
    Run In Container And Check    node-wrk2    grep -Fx '${SMB_DUAL_CLUSTER}' /var/snap/microceph/current/samba/cluster-id    30
    Run In Container And Check    node-wrk1    grep -E '"vfs objects": ".*ceph_new' /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk2    grep -F '"ceph_new:proxy": "no"' /var/snap/microceph/current/samba/container.json    30

Verify Both SMB Clusters Serve Their CephFS Subvolumes
    ${ip_single}=    Get Node IP    node-wrk0
    ${ip_dual0}=    Get Node IP    node-wrk1
    ${ip_dual1}=    Get Node IP    node-wrk2
    Run In VM And Check    printf 'single cluster smb test\n' > /tmp/smb-single-source    30
    Run In VM And Check    smbclient //${ip_single}/${SMB_SHARE} -U '${SMB_SINGLE_USER}%${SMB_SINGLE_PASSWORD}' -c 'put /tmp/smb-single-source smb-single-file; get smb-single-file /tmp/smb-single-result'    60
    Run In VM And Check    cmp /tmp/smb-single-source /tmp/smb-single-result    30
    Run In VM And Check    printf 'dual cluster smb test\n' > /tmp/smb-dual-source    30
    Run In VM And Check    smbclient //${ip_dual0}/${SMB_SHARE} -U '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' -c 'put /tmp/smb-dual-source smb-dual-file'    60
    Run In VM And Check    smbclient //${ip_dual1}/${SMB_SHARE} -U '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' -c 'get smb-dual-file /tmp/smb-dual-result'    60
    Run In VM And Check    cmp /tmp/smb-dual-source /tmp/smb-dual-result    30

Verify CTDB Member Loss And Recovery
    ${ip_single}=    Get Node IP    node-wrk0
    ${ip_survivor}=    Get Node IP    node-wrk2
    Run In VM And Check    lxc stop node-wrk1 --force    60
    Wait For CTDB Healthy Nodes    2    1    node=node-wrk2
    Run In VM And Check    smbclient //${ip_survivor}/${SMB_SHARE} -U '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' -c 'get smb-dual-file /tmp/smb-dual-after-failure'    60
    Run In VM And Check    cmp /tmp/smb-dual-source /tmp/smb-dual-after-failure    30
    Run In VM And Check    printf 'written while one CTDB member is down\n' > /tmp/smb-failover-source    30
    Run In VM And Check    smbclient //${ip_survivor}/${SMB_SHARE} -U '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' -c 'put /tmp/smb-failover-source smb-failover-file'    60
    Run In VM And Check    smbclient //${ip_single}/${SMB_SHARE} -U '${SMB_SINGLE_USER}%${SMB_SINGLE_PASSWORD}' -c 'get smb-single-file /tmp/smb-single-during-failure'    60
    Run In VM And Check    cmp /tmp/smb-single-source /tmp/smb-single-during-failure    30
    Run In VM And Check    lxc start node-wrk1    60
    Wait For SMB Service    enabled    active    node=node-wrk1
    Wait For CTDB Service    enabled    active    node=node-wrk1
    Wait For CTDB Nodes Service    enabled    active    node=node-wrk1
    Wait For CTDB Healthy Nodes    2    2    node=node-wrk2
    Wait For CTDB Healthy Nodes    2    2    node=node-wrk1
    ${ip_recovered}=    Get Node IP    node-wrk1
    Run In VM And Check    smbclient //${ip_recovered}/${SMB_SHARE} -U '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' -c 'get smb-failover-file /tmp/smb-failover-result'    60
    Run In VM And Check    cmp /tmp/smb-failover-source /tmp/smb-failover-result    30

Verify Overlapping SMB Placement Is Rejected
    ${result}=    Run In Container Unchecked    node-wrk0    echo c2VydmljZV90eXBlOiBzbWIKc2VydmljZV9pZDogc21iLXRoaXJkCmNsdXN0ZXJfaWQ6IHNtYi10aGlyZApjb25maWdfdXJpOiByYWRvczovLy5zbWIvc21iLXRoaXJkL2NvbmZpZy5zbWIKcGxhY2VtZW50OgogIGhvc3RzOgogICAgLSBub2RlLXdyazAKICBjb3VudDogMQo= | base64 --decode | microceph.ceph orch apply -i -    120
    Should Not Be Equal As Integers    ${result.rc}    0    msg=Third SMB cluster on an occupied host unexpectedly succeeded
    ${error}=    Catenate    SEPARATOR=\n    ${result.stdout}    ${result.stderr}
    Should Contain    ${error}    at most one SMB cluster

Verify Clusters Can Be Removed Independently
    ${ip_dual}=    Get Node IP    node-wrk2
    Run In Head Node    microceph.ceph smb share rm ${SMB_SINGLE_CLUSTER} ${SMB_SHARE}    120
    Run In Head Node    microceph.ceph smb cluster rm ${SMB_SINGLE_CLUSTER}    120
    Wait For SMB Service    disabled    inactive    node=node-wrk0
    Run In Container And Check    node-wrk0    test ! -e /var/snap/microceph/current/samba/container.json    30
    Run In VM And Check    smbclient //${ip_dual}/${SMB_SHARE} -U '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' -c 'get smb-failover-file /tmp/smb-dual-after-single-removal'    60
    Run In VM And Check    cmp /tmp/smb-failover-source /tmp/smb-dual-after-single-removal    30
    Wait For CTDB Healthy Nodes    2    2    node=node-wrk2
    Run In Head Node    microceph.ceph smb share rm ${SMB_DUAL_CLUSTER} ${SMB_SHARE}    120
    Run In Head Node    microceph.ceph smb cluster rm ${SMB_DUAL_CLUSTER}    120
    Wait For SMB Service    disabled    inactive    node=node-wrk1
    Wait For SMB Service    disabled    inactive    node=node-wrk2
    Wait For CTDB Service    disabled    inactive    node=node-wrk1
    Wait For CTDB Nodes Service    disabled    inactive    node=node-wrk1
    Wait For CTDB Service    disabled    inactive    node=node-wrk2
    Wait For CTDB Nodes Service    disabled    inactive    node=node-wrk2
    Run In Container And Check    node-wrk1    test ! -e /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk2    test ! -e /var/snap/microceph/current/samba/container.json    30

*** Test Cases ***
Test Complete Mixed Three Node SMB Failover Scenario
    [Documentation]    Exercises two isolated SMB clusters, CTDB member loss and recovery, and independent cleanup.
    Create Mixed SMB Topology
    Verify Mixed SMB Placement
    Verify Both SMB Clusters Serve Their CephFS Subvolumes
    Verify CTDB Member Loss And Recovery
    Verify Overlapping SMB Placement Is Rejected
    Verify Clusters Can Be Removed Independently
