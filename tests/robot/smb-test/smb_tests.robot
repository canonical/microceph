*** Settings ***
Documentation    smb-test
...    Verifies the complete strict-confinement native SMB lifecycle on one node:
...    placement, direct samba-vfs ceph_new access, authentication, validation,
...    share removal, service shutdown, and local-state cleanup.
Resource        ../resources/microceph_harness.resource
Suite Setup     SMB Tests Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       single-node    smb    cephfs    lxd    integration    strict-confinement

*** Variables ***
${OUTER_VM_IMAGE}       ubuntu:26.04
${SMB_CLUSTER}          smbtest
${SMB_SHARE}            cephfs
${SMB_OTHER_SHARE}      auxiliary
${SMB_VOLUME}           smbfs
${SMB_SUBVOLUME}        smbshare
${SMB_USERNAME}         smbuser
${SMB_PASSWORD}         SmbTestPassword1

*** Keywords ***
SMB Tests Suite Setup
    Launch Outer Test VM    vm_name=microceph-smb-vm
    Verify Resolute Outer VM
    Copy Snap To VM
    Install And Bootstrap MicroCeph
    Run In VM And Check    sudo microceph disk add loop,1G,3    120
    Wait For OSD Count    3
    Wait For Cluster Health OK
    Run In VM And Check    sudo microceph.ceph fs volume create ${SMB_VOLUME}    120
    Run In VM And Check    sudo microceph.ceph fs subvolume create ${SMB_VOLUME} ${SMB_SUBVOLUME} --mode 777    120
    Wait For Cluster Health OK
    Run In VM And Check    sudo microceph.ceph mgr module enable microceph    30
    Run In VM And Check    sudo microceph.ceph orch set backend microceph    30
    Run In VM And Check    sudo microceph.ceph mgr module enable smb    30
    Run In VM And Check    sudo snap connect microceph:smb-identity    30
    Run In VM And Check    sudo snap connect microceph:ctdb-run    30
    Run In VM And Check    snap connections microceph | grep -F 'microceph:smb-identity' | grep -v -- ' -$'    30
    Run In VM And Check    sudo grep -Fx 'capability setuid,' /var/lib/snapd/apparmor/profiles/snap.microceph.smbd    30
    Run In VM And Check    sudo grep -Fx 'capability setgid,' /var/lib/snapd/apparmor/profiles/snap.microceph.smbd    30
    Run In VM And Check    sudo grep -Fx setgroups /var/lib/snapd/seccomp/bpf/snap.microceph.smbd.src    30
    Run In VM Must Fail    sudo grep -F 'capability setuid,' /var/lib/snapd/apparmor/profiles/snap.microceph.daemon    30
    Run In VM And Check    sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq    120
    Run In VM And Check    sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y smbclient    300

Create Native SMB Cluster And Share
    # Disposable fixtures only; credentials are passed to MicroCeph through stdin.
    Run In VM And Check    printf '%s' '{"users":[{"name":"${SMB_USERNAME}","password":"${SMB_PASSWORD}"}]}' | sudo microceph enable smb --cluster-id ${SMB_CLUSTER} --credentials-file -    120
    Run In VM And Check    test ! -e /var/snap/microceph/current/samba/cluster-id    30
    Run In VM And Check    printf '%s' '{"resource_type":"ceph.smb.share","cluster_id":"${SMB_CLUSTER}","share_id":"${SMB_SHARE}","cephfs":{"volume":"${SMB_VOLUME}","subvolume":"${SMB_SUBVOLUME}","provider":"samba-vfs/new"}}' | sudo microceph.ceph smb apply -i -    120
    Wait For CTDB Service    enabled    active
    Wait For CTDB Nodes Service    enabled    active
    Wait For SMB Service    enabled    active
    Run In VM And Check Eventually    sudo microceph.ctdb pnn    5    2    15

Verify Native SMB Placement And Direct Configuration
    Run In VM And Check    sudo microceph.ceph orch ls --service_type smb | grep -F 'smb.${SMB_CLUSTER}'    30
    Run In VM And Check    test -f /var/snap/microceph/current/samba/container.json    30
    Run In VM And Check    test -s /var/snap/microceph/current/samba/ctdb.json    30
    Run In VM And Check    grep -E '"vfs objects": ".*ceph_new' /var/snap/microceph/current/samba/container.json    30
    Run In VM And Check    grep -F '"ceph_new:proxy": "no"' /var/snap/microceph/current/samba/container.json    30
    Run In VM Must Fail    grep -F '"ceph_new:proxy": "yes"' /var/snap/microceph/current/samba/container.json    30

Verify Native SMB Authentication And IO
    ${ip}=    Get VM IP
    Run In VM And Check    smbclient //${ip}/${SMB_SHARE} -U '${SMB_USERNAME}%${SMB_PASSWORD}' -c 'ls'    60
    Run In VM And Check    printf 'native SMB CephFS test\n' > /tmp/smb-e2e-source    30
    Run In VM And Check    smbclient //${ip}/${SMB_SHARE} -U '${SMB_USERNAME}%${SMB_PASSWORD}' -c 'put /tmp/smb-e2e-source smb-e2e-file; get smb-e2e-file /tmp/smb-e2e-result'    60
    Run In VM And Check    cmp /tmp/smb-e2e-source /tmp/smb-e2e-result    30

Verify Share Update With Unchanged Cluster Config URI
    ${ip}=    Get VM IP
    Run In VM And Check    printf '%s' '{"resource_type":"ceph.smb.share","cluster_id":"${SMB_CLUSTER}","share_id":"${SMB_OTHER_SHARE}","cephfs":{"volume":"${SMB_VOLUME}","subvolume":"${SMB_SUBVOLUME}","provider":"samba-vfs/new"}}' | sudo microceph.ceph smb apply -i -    120
    Run In VM And Check Eventually    smbclient //${ip}/${SMB_OTHER_SHARE} -U '${SMB_USERNAME}%${SMB_PASSWORD}' -c 'get smb-e2e-file /tmp/smb-other-result'    5    3    60
    Run In VM And Check    cmp /tmp/smb-e2e-source /tmp/smb-other-result    30
    Run In VM And Check    printf '%s' '{"resource_type":"ceph.smb.share","cluster_id":"${SMB_CLUSTER}","share_id":"${SMB_OTHER_SHARE}","name":"auxiliary-renamed","cephfs":{"volume":"${SMB_VOLUME}","subvolume":"${SMB_SUBVOLUME}","provider":"samba-vfs/new"}}' | sudo microceph.ceph smb apply -i -    120
    Run In VM And Check Eventually    smbclient //${ip}/auxiliary-renamed -U '${SMB_USERNAME}%${SMB_PASSWORD}' -c 'get smb-e2e-file /tmp/smb-renamed-result'    5    3    60
    Run In VM And Check    cmp /tmp/smb-e2e-source /tmp/smb-renamed-result    30
    Run In VM Must Fail    timeout 30s smbclient //${ip}/${SMB_OTHER_SHARE} -U '${SMB_USERNAME}%${SMB_PASSWORD}' -c 'ls'    45
    Run In VM And Check    smbclient //${ip}/${SMB_SHARE} -U '${SMB_USERNAME}%${SMB_PASSWORD}' -c 'get smb-e2e-file /tmp/smb-still-present'    60
    Run In VM And Check    cmp /tmp/smb-e2e-source /tmp/smb-still-present    30

Verify Unsupported And Overlapping Placements Are Rejected
    ${proxy_result}=    Run In VM    echo c2VydmljZV90eXBlOiBzbWIKc2VydmljZV9pZDogc21iLXByb3h5CmNsdXN0ZXJfaWQ6IHNtYi1wcm94eQpjb25maWdfdXJpOiByYWRvczovLy5zbWIvc21iLXByb3h5L2NvbmZpZy5zbWIKZmVhdHVyZXM6CiAgLSBjZXBoZnMtcHJveHkKcGxhY2VtZW50OgogIGhvc3RzOgogIC0gbWljcm9jZXBoLXNtYi12bQogIGNvdW50OiAxCg== | base64 --decode | sudo microceph.ceph orch apply -i -    120
    Should Not Be Equal As Integers    ${proxy_result.rc}    0    msg=Proxied SMBSpec unexpectedly succeeded
    ${proxy_error}=    Catenate    SEPARATOR=\n    ${proxy_result.stdout}    ${proxy_result.stderr}
    Should Contain    ${proxy_error}    native SMB does not support SMB features
    ${unclustered_result}=    Run In VM    echo c2VydmljZV90eXBlOiBzbWIKc2VydmljZV9pZDogc21iLXNlY29uZApjbHVzdGVyX2lkOiBzbWItc2Vjb25kCmNvbmZpZ191cmk6IHJhZG9zOi8vLnNtYi9zbWItc2Vjb25kL2NvbmZpZy5zbWIKcGxhY2VtZW50OgogIGhvc3RzOgogIC0gbWljcm9jZXBoLXNtYi12bQogIGNvdW50OiAxCg== | base64 --decode | sudo microceph.ceph orch apply -i -    120
    Should Not Be Equal As Integers    ${unclustered_result.rc}    0    msg=Overlapping non-clustered SMBSpec unexpectedly succeeded
    ${unclustered_error}=    Catenate    SEPARATOR=\n    ${unclustered_result.stdout}    ${unclustered_result.stderr}
    Should Contain    ${unclustered_error}    at most one SMB cluster
    ${overlap_result}=    Run In VM    echo c2VydmljZV90eXBlOiBzbWIKc2VydmljZV9pZDogc21iLXNlY29uZApjbHVzdGVyX2lkOiBzbWItc2Vjb25kCmNvbmZpZ191cmk6IHJhZG9zOi8vLnNtYi9zbWItc2Vjb25kL2NvbmZpZy5zbWIKZmVhdHVyZXM6CiAgLSBjbHVzdGVyZWQKY2x1c3Rlcl9tZXRhX3VyaTogcmFkb3M6Ly8uc21iL3NtYi1zZWNvbmQvY2x1c3Rlci5tZXRhLmpzb24KY2x1c3Rlcl9sb2NrX3VyaTogcmFkb3M6Ly8uc21iL3NtYi1zZWNvbmQvY2x1c3Rlci5tZXRhLmxvY2sKcGxhY2VtZW50OgogIGhvc3RzOgogICAgLSBtaWNyb2NlcGgtc21iLXZtCiAgY291bnQ6IDEK | base64 --decode | sudo microceph.ceph orch apply -i -    120
    Should Not Be Equal As Integers    ${overlap_result.rc}    0    msg=Second native SMB cluster on an occupied host unexpectedly succeeded
    ${overlap_error}=    Catenate    SEPARATOR=\n    ${overlap_result.stdout}    ${overlap_result.stderr}
    Should Contain    ${overlap_error}    at most one SMB cluster

Verify Native SMB Share And Cluster Removal
    ${ip}=    Get VM IP
    Run In VM And Check    sudo microceph.ceph smb share rm ${SMB_CLUSTER} ${SMB_OTHER_SHARE}    120
    Run In VM And Check    sudo microceph.ceph smb share rm ${SMB_CLUSTER} ${SMB_SHARE}    120
    Wait For SMB Service    enabled    active
    Run In VM Must Fail    timeout 30s smbclient //${ip}/${SMB_SHARE} -U '${SMB_USERNAME}%${SMB_PASSWORD}' -c 'ls'    45
    Run In VM And Check    sudo microceph.ceph smb cluster rm ${SMB_CLUSTER}    120
    Wait For SMB Service    disabled    inactive
    Wait For CTDB Service    disabled    inactive
    Wait For CTDB Nodes Service    disabled    inactive
    Run In VM And Check    test ! -e /var/snap/microceph/current/samba/container.json    30

Verify Nonclustered Managed Lifecycle
    # A different cluster must not reuse a subvolume earmarked for smbtest.
    Run In VM And Check    sudo microceph.ceph fs subvolume create ${SMB_VOLUME} ${SMB_SUBVOLUME}-standalone --mode 777    120
    Run In VM And Check    printf '%s' '{"users":[{"name":"${SMB_USERNAME}","password":"${SMB_PASSWORD}"}]}' | sudo microceph enable smb --cluster-id smbstandalone --clustering never --credentials-file -    120
    Run In VM And Check    printf '%s' '{"resource_type":"ceph.smb.share","cluster_id":"smbstandalone","share_id":"${SMB_SHARE}","cephfs":{"volume":"${SMB_VOLUME}","subvolume":"${SMB_SUBVOLUME}-standalone","provider":"samba-vfs/new"}}' | sudo microceph.ceph smb apply -i -    180
    Wait For SMB Service    enabled    active
    Wait For CTDB Service    disabled    inactive
    Wait For CTDB Nodes Service    disabled    inactive
    Run In VM And Check    test ! -e /var/snap/microceph/current/samba/ctdb.json    30
    ${ip}=    Get VM IP
    Run In VM And Check    smbclient //${ip}/${SMB_SHARE} -U '${SMB_USERNAME}%${SMB_PASSWORD}' -c 'put /tmp/smb-e2e-source smb-e2e-file; get smb-e2e-file /tmp/smb-standalone-result'    60
    Run In VM And Check    cmp /tmp/smb-e2e-source /tmp/smb-standalone-result    30
    # Omission must preserve never; an explicit transition must fail before mutation.
    Run In VM And Check    sudo microceph enable smb --cluster-id smbstandalone --port 1445    180
    Run In VM Must Fail    sudo microceph enable smb --cluster-id smbstandalone --clustering always    30
    Run In VM And Check    smbclient -p 1445 //${ip}/${SMB_SHARE} -U '${SMB_USERNAME}%${SMB_PASSWORD}' -c 'ls'    60
    Run In VM And Check    sudo microceph.ceph smb share rm smbstandalone ${SMB_SHARE}    120
    Run In VM And Check    sudo microceph disable smb --cluster-id smbstandalone    180
    Wait For SMB Service    disabled    inactive

*** Test Cases ***
Test Complete Native SMB Single Node Scenario
    [Documentation]    Exercises the complete supported single-node SMB lifecycle as one independent scenario.
    Create Native SMB Cluster And Share
    Verify Native SMB Placement And Direct Configuration
    Verify Native SMB Authentication And IO
    Verify Share Update With Unchanged Cluster Config URI
    Verify Unsupported And Overlapping Placements Are Rejected
    Verify Native SMB Share And Cluster Removal
    Verify Nonclustered Managed Lifecycle
