*** Settings ***
Documentation    smb-multinode-test
...    Verifies multiple native SMB clusters on disjoint hosts in a multi-node
...    LXD cluster: a one-host cluster (node-wrk0) and a two-host cluster
...    (node-wrk1, node-wrk2) serving the same CephFS subvolume, plus the
...    per-host single-cluster invariant.
Resource        ../resources/microceph_harness.resource
Suite Setup     SMB Multinode Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    smb    cephfs    lxd    slow    integration    strict-confinement

*** Variables ***
${SMB_VOLUME}             smbsfs
${SMB_SUBVOLUME}          smbshared
${SMB_SINGLE_CLUSTER}     smbsingle
${SMB_DUAL_CLUSTER}       smbdual
${SMB_SINGLE_USER}        smbuser1
${SMB_SINGLE_PASSWORD}    SmbSinglePassword1
${SMB_DUAL_USER}          smbuser2
${SMB_DUAL_PASSWORD}      SmbDualPassword1
${SMB_SHARE}              cephfs

*** Keywords ***
SMB Multinode Suite Setup
    Provision Multinode VM    microceph-smbmn-vm    ${OUTER_VM_DISK}    public
    Bootstrap Head Node    public
    Join Worker Nodes To Cluster    public
    Add OSD To Node    node-wrk0
    Add OSD To Node    node-wrk1
    Add OSD To Node    node-wrk2
    Wait For OSD Count Head    3
    Wait For Cluster Health OK    node=node-wrk0
    FOR    ${node}    IN    node-wrk0    node-wrk1    node-wrk2
        Run In Container And Check    ${node}    snap connect microceph:smb-identity    30
    END
    Run In Head Node    microceph.ceph mgr module enable microceph    30
    Run In Head Node    microceph.ceph orch set backend microceph    30
    Run In Head Node    microceph.ceph mgr module enable smb    30
    Run In Head Node    microceph.ceph fs volume create ${SMB_VOLUME}    120
    Run In Head Node    microceph.ceph fs subvolume create ${SMB_VOLUME} ${SMB_SUBVOLUME} --mode 777    120
    Wait For Cluster Health OK    node=node-wrk0
    Run In VM And Check    sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq    120
    Run In VM And Check    sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y smbclient    300

*** Test Cases ***
Test Create Two SMB Clusters On Disjoint Hosts
    [Documentation]    Creates a one-host SMB cluster on node-wrk0 and a two-host
    ...    cluster on node-wrk1/node-wrk2, each serving a share of the same subvolume.
    [Tags]    smb    multi-node
    Run In Head Node    microceph.ceph smb cluster create ${SMB_SINGLE_CLUSTER} user --define-user-pass '${SMB_SINGLE_USER}%${SMB_SINGLE_PASSWORD}' --placement "1 node-wrk0"    120
    Run In Head Node    microceph.ceph smb cluster create ${SMB_DUAL_CLUSTER} user --define-user-pass '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' --placement "2 node-wrk1 node-wrk2"    120
    Run In Head Node    echo cmVzb3VyY2VfdHlwZTogY2VwaC5zbWIuc2hhcmUKY2x1c3Rlcl9pZDogc21ic2luZ2xlCnNoYXJlX2lkOiBjZXBoZnMKY2VwaGZzOgogIHZvbHVtZTogc21ic2ZzCiAgc3Vidm9sdW1lOiBzbWJzaGFyZWQKICBwcm92aWRlcjogc2FtYmEtdmZzL25ldwo= | base64 --decode | microceph.ceph smb apply -i -    120
    Run In Head Node    echo cmVzb3VyY2VfdHlwZTogY2VwaC5zbWIuc2hhcmUKY2x1c3Rlcl9pZDogc21iZHVhbApzaGFyZV9pZDogY2VwaGZzCmNlcGhmczoKICB2b2x1bWU6IHNtYnNmcwogIHN1YnZvbHVtZTogc21ic2hhcmVkCiAgcHJvdmlkZXI6IHNhbWJhLXZmcy9uZXcK | base64 --decode | microceph.ceph smb apply -i -    120
    Wait For SMB Service    enabled    active    node=node-wrk0
    Wait For SMB Service    enabled    active    node=node-wrk1
    Wait For SMB Service    enabled    active    node=node-wrk2

Test SMB Clusters Land On Their Own Hosts
    [Documentation]    Both clusters are registered as service groups and each node
    ...    materialises only the cluster placed on it.
    [Tags]    smb    multi-node
    Run In Head Node    microceph.ceph orch ls --service_type smb | grep -F 'smb.${SMB_SINGLE_CLUSTER}'    30
    Run In Head Node    microceph.ceph orch ls --service_type smb | grep -F 'smb.${SMB_DUAL_CLUSTER}'    30
    Run In Container And Check    node-wrk0    grep -Fx '${SMB_SINGLE_CLUSTER}' /var/snap/microceph/current/samba/cluster-id    30
    Run In Container And Check    node-wrk0    grep -E '"vfs objects": ".*ceph_new' /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk0    grep -F '"ceph_new:proxy": "no"' /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk1    grep -Fx '${SMB_DUAL_CLUSTER}' /var/snap/microceph/current/samba/cluster-id    30
    Run In Container And Check    node-wrk2    grep -Fx '${SMB_DUAL_CLUSTER}' /var/snap/microceph/current/samba/cluster-id    30
    Run In Container And Check    node-wrk1    grep -E '"vfs objects": ".*ceph_new' /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk2    grep -F '"ceph_new:proxy": "no"' /var/snap/microceph/current/samba/container.json    30

Test Both SMB Clusters Serve The Shared CephFS Subvolume
    [Documentation]    Writes through the one-host cluster and reads the same data
    ...    back through both members of the two-host cluster.
    [Tags]    smb    cephfs    multi-node
    ${ip_single}=    Get Node IP    node-wrk0
    ${ip_dual0}=    Get Node IP    node-wrk1
    ${ip_dual1}=    Get Node IP    node-wrk2
    Run In VM And Check    printf 'multi cluster smb test\n' > /tmp/smb-mn-source    30
    Run In VM And Check    smbclient //${ip_single}/${SMB_SHARE} -U '${SMB_SINGLE_USER}%${SMB_SINGLE_PASSWORD}' -c 'put /tmp/smb-mn-source smb-mn-file'    60
    Run In VM And Check    smbclient //${ip_dual0}/${SMB_SHARE} -U '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' -c 'get smb-mn-file /tmp/smb-mn-result0'    60
    Run In VM And Check    smbclient //${ip_dual1}/${SMB_SHARE} -U '${SMB_DUAL_USER}%${SMB_DUAL_PASSWORD}' -c 'get smb-mn-file /tmp/smb-mn-result1'    60
    Run In VM And Check    cmp /tmp/smb-mn-source /tmp/smb-mn-result0    30
    Run In VM And Check    cmp /tmp/smb-mn-source /tmp/smb-mn-result1    30

Test Third SMB Cluster Rejected On An Occupied Host
    [Documentation]    A third cluster targeting node-wrk0 (already serving
    ...    ${SMB_SINGLE_CLUSTER}) is rejected even though other hosts serve a
    ...    different cluster: the invariant is per host, not per deployment.
    [Tags]    smb    multi-node
    ${result}=    Run In Container Unchecked    node-wrk0    echo c2VydmljZV90eXBlOiBzbWIKc2VydmljZV9pZDogc21iLXRoaXJkCmNsdXN0ZXJfaWQ6IHNtYi10aGlyZApjb25maWdfdXJpOiByYWRvczovLy5zbWIvc21iLXRoaXJkL2NvbmZpZy5zbWIKcGxhY2VtZW50OgogIGhvc3RzOgogICAgLSBub2RlLXdyazAKICBjb3VudDogMQo= | base64 --decode | microceph.ceph orch apply -i -    120
    Should Not Be Equal As Integers    ${result.rc}    0    msg=Third SMB cluster on an occupied host unexpectedly succeeded
    ${error}=    Catenate    SEPARATOR=\n    ${result.stdout}    ${result.stderr}
    Should Contain    ${error}    at most one SMB cluster

Test Remove Both SMB Clusters
    [Documentation]    Removing both clusters stops smbd on every host and clears
    ...    node-local configuration.
    [Tags]    smb    multi-node
    Run In Head Node    microceph.ceph smb cluster rm ${SMB_SINGLE_CLUSTER}    120
    Run In Head Node    microceph.ceph smb cluster rm ${SMB_DUAL_CLUSTER}    120
    Wait For SMB Service    disabled    inactive    node=node-wrk0
    Wait For SMB Service    disabled    inactive    node=node-wrk1
    Wait For SMB Service    disabled    inactive    node=node-wrk2
    Run In Container And Check    node-wrk0    test ! -e /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk1    test ! -e /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk2    test ! -e /var/snap/microceph/current/samba/container.json    30
