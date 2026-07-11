*** Settings ***
Documentation    smb-tests
...    Tests MicroCeph native SMB in a multi-node LXD cluster: creates an
...    smb cluster through mgr/smb (ceph smb CLI, microceph orchestrator
...    backend), verifies a share roundtrip via a CTDB public address, kills
...    the VIP holder to exercise failover, rejoins it, then removes the
...    cluster.
Resource        ../resources/microceph_harness.resource
Suite Setup     SMB Multinode Suite Setup
Suite Teardown  Teardown MicroCeph Environment
Test Tags       multi-node    smb    cephfs    lxd    slow    integration

*** Variables ***
${SMB_CLUSTER}      dev
${SMB_SHARE}        share1
${SMB_VOLUME}       smbfs
${SMB_SUBVOLUME}    s1
${SMB_USER}         smbuser
${SMB_PASSWORD}     s3cr3tpass

*** Keywords ***
SMB Multinode Suite Setup
    Provision Multinode VM    microceph-smb-vm    ${OUTER_VM_DISK}    public
    Bootstrap Head Node    public
    Join Worker Nodes To Cluster    public
    Add OSD To Node    node-wrk0
    Add OSD To Node    node-wrk1
    Add OSD To Node    node-wrk2
    Wait For OSD Count Head    3
    # Confined smbd panics on setgroups until the smb-support snapd
    # interface lands; the smb suite runs the snap in devmode.
    Reinstall Snap Devmode On All Nodes
    Wait For Cluster Health OK    node-wrk0

Enable Microceph Orchestrator
    [Documentation]    Points mgr/smb at the microceph orchestrator backend.
    Run In Head Node And Check    microceph.ceph mgr module enable smb
    Run In Head Node And Check    microceph.ceph mgr module enable microceph
    Run In Head Node And Check    microceph.ceph orch set backend microceph
    ${result}=    Run In Head Node    microceph.ceph orch status    60
    Should Contain    ${result.stdout}    Available: Yes

Run In Head Node And Check
    [Documentation]    Runs a command on the head container and asserts rc 0.
    [Arguments]    ${cmd}    ${timeout}=120
    ${result}=    Run In Head Node    ${cmd}    ${timeout}
    Should Be Equal As Integers    ${result.rc}    0    msg=${cmd} failed: ${result.stderr}

Provision SMB Backing Volume
    [Documentation]    Creates the CephFS volume and a world-writable subvolume
    ...    (share write permissions are the admin's task in Phase 1).
    Run In Head Node And Check    microceph.ceph fs volume create ${SMB_VOLUME}    180
    Run In Head Node And Check    microceph.ceph fs subvolume create ${SMB_VOLUME} ${SMB_SUBVOLUME} --mode 0777    60

Provision SMB System Users
    [Documentation]    Creates the matching unix user on every node: the passdb
    ...    is CTDB-replicated but each smbd maps sessions via local NSS.
    FOR    ${container}    IN    node-wrk0    node-wrk1    node-wrk2
        Run In Container And Check    ${container}    id ${SMB_USER} >/dev/null 2>&1 || useradd -M -s /usr/sbin/nologin ${SMB_USER}    30
    END

Create SMB Cluster Via Mgr
    [Documentation]    Creates the CTDB-clustered smb cluster through ceph smb,
    ...    with public addresses computed from the cluster network.
    ${cidr}=    Get Public Network Cidr
    ${vips}=    Smb Vip Addresses    ${cidr}    ${3}
    Set Suite Variable    ${SMB_VIPS}    ${vips}
    ${addr_flags}=    Evaluate    " ".join(f"--public-addrs={a}" for a in $vips)
    Run In Head Node And Check
    ...    microceph.ceph smb cluster create ${SMB_CLUSTER} user --define-user-pass=${SMB_USER}%${SMB_PASSWORD} --placement=count:3 --clustering=always ${addr_flags}
    ...    900

Create SMB Share Via Mgr
    [Documentation]    Applies the share declaratively: the imperative create
    ...    defaults to the proxied vfs provider, which microceph rejects.
    ${yaml}=    Smb Share Spec Yaml    ${SMB_CLUSTER}    ${SMB_SHARE}    ${SMB_VOLUME}    ${SMB_SUBVOLUME}
    # No stdin plumbing through the nested lxc exec: ship the document base64ed.
    ${b64}=    Evaluate    base64.b64encode($yaml.encode()).decode()    modules=base64
    Run In Container And Check    node-wrk0    printf '%s' ${b64} | base64 -d > /root/${SMB_SHARE}.yaml    30
    Run In Head Node And Check    microceph.ceph smb apply -i /root/${SMB_SHARE}.yaml    900

SMB Roundtrip Via Address
    [Documentation]    put + get via smbclient from the outer VM and compares content.
    [Arguments]    ${address}
    ${ip}=    Evaluate    $address.split("/")[0]
    Run In VM And Check    echo "smb roundtrip $(date -u)" > /tmp/smb-rt.txt    10
    Run In VM And Check    smbclient //${ip}/${SMB_SHARE} -U ${SMB_USER}%${SMB_PASSWORD} -c "put /tmp/smb-rt.txt rt.txt"    120
    Run In VM And Check    smbclient //${ip}/${SMB_SHARE} -U ${SMB_USER}%${SMB_PASSWORD} -c "get rt.txt /tmp/smb-rt-back.txt"    120
    Run In VM And Check    diff /tmp/smb-rt.txt /tmp/smb-rt-back.txt    10
    Run In VM And Check    rm -f /tmp/smb-rt.txt /tmp/smb-rt-back.txt    10

*** Test Cases ***
Test Enable Microceph Orchestrator Backend
    [Documentation]    Enables mgr/smb plus the microceph orchestrator module.
    [Tags]    smb    multi-node
    Enable Microceph Orchestrator

Test Create SMB Cluster And Share
    [Documentation]    Provisions the backing volume and creates cluster+share via mgr.
    [Tags]    smb    multi-node
    Provision SMB Backing Volume
    Provision SMB System Users
    Create SMB Cluster Via Mgr
    Create SMB Share Via Mgr
    Wait For Ctdb Healthy    node-wrk0    3

Test SMB Roundtrip Via VIP
    [Documentation]    Writes and reads back a file through the first CTDB VIP.
    [Tags]    smb    multi-node
    Run In VM And Check    sudo apt-get install -y smbclient    300
    SMB Roundtrip Via Address    ${SMB_VIPS}[0]

Test SMB VIP Failover And Rejoin
    [Documentation]    Force-stops the node holding the first VIP, verifies the
    ...    share recovers on the same address, then rejoins the node.
    [Tags]    smb    multi-node    slow
    ${ip_table}=    Get Ctdb Vip Output    node-wrk0
    ${pnn}=    Ctdb Vip Pnn    ${ip_table}    ${SMB_VIPS}[0]
    Should Be True    ${pnn} >= 0    msg=VIP ${SMB_VIPS}[0] is not assigned
    # CTDB pnn N is line N of the nodes file, which follows join order.
    ${holder}=    Set Variable    node-wrk${pnn}
    ${observer}=    Set Variable IF    "${holder}" == "node-wrk0"    node-wrk1    node-wrk0
    Run In VM And Check    lxc stop --force ${holder}    120
    Wait Until Keyword Succeeds    180s    10s    SMB Roundtrip Via Address    ${SMB_VIPS}[0]
    Run In VM And Check    lxc start ${holder}    120
    Wait For Ctdb Healthy    ${observer}    3    attempts=45

Test Remove SMB Cluster
    [Documentation]    Removes share and cluster through mgr and verifies teardown.
    [Tags]    smb    multi-node
    Run In Head Node And Check    microceph.ceph smb share rm ${SMB_CLUSTER} ${SMB_SHARE}    300
    Run In Head Node And Check    microceph.ceph smb cluster rm ${SMB_CLUSTER}    900
    ${result}=    Run In Head Node    microceph.ceph smb show    60
    Should Not Contain    ${result.stdout}    ceph.smb.cluster
    ${services}=    Run In Container Unchecked    node-wrk0    snap services microceph    30
    ${active}=    Enabled Active Services    ${services.stdout}
    Should Not Contain    ${active}    microceph.ctdbd
