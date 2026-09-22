*** Settings ***
Documentation    smb-multinode-test
...    Verifies one managed SMB cluster with an smbd and CTDB instance on each
...    member of a three-node MicroCeph cluster, including member loss, surviving
...    node I/O, recovery, placement scale-down, and complete cleanup.
Resource        ../resources/microceph_harness.resource
Suite Setup     SMB Multinode Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    smb    ctdb    failover    cephfs    lxd    slow    integration    strict-confinement

*** Variables ***
${SMB_VOLUME}       smbsfs
${SMB_SUBVOLUME}    smbdata
${SMB_CLUSTER}      smbcluster
${SMB_USER}         smbuser
${SMB_PASSWORD}     SmbClusterPassword1
${SMB_SHARE}        cephfs

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
    Run In Head Node    microceph.ceph fs subvolume create ${SMB_VOLUME} ${SMB_SUBVOLUME} --mode 777    120
    Wait For Cluster Health OK    node=node-wrk0
    FOR    ${node}    IN    node-wrk0    node-wrk1    node-wrk2
        Run In Container And Check    ${node}    snap connect microceph:smb-identity    30
        Run In Container And Check    ${node}    snap connect microceph:ctdb-run    30
    END
    Run In VM And Check    sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq    120
    Run In VM And Check    sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y smbclient    300

Enable Three Managed SMB Instances
    Run In Head Node    microceph enable smb --cluster-id ${SMB_CLUSTER} --target node-wrk0 --define-user-pass '${SMB_USER}%${SMB_PASSWORD}'    180
    Run In Head Node    microceph enable smb --cluster-id ${SMB_CLUSTER} --target node-wrk1    180
    Run In Head Node    microceph enable smb --cluster-id ${SMB_CLUSTER} --target node-wrk2    180
    Run In Head Node    echo cmVzb3VyY2VfdHlwZTogY2VwaC5zbWIuc2hhcmUKY2x1c3Rlcl9pZDogc21iY2x1c3RlcgpzaGFyZV9pZDogY2VwaGZzCmNlcGhmczoKICB2b2x1bWU6IHNtYnNmcwogIHN1YnZvbHVtZTogc21iZGF0YQogIHByb3ZpZGVyOiBzYW1iYS12ZnMvbmV3Cg== | base64 --decode | microceph.ceph smb apply -i -    120
    FOR    ${node}    IN    node-wrk0    node-wrk1    node-wrk2
        Wait For SMB Service    enabled    active    node=${node}
        Wait For CTDB Service    enabled    active    node=${node}
        Wait For CTDB Nodes Service    enabled    active    node=${node}
        Wait For CTDB Healthy Nodes    3    3    node=${node}
    END

Verify Three Instance Placement And Addresses
    Run In Head Node    microceph.ceph orch ls --service_type smb | grep -F 'smb.${SMB_CLUSTER}'    30
    FOR    ${node}    IN    node-wrk0    node-wrk1    node-wrk2
        Run In Container And Check    ${node}    grep -Fx '${SMB_CLUSTER}' /var/snap/microceph/current/samba/cluster-id    30
        Run In Container And Check    ${node}    grep -E '"vfs objects": ".*ceph_new' /var/snap/microceph/current/samba/container.json    30
        Run In Container And Check    ${node}    grep -F '"ceph_new:proxy": "no"' /var/snap/microceph/current/samba/container.json    30
        Run In Container And Check    ${node}    test -s /var/snap/microceph/current/samba/ctdb-address    30
        Run In Container And Check    ${node}    grep -F 'bind interfaces only = yes' /var/snap/microceph/current/conf/samba/smb.conf    30
    END

Verify IO Through Every SMB Instance
    ${ip0}=    Get Node IP    node-wrk0
    ${ip1}=    Get Node IP    node-wrk1
    ${ip2}=    Get Node IP    node-wrk2
    Run In VM And Check    printf 'three instance smb test\n' > /tmp/smb-source    30
    Run In VM And Check    smbclient //${ip0}/${SMB_SHARE} -U '${SMB_USER}%${SMB_PASSWORD}' -c 'put /tmp/smb-source smb-file'    60
    Run In VM And Check    smbclient //${ip1}/${SMB_SHARE} -U '${SMB_USER}%${SMB_PASSWORD}' -c 'get smb-file /tmp/smb-result1'    60
    Run In VM And Check    smbclient //${ip2}/${SMB_SHARE} -U '${SMB_USER}%${SMB_PASSWORD}' -c 'get smb-file /tmp/smb-result2'    60
    Run In VM And Check    cmp /tmp/smb-source /tmp/smb-result1    30
    Run In VM And Check    cmp /tmp/smb-source /tmp/smb-result2    30

Verify CTDB Member Loss And Recovery
    ${ip0}=    Get Node IP    node-wrk0
    ${ip2}=    Get Node IP    node-wrk2
    Run In VM And Check    lxc stop node-wrk1 --force    60
    Wait For CTDB Healthy Nodes    3    2    node=node-wrk0
    Wait For CTDB Healthy Nodes    3    2    node=node-wrk2
    Run In VM And Check    smbclient //${ip2}/${SMB_SHARE} -U '${SMB_USER}%${SMB_PASSWORD}' -c 'get smb-file /tmp/smb-after-failure'    60
    Run In VM And Check    cmp /tmp/smb-source /tmp/smb-after-failure    30
    Run In VM And Check    printf 'written during member loss\n' > /tmp/smb-failover-source    30
    Run In VM And Check    smbclient //${ip0}/${SMB_SHARE} -U '${SMB_USER}%${SMB_PASSWORD}' -c 'put /tmp/smb-failover-source smb-failover-file'    60
    Run In VM And Check    lxc start node-wrk1    60
    Wait For SMB Service    enabled    active    node=node-wrk1
    Wait For CTDB Service    enabled    active    node=node-wrk1
    Wait For CTDB Nodes Service    enabled    active    node=node-wrk1
    FOR    ${node}    IN    node-wrk0    node-wrk1    node-wrk2
        Wait For CTDB Healthy Nodes    3    3    node=${node}
    END
    ${ip1}=    Get Node IP    node-wrk1
    Run In VM And Check    smbclient //${ip1}/${SMB_SHARE} -U '${SMB_USER}%${SMB_PASSWORD}' -c 'get smb-failover-file /tmp/smb-failover-result'    60
    Run In VM And Check    cmp /tmp/smb-failover-source /tmp/smb-failover-result    30

Scale Down And Remove Managed SMB Cluster
    ${ip0}=    Get Node IP    node-wrk0
    Run In Head Node    microceph disable smb --cluster-id ${SMB_CLUSTER} --target node-wrk2    180
    Wait For SMB Service    disabled    inactive    node=node-wrk2
    Wait For CTDB Service    disabled    inactive    node=node-wrk2
    Wait For CTDB Nodes Service    disabled    inactive    node=node-wrk2
    Wait For CTDB Healthy Nodes    2    2    node=node-wrk0
    Wait For CTDB Healthy Nodes    2    2    node=node-wrk1
    Run In VM And Check    smbclient //${ip0}/${SMB_SHARE} -U '${SMB_USER}%${SMB_PASSWORD}' -c 'get smb-failover-file /tmp/smb-after-scale-down'    60
    Run In VM And Check    cmp /tmp/smb-failover-source /tmp/smb-after-scale-down    30
    Run In Head Node    microceph disable smb --cluster-id ${SMB_CLUSTER} --target node-wrk1    180
    Wait For SMB Service    disabled    inactive    node=node-wrk1
    Wait For CTDB Service    disabled    inactive    node=node-wrk1
    Wait For CTDB Nodes Service    disabled    inactive    node=node-wrk1
    Wait For SMB Service    enabled    active    node=node-wrk0
    Wait For CTDB Service    disabled    inactive    node=node-wrk0
    Wait For CTDB Nodes Service    disabled    inactive    node=node-wrk0
    Run In Head Node    microceph.ceph smb share rm ${SMB_CLUSTER} ${SMB_SHARE}    120
    Run In Head Node    microceph disable smb --cluster-id ${SMB_CLUSTER} --target node-wrk0    180
    Wait For SMB Service    disabled    inactive    node=node-wrk0
    Run In Container And Check    node-wrk0    test ! -e /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk1    test ! -e /var/snap/microceph/current/samba/container.json    30
    Run In Container And Check    node-wrk2    test ! -e /var/snap/microceph/current/samba/container.json    30

*** Test Cases ***
Test Complete Three Node Managed SMB Failover Scenario
    [Documentation]    Exercises one three-instance SMB cluster, member loss and recovery, scale-down, and cleanup.
    Enable Three Managed SMB Instances
    Verify Three Instance Placement And Addresses
    Verify IO Through Every SMB Instance
    Verify CTDB Member Loss And Recovery
    Scale Down And Remove Managed SMB Cluster
