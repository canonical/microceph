*** Settings ***
Documentation    Shared resource for MicroCeph Robot Framework tests.
...    All test commands run directly inside the outer LXD VM (no actionutils.sh dependency).
Library         OperatingSystem
Library         Process
Library         String
Library         Collections

*** Variables ***
${SNAP_PATH}          ${EMPTY}
${OUTER_VM}           microceph-test-vm
${OUTER_VM_DISK}      50GiB
${REPO_ROOT}          ${CURDIR}/../../..

*** Keywords ***

# ---------------------------------------------------------------------------
# Host dependency checking
# ---------------------------------------------------------------------------

Require Host Commands
    [Documentation]    Fails immediately if any listed command is absent from the host PATH.
    ...    Call this in Suite Setup for tests that run directly on the host runner.
    ...    For VM-based tests, lxc is checked automatically inside Launch Outer Test VM.
    [Arguments]    @{commands}
    FOR    ${cmd}    IN    @{commands}
        ${result}=    Run Process    bash -c "command -v '${cmd}' >/dev/null 2>&1"    shell=True
        IF    ${result.rc} != 0
            Fail    Missing host dependency: '${cmd}' not found in PATH. Install it before running this suite.
        END
    END

# ---------------------------------------------------------------------------
# Core execution helpers
# ---------------------------------------------------------------------------

Run In VM
    [Documentation]    Runs an arbitrary bash command inside the outer VM (no fail on non-zero).
    ...    bash -eo pipefail: pipe failures and early command failures propagate to the exit code,
    ...    mirroring the set -e behaviour of the original bash CI steps.
    [Arguments]    ${bash_cmd}    ${timeout}=300
    ${result}=    Run Process    lxc    exec    ${OUTER_VM}    --    bash    -eo    pipefail    -c    ${bash_cmd}
    ...    timeout=${timeout}
    Log    VM cmd rc=${result.rc}: ${result.stdout}
    Log    STDERR: ${result.stderr}
    RETURN    ${result}

Run In VM And Check
    [Documentation]    Runs a bash command inside the outer VM and fails on non-zero rc.
    [Arguments]    ${bash_cmd}    ${timeout}=300
    ${result}=    Run In VM    ${bash_cmd}    ${timeout}
    Should Be Equal As Integers    ${result.rc}    0
    ...    msg=Command failed (rc=${result.rc}):\nSTDERR: ${result.stderr}\nSTDOUT: ${result.stdout}
    RETURN    ${result}

Run In VM Must Fail
    [Documentation]    Runs a bash command inside the outer VM and fails if it SUCCEEDS (expects non-zero).
    [Arguments]    ${bash_cmd}    ${timeout}=120
    ${result}=    Run In VM    ${bash_cmd}    ${timeout}
    Should Not Be Equal As Integers    ${result.rc}    0
    ...    msg=Expected failure but command succeeded: ${bash_cmd}
    RETURN    ${result}

Run In Container
    [Documentation]    Runs cmd inside an inner LXD container via the outer VM.
    ...    ${cmd} is written to a temp file by the local runner using Python file I/O
    ...    and pushed into the container with lxc file push, so it is never interpreted
    ...    by any intermediate shell regardless of what characters it contains.
    ...    bash -eo pipefail: mirrors set -e semantics so any failing command or pipe stage
    ...    inside the container fails the keyword immediately.
    [Arguments]    ${container}    ${cmd}    ${timeout}=300
    Log To Console    [${container}] ${cmd[:80]}
    ${tmpname}=    Evaluate    'rf_cmd_' + __import__('uuid').uuid4().hex[:8] + '.sh'
    ${localpath}=    Set Variable    /tmp/${tmpname}
    ${remotepath}=    Set Variable    /tmp/${tmpname}
    Create File    ${localpath}    ${cmd}
    ${push_vm}=    Run Process    lxc    file    push    ${localpath}    ${OUTER_VM}${remotepath}    timeout=30
    Should Be Equal As Integers    ${push_vm.rc}    0    msg=Failed to push script to outer VM: ${push_vm.stderr}
    ${push_ct}=    Run Process    lxc    exec    ${OUTER_VM}    --    lxc    file    push    ${remotepath}    ${container}${remotepath}    timeout=30
    Should Be Equal As Integers    ${push_ct.rc}    0    msg=Failed to push script to ${container}: ${push_ct.stderr}
    ${result}=    Run Process    lxc    exec    ${OUTER_VM}    --    lxc    exec    ${container}    --    bash    -eo    pipefail    ${remotepath}
    ...    timeout=${timeout}
    Log    Container cmd rc=${result.rc}: ${result.stdout}
    Log    STDERR: ${result.stderr}
    Run Keyword And Ignore Error    Run Process    lxc    exec    ${OUTER_VM}    --    lxc    exec    ${container}    --    rm    -f    ${remotepath}    timeout=10
    Run Keyword And Ignore Error    Run Process    lxc    exec    ${OUTER_VM}    --    rm    -f    ${remotepath}    timeout=10
    Run Keyword And Ignore Error    Remove File    ${localpath}
    Should Be Equal As Integers    ${result.rc}    0
    ...    msg=Command failed (rc=${result.rc}):\nSTDERR: ${result.stderr}\nSTDOUT: ${result.stdout}

Run In Head Node
    [Documentation]    Runs cmd inside node-wrk0 container.
    [Arguments]    ${cmd}    ${timeout}=300
    Run In Container    node-wrk0    ${cmd}    ${timeout}

Get Public Network CIDR
    [Documentation]    Returns the CIDR of the LXD public network (e.g. 10.0.0.0/24) from the outer VM.
    ${result}=    Run In VM    lxc network list --format=csv | grep 'public' | cut -d, -f4    30
    RETURN    ${result.stdout.strip()}

Get VM Hostname
    [Documentation]    Returns the hostname of the outer VM.
    ${result}=    Run In VM    hostname
    RETURN    ${result.stdout.strip()}

Get VM IP
    [Documentation]    Returns the primary IP of the outer VM (first address from hostname -I).
    ${result}=    Run In VM    hostname -I | cut -d ' ' -f1    10
    RETURN    ${result.stdout.strip()}

# ---------------------------------------------------------------------------
# VM lifecycle
# ---------------------------------------------------------------------------

Launch Outer Test VM
    [Documentation]    Launches the LXD VM used as the test boundary. Deletes any pre-existing
    ...    instance with the same name so re-runs are clean.
    [Arguments]    ${vm_name}=${OUTER_VM}    ${disk_size}=${OUTER_VM_DISK}    ${enable_nesting}=${False}
    Require Host Commands    lxc
    Log To Console    \n[setup] Deleting pre-existing VM ${vm_name} (if any)...
    ${del}=    Run Process    lxc delete --force ${vm_name}    shell=True    timeout=60
    Log To Console    [setup] Launching VM ${vm_name} (disk=${disk_size})...
    ${cmd}=    Set Variable    lxc launch ubuntu:24.04 ${vm_name} --vm -c limits.cpu=4 -c limits.memory=6GiB -d root,size=${disk_size}
    FOR    ${attempt}    IN RANGE    3
        ${result}=    Run Process    ${cmd}    shell=True    timeout=300
        IF    ${result.rc} == 0    BREAK
        Log To Console    [setup] Launch attempt ${attempt} failed (rc=${result.rc}), retrying in 30s...
        Run Process    lxc delete --force ${vm_name}    shell=True    timeout=60
        IF    ${attempt} == 2    Fail    Failed to launch VM ${vm_name} after 3 attempts: ${result.stderr}
        Sleep    30s
    END
    Set Suite Variable    ${OUTER_VM}    ${vm_name}
    Log To Console    [setup] Waiting for VM agent in ${vm_name}...
    Wait For VM Agent    ${vm_name}
    Log To Console    [setup] Waiting for cloud-init in ${vm_name}...
    ${wait_cmd}=    Set Variable    lxc exec ${vm_name} -- cloud-init status --wait
    ${result}=    Run Process    ${wait_cmd}    shell=True    timeout=300
    Should Be Equal As Integers    ${result.rc}    0    msg=cloud-init failed in ${vm_name}: ${result.stderr}
    Log To Console    [setup] VM ${vm_name} ready.

Wait For VM Agent
    [Documentation]    Polls lxc exec until the LXD VM agent responds (60 × 5 s = 5 min max).
    [Arguments]    ${vm_name}
    Log    Waiting for VM agent in ${vm_name}
    FOR    ${i}    IN RANGE    60
        ${test_cmd}=    Set Variable    lxc exec ${vm_name} -- true
        ${result}=    Run Process    ${test_cmd}    shell=True    timeout=15
        Return From Keyword If    ${result.rc} == 0
        Sleep    5s
    END
    Fail    VM agent for ${vm_name} did not become ready within 5 minutes

# ---------------------------------------------------------------------------
# Script and artefact distribution
# ---------------------------------------------------------------------------

Copy Scripts To VM
    [Documentation]    Copies actionutils.sh and adoptutils.sh to ~/  in the outer VM.
    Log To Console    [setup] Copying scripts to ${OUTER_VM}...
    ${result}=    Run Process    lxc file push ${REPO_ROOT}/tests/scripts/actionutils.sh ${OUTER_VM}/root/actionutils.sh
    ...    shell=True    timeout=60
    Should Be Equal As Integers    ${result.rc}    0    msg=Failed to copy actionutils.sh: ${result.stderr}
    ${result}=    Run Process    lxc file push ${REPO_ROOT}/tests/scripts/adoptutils.sh ${OUTER_VM}/root/adoptutils.sh
    ...    shell=True    timeout=60
    Should Be Equal As Integers    ${result.rc}    0    msg=Failed to copy adoptutils.sh: ${result.stderr}
    Run In VM And Check    chmod +x ~/actionutils.sh ~/adoptutils.sh
    Log    Scripts copied to ${OUTER_VM}

Copy Snap To VM
    [Documentation]    Copies the snap to ~/microceph_0_amd64.snap inside the outer VM.
    ...    Multi-node containers mount ~/  as /mnt so install_multinode finds the snap via /mnt/microceph_*.snap.
    [Arguments]    ${snap_path}=${SNAP_PATH}
    IF    "${snap_path}" == "${EMPTY}"
        Log    SNAP_PATH not set — skipping snap copy    level=WARN
        RETURN
    END
    Log To Console    [setup] Copying snap to ${OUTER_VM} (this may take a minute)...
    ${result}=    Run Process    lxc file push ${snap_path} ${OUTER_VM}/root/microceph_0_amd64.snap
    ...    shell=True    timeout=120
    Should Be Equal As Integers    ${result.rc}    0    msg=Failed to push snap: ${result.stderr}
    Log    Snap pushed to ${OUTER_VM}:/root/microceph_0_amd64.snap

Copy Source To VM
    [Documentation]    Copies the repository source tree into ~/microceph/ inside the outer VM.
    ...    Uses git archive so only tracked files are transferred.
    ${result}=    Run Process
    ...    bash -c "cd ${REPO_ROOT} && git archive HEAD | lxc exec ${OUTER_VM} -- bash -c 'mkdir -p ~/microceph && tar -xf - -C ~/microceph'"
    ...    shell=True    timeout=120
    Should Be Equal As Integers    ${result.rc}    0    msg=Failed to copy source: ${result.stderr}
    Log    Source code copied to ${OUTER_VM}:/root/microceph

Copy DSL Test Script To VM
    [Documentation]    Copies the DSL functional test script (test_dsl_functest.sh) into the outer VM.
    ${result}=    Run Process    lxc file push ${REPO_ROOT}/tests/scripts/test_dsl_functest.sh ${OUTER_VM}/root/test_dsl_functest.sh
    ...    shell=True    timeout=60
    Should Be Equal As Integers    ${result.rc}    0    msg=Failed to copy test_dsl_functest.sh: ${result.stderr}
    Run In VM And Check    chmod +x ~/test_dsl_functest.sh
    Log    test_dsl_functest.sh copied to ${OUTER_VM}

Copy Hurl Files To VM
    [Documentation]    Copies all hurl test files from tests/hurl/ into ~/tests/hurl/ on the outer VM.
    ...    Required by API tests that call Run Hurl with relative paths like tests/hurl/foo.hurl.
    Run In VM And Check    mkdir -p ~/tests/hurl
    FOR    ${hurl_file}    IN
    ...    disks-delete.hurl
    ...    disks-encryption-support-supported.hurl
    ...    disks-encryption-support-unsupported.hurl
    ...    disks-list.hurl
    ...    disks-post-dryrun.hurl
    ...    maintenance-put-failed.hurl
    ...    services-mon.hurl
        ${result}=    Run Process
        ...    lxc file push ${REPO_ROOT}/tests/hurl/${hurl_file} ${OUTER_VM}/root/tests/hurl/${hurl_file}
        ...    shell=True    timeout=60
        Should Be Equal As Integers    ${result.rc}    0    msg=Failed to copy ${hurl_file}: ${result.stderr}
    END
    Log    Hurl files copied to ${OUTER_VM}:~/tests/hurl/

# ---------------------------------------------------------------------------
# Single-node MicroCeph setup
# ---------------------------------------------------------------------------

Install MicroCeph From Local Snap
    [Documentation]    Installs locally-built snap and connects all interfaces (except dm-crypt).
    [Arguments]    ${snap_path}=${SNAP_PATH}
    IF    "${snap_path}" == "${EMPTY}"
        Log    SNAP_PATH not set — skipping MicroCeph snap installation    level=WARN
        RETURN
    END
    Log To Console    [install] Installing MicroCeph snap...
    Run In VM And Check    sudo snap install core24 || true    120
    Run In VM And Check    sudo snap install --dangerous ~/microceph_*.snap    600
    Run In VM And Check    sudo snap connect microceph:block-devices    30
    Run In VM And Check    sudo snap connect microceph:hardware-observe    30
    Run In VM And Check    sudo snap connect microceph:mount-observe    30
    Run In VM And Check    sudo snap connect microceph:load-rbd    30
    Run In VM And Check    sudo snap connect microceph:microceph-support    30
    Run In VM And Check    sudo snap connect microceph:network-bind    30
    Run In VM And Check    sudo snap connect microceph:process-control    30

Bootstrap MicroCeph Cluster
    [Documentation]    Runs microceph cluster bootstrap and waits 30 s for stabilisation.
    [Arguments]    ${mon_ip}=${EMPTY}
    Log To Console    [bootstrap] Bootstrapping MicroCeph cluster...
    # Wait for the snap daemon to open the control socket before bootstrapping
    FOR    ${i}    IN RANGE    24
        ${ready}=    Run In VM    test -S /var/snap/microceph/common/state/control.socket && echo yes || echo no    15
        IF    "${ready.stdout.strip()}" == "yes"    BREAK
        Log To Console    [bootstrap] Waiting for MicroCeph control socket (${i})...
        Sleep    5s
    END
    IF    "${mon_ip}" != "${EMPTY}"
        Run In VM And Check    sudo microceph cluster bootstrap --mon-ip ${mon_ip}    120
    ELSE
        Run In VM And Check    sudo microceph cluster bootstrap    120
    END
    Run In VM And Check    sudo microceph.ceph version    30
    Run In VM And Check    sudo microceph.ceph status    30
    Sleep    30s
    Run In VM And Check    sudo microceph.ceph status    30
    Run In VM And Check    sudo microceph.ceph health    30

Install And Bootstrap MicroCeph
    [Documentation]    Installs snap and bootstraps the cluster.
    [Arguments]    ${mon_ip}=${EMPTY}
    Install MicroCeph From Local Snap
    Bootstrap MicroCeph Cluster    ${mon_ip}

Install Tools
    [Documentation]    Installs s3cmd and jq on the outer VM.
    Log To Console    [setup] Installing tools (s3cmd, jq)...
    Run In VM And Check    sudo apt-get update -qq    120
    Run In VM And Check    sudo apt-get -qq -y install s3cmd jq    120

Enable DM Crypt
    [Documentation]    Connects the dm-crypt snap interface and restarts microceph.daemon.
    Run In VM And Check    sudo snap connect microceph:dm-crypt    30
    Run In VM And Check    sudo snap restart microceph.daemon    60

# ---------------------------------------------------------------------------
# Multi-node LXD container setup
# ---------------------------------------------------------------------------

Setup LXD In VM
    [Documentation]    Installs and initialises LXD inside the outer VM.
    Log To Console    [setup] Setting up LXD inside ${OUTER_VM} (may take several minutes)...
    ${check}=    Run In VM    sudo snap list | grep -cF "lxd" || true    30
    IF    "${check.stdout.strip()}" != "1"
        Run In VM And Check    sudo snap install lxd    300
    END
    Run In VM And Check    sudo snap refresh    300
    Run In VM And Check    sudo snap set lxd daemon.group=adm    30
    Run In VM And Check    sudo lxd init --auto    60

Create LXD Containers With Loop Devices
    [Documentation]    Creates 4 privileged LXD containers with loop-back disks.
    [Arguments]    ${network_type}=public
    Log To Console    [setup] Creating LXD containers with loop devices (network=${network_type})...
    Run In VM And Check    lxc network create ${network_type}    60
    ${nw_result}=    Run In VM    lxc network list --format=csv | grep '${network_type}' | cut -d, -f4    30
    ${nw}=    Set Variable    ${nw_result.stdout.strip()}
    ${gw}=    Evaluate    '${nw}'.split('/')[0]
    ${mask}=    Evaluate    '${nw}'.split('/')[1]
    ${home_result}=    Run In VM    echo $HOME    10
    ${home}=    Set Variable    ${home_result.stdout.strip()}
    # Pre-pull ubuntu:22.04 image so the per-container init doesn't race the download
    Run In VM And Check    lxc image copy ubuntu:22.04 local: --alias ubuntu-22.04 --auto-update    600
    FOR    ${i}    IN RANGE    4
        ${c}=    Set Variable    node-wrk${i}
        Run In VM And Check    lxc init local:ubuntu-22.04 ${c}    60
        Run In VM And Check    lxc config set ${c} security.privileged true    10
        Run In VM And Check    lxc config set ${c} security.nesting true    10
        Run In VM And Check    bash -c "printf 'lxc.cgroup2.devices.allow = b 7:* rwm\\nlxc.cgroup2.devices.allow = c 10:237 rwm' | lxc config set ${c} raw.lxc -"    10
        Run In VM And Check    lxc config device add ${c} homedir disk source=${home} path=/mnt    10
        Run In VM And Check    lxc network attach ${network_type} ${c} eth2    10
        Run In VM And Check    lxc start ${c}    60
        Sleep    2s
        ${dev_result}=    Run In VM    lxc exec ${c} -- sh -c "ip a | grep ': eth' | tail -n 1 | cut -d@ -f1 | cut -d ' ' -f2"    30
        ${dev}=    Set Variable    ${dev_result.stdout.strip()}
        Run In VM And Check    lxc exec ${c} -- ip addr add ${gw}${i}/${mask} dev ${dev}    10
        ${lf_result}=    Run In VM    sudo mktemp -p /mnt mctest-${i}-XXXX.img    30
        ${lf}=    Set Variable    ${lf_result.stdout.strip()}
        Run In VM And Check    sudo truncate -s 1G ${lf}    30
        ${ld_result}=    Run In VM    sudo losetup --show -f ${lf}    30
        ${minor}=    Evaluate    '${ld_result.stdout.strip()}'.replace('/dev/loop', '')
        Run In VM And Check    lxc exec ${c} -- mknod -m 0660 /dev/sdia b 7 ${minor}    10
        Run In VM And Check    lxc exec ${c} -- ln -s /bin/true /usr/local/bin/udevadm    10
    END
    Install Tools

Install MicroCeph On All Nodes
    [Documentation]    Installs the locally-built snap on all 4 inner containers.
    [Arguments]    ${snap_path}=${SNAP_PATH}
    IF    "${snap_path}" == "${EMPTY}"
        Log    SNAP_PATH not set — skipping multi-node snap installation    level=WARN
        RETURN
    END
    Log To Console    [install] Installing MicroCeph on all nodes...
    FOR    ${container}    IN    node-wrk0    node-wrk1    node-wrk2    node-wrk3
        Log To Console    [install] Installing on ${container}...
        Run In VM And Check    lxc exec ${container} -- sh -c "sudo apt-get update -qq && sudo apt-get -qq -y install s3cmd jq"    300
        Run In VM And Check    lxc exec ${container} -- sh -c "sudo snap install --dangerous /mnt/microceph_*.snap"    600
        Run In VM And Check    lxc exec ${container} -- sh -c "snap connect microceph:block-devices && snap connect microceph:hardware-observe && snap connect microceph:mount-observe"    120
        Run In VM And Check    lxc exec ${container} -- snap alias microceph.ceph ceph    30
        Run In VM And Check    lxc exec ${container} -- sh -c "sed -e 's|/sys/devices/\\*\\*/ r,|/sys/devices/** r,|' -i.bak /var/lib/snapd/apparmor/profiles/snap.microceph.daemon"    30
        Run In VM And Check    lxc exec ${container} -- apparmor_parser -r /var/lib/snapd/apparmor/profiles/snap.microceph.daemon    60
        Run In VM And Check    lxc exec ${container} -- snap restart microceph.daemon    120
    END

Install MicroCeph From Store On All Nodes
    [Documentation]    Installs microceph from the Snap Store channel on all 4 inner containers.
    ...    Also installs s3cmd so RGW exercises work (jq is not needed; Wait For OSD Count Head uses outer VM's jq).
    [Arguments]    ${channel}
    Log To Console    [install] Installing MicroCeph from store (${channel}) on all nodes...
    FOR    ${container}    IN    node-wrk0    node-wrk1    node-wrk2    node-wrk3
        Run In VM And Check    lxc exec ${container} -- sh -c "sudo apt-get update -qq && sudo apt-get -qq -y install s3cmd && sudo snap install microceph --channel ${channel}"    300
    END

Bootstrap Head Node
    [Documentation]    Bootstraps microceph on node-wrk0.
    [Arguments]    ${network_mode}=public    ${extra_flags}=${EMPTY}
    Log To Console    [cluster] Bootstrapping head node (node-wrk0, network=${network_mode})...
    IF    "${network_mode}" == "public"
        ${result}=    Run In VM    lxc network list --format=csv | grep 'public' | cut -d, -f4    30
        ${nw}=    Set Variable    ${result.stdout.strip()}
        Run In Container    node-wrk0    microceph cluster bootstrap --public-network=${nw} ${extra_flags}    120
        Sleep    5s
        ${result}=    Run In VM    lxc network list --format=csv | grep 'public' | cut -d, -f4    30
        ${nw}=    Set Variable    ${result.stdout.strip()}
        ${gw}=    Evaluate    '${nw}'.split('/')[0]
        ${node_ip}=    Set Variable    ${gw}0
        ${mon_host_count}=    Run In VM    lxc exec node-wrk0 -- sh -c "grep 'mon host' /var/snap/microceph/current/conf/ceph.conf | grep -c '${node_ip}'"    30
        Should Be Equal As Strings    ${mon_host_count.stdout.strip()}    1    msg=IP ${node_ip} not exactly-once on mon host line in node-wrk0 ceph.conf (mirrors verify_bootstrap_configs)
        ${pub_count}=    Run In VM    lxc exec node-wrk0 -- sh -c "grep -c 'public_network = ${nw}' /var/snap/microceph/current/conf/ceph.conf"    30
        Should Be Equal As Strings    ${pub_count.stdout.strip()}    1    msg=public_network = ${nw} not exactly-once in node-wrk0 ceph.conf (mirrors verify_bootstrap_configs)
    ELSE IF    "${network_mode}" == "internal"
        ${result}=    Run In VM    lxc network list --format=csv | grep 'internal' | cut -d, -f4    30
        ${nw}=    Set Variable    ${result.stdout.strip()}
        ${gw}=    Evaluate    '${nw}'.split('/')[0]
        ${node_ip}=    Set Variable    ${gw}0
        Run In Container    node-wrk0    microceph cluster bootstrap --microceph-ip=${node_ip} ${extra_flags}    120
        Sleep    10s
        Run In Container    node-wrk0    microceph status | grep node-wrk0 | grep ${node_ip}    30
    ELSE
        Run In Container    node-wrk0    microceph cluster bootstrap ${extra_flags}    120
    END
    Run In Container    node-wrk0    microceph status    30
    Sleep    4s
    Run In Container    node-wrk0    microceph.ceph -s | grep "mon: 1 daemons, quorum node-wrk0"    30

Join Worker Nodes To Cluster
    [Documentation]    Joins node-wrk1..3 to the cluster.
    [Arguments]    ${network_mode}=public
    Log To Console    [cluster] Joining worker nodes to cluster (${network_mode})...
    ${nw_result}=    Run In VM    lxc network list --format=csv | grep '${network_mode}' | cut -d, -f4    30
    ${nw}=    Set Variable    ${nw_result.stdout.strip()}
    ${gw}=    Evaluate    '${nw}'.split('/')[0]
    ${mask}=    Evaluate    '${nw}'.split('/')[1]
    @{mon_ips}=    Create List    ${gw}0
    FOR    ${i}    IN    1    2    3
        Log To Console    [cluster] Joining node-wrk${i}...
        ${tok_result}=    Run In VM    lxc exec node-wrk0 -- microceph cluster add node-wrk${i}    60
        ${tok}=    Set Variable    ${tok_result.stdout.strip()}
        IF    "${network_mode}" == "internal"
            ${node_ip}=    Set Variable    ${gw}${i}
            Run In Container    node-wrk${i}    microceph cluster join ${tok} --microceph-ip=${node_ip}    120
            Sleep    10s
            Run In Container    node-wrk0    microceph status | grep node-wrk${i} | grep ${node_ip}    30
        ELSE
            Run In Container    node-wrk${i}    microceph cluster join ${tok}    120
        END
        IF    "${network_mode}" == "public"
            FOR    ${ip}    IN    @{mon_ips}
                ${ip_count}=    Run In VM    lxc exec node-wrk${i} -- sh -c "grep 'mon host' /var/snap/microceph/current/conf/ceph.conf | grep -c '${ip}'"    30
                Should Be Equal As Strings    ${ip_count.stdout.strip()}    1    msg=IP ${ip} not exactly-once on mon host line of node-wrk${i} (mirrors verify_bootstrap_configs)
            END
            ${pub_count}=    Run In VM    lxc exec node-wrk${i} -- sh -c "grep -c 'public_network = ${nw}' /var/snap/microceph/current/conf/ceph.conf"    30
            Should Be Equal As Strings    ${pub_count.stdout.strip()}    1    msg=public_network = ${nw} not exactly-once in node-wrk${i} ceph.conf (mirrors verify_bootstrap_configs)
            Append To List    ${mon_ips}    ${gw}${i}
        END
    END
    Wait For N Nodes In Cluster    4
    Run In Container    node-wrk0    microceph status    30
    Run In Container    node-wrk0    microceph.ceph -s    30

# ---------------------------------------------------------------------------
# OSD / disk operations
# ---------------------------------------------------------------------------

Add OSD To Node
    [Documentation]    Adds /dev/sdia as an OSD on the specified inner container.
    [Arguments]    ${container}
    Log To Console    [osd] Adding OSD to ${container}...
    Run In VM And Check    lxc exec ${container} -- microceph disk add /dev/sdia --wipe    120
    Sleep    1s

Wait For OSD Count
    [Documentation]    Polls until num_in_osds >= expected_count on the outer VM.
    [Arguments]    ${expected_count}    ${tries}=10
    Log To Console    [osd] Waiting for ${expected_count} OSD(s) on outer VM...
    FOR    ${i}    IN RANGE    ${tries}
        ${result}=    Run In VM    sudo microceph.ceph -s -f json 2>/dev/null | jq -r '.osdmap.num_in_osds // 0'    30
        ${count}=    Evaluate    int('${result.stdout.strip()}') if '${result.stdout.strip()}'.isdigit() else 0
        IF    ${count} >= ${expected_count}
            Log To Console    [osd] Found ${count} OSD(s)
            Run In VM And Check    sudo microceph.ceph -s    30
            RETURN
        END
        Sleep    5s
    END
    Run In VM And Check    sudo microceph.ceph -s    30
    Fail    Never reached ${expected_count} OSD(s) on outer VM

Wait For OSD Count Up In
    [Documentation]    Polls until BOTH num_up_osds AND num_in_osds >= expected_count on the outer VM.
    ...    Mirrors bash wait_for_osds_up_in: an OSD that is "in" but not "up" (e.g. a LUKS
    ...    volume that failed to reopen after a restart) must NOT satisfy this gate.
    [Arguments]    ${expected_count}    ${tries}=24
    Log To Console    [osd] Waiting for ${expected_count} OSD(s) up AND in on outer VM...
    FOR    ${i}    IN RANGE    ${tries}
        ${up}=    Run In VM    sudo microceph.ceph -s -f json 2>/dev/null | jq -r '.osdmap.num_up_osds // 0'    30
        ${in}=    Run In VM    sudo microceph.ceph -s -f json 2>/dev/null | jq -r '.osdmap.num_in_osds // 0'    30
        ${up_count}=    Evaluate    int('${up.stdout.strip()}') if '${up.stdout.strip()}'.isdigit() else 0
        ${in_count}=    Evaluate    int('${in.stdout.strip()}') if '${in.stdout.strip()}'.isdigit() else 0
        IF    ${up_count} >= ${expected_count} and ${in_count} >= ${expected_count}
            Log To Console    [osd] Found ${up_count} up / ${in_count} in OSD(s)
            Run In VM And Check    sudo microceph.ceph -s    30
            RETURN
        END
        Sleep    5s
    END
    Run In VM And Check    sudo microceph.ceph -s    30
    Fail    Never reached ${expected_count} OSD(s) up AND in on outer VM (up<${expected_count} or in<${expected_count})

Wait For OSD Count Head
    [Documentation]    Polls until num_in_osds >= expected_count via node-wrk0.
    ...    jq runs on the outer VM (not inside the container) so it works regardless of container tool availability.
    [Arguments]    ${expected_count}    ${tries}=20
    Log To Console    [osd] Waiting for ${expected_count} OSD(s) on node-wrk0...
    FOR    ${i}    IN RANGE    ${tries}
        ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph -s -f json 2>/dev/null" | jq -r '.osdmap.num_in_osds // 0'    30
        ${count}=    Evaluate    int('${result.stdout.strip()}') if '${result.stdout.strip()}'.isdigit() else 0
        IF    ${count} >= ${expected_count}
            Log To Console    [osd] Found ${count} OSD(s)
            Run In Container    node-wrk0    microceph.ceph -s    30
            RETURN
        END
        Sleep    5s
    END
    Run In Container    node-wrk0    microceph.ceph -s    30
    Fail    Never reached ${expected_count} OSD(s) on node-wrk0

Add Encrypted OSDs
    [Documentation]    Enables dm-crypt, creates loop devices, adds two encrypted OSDs.
    Log To Console    [osd] Adding encrypted OSDs with dm-crypt...
    Run In VM And Check    sudo snap connect microceph:dm-crypt    30
    Run In VM And Check    sudo snap restart microceph.daemon    60
    Create Loop Devices
    Run In VM And Check    sudo microceph disk add /dev/sdia /dev/sdib --wipe --encrypt    120
    Sleep    30s
    ${result}=    Run In VM    sudo microceph disk list --json | jq -r '.ConfiguredDisks[].path' | grep -e "/dev/sdia" -e "/dev/sdib" -c    30
    Should Be Equal As Strings    ${result.stdout.strip()}    2    msg=Expected 2 encrypted disks, got ${result.stdout.strip()}

Add LVM Volume OSD
    [Documentation]    Creates an LVM volume on a loop device and adds it as an OSD.
    Log To Console    [osd] Adding LVM volume OSD...
    ${lf_result}=    Run In VM    mktemp /tmp/mctestXXXXXX    30
    ${lf}=    Set Variable    ${lf_result.stdout.strip()}
    Run In VM And Check    sudo truncate -s 4G ${lf}    30
    ${ld_result}=    Run In VM    sudo losetup --show -f ${lf}    30
    ${ld}=    Set Variable    ${ld_result.stdout.strip()}
    Run In VM And Check    sudo pvcreate ${ld}    30
    Run In VM And Check    sudo vgcreate vgtst ${ld}    30
    Run In VM And Check    sudo lvcreate -l100%FREE --name lvtest vgtst    30
    Run In VM And Check    sudo microceph disk add /dev/vgtst/lvtest --wipe    120
    Sleep    20s
    Run In VM And Check    sudo microceph.ceph -s    30
    ${result}=    Run In VM    sudo microceph disk list --json | jq -r '.ConfiguredDisks[].path' | grep "/dev/vgtst/lvtest" -c    30
    Should Be Equal As Strings    ${result.stdout.strip()}    1    msg=LVM OSD not found in disk list

Create Loop Device At
    [Documentation]    Creates a single named loop-backed block device at ${device} on the outer VM.
    [Arguments]    ${device}    ${size}=4G
    ${lf_result}=    Run In VM    mktemp /tmp/mctestXXXXXX    30
    ${lf}=    Set Variable    ${lf_result.stdout.strip()}
    Run In VM And Check    sudo truncate -s ${size} ${lf}    30
    ${ld_result}=    Run In VM    sudo losetup --show -f ${lf}    30
    ${minor}=    Evaluate    '${ld_result.stdout.strip()}'.replace('/dev/loop', '')
    Run In VM And Check    sudo mknod -m 0660 ${device} b 7 ${minor}    30

Create Loop Devices
    [Documentation]    Creates /dev/sdia, /dev/sdib, /dev/sdic as loop-backed devices.
    Log To Console    [osd] Creating loop devices /dev/sdia, /dev/sdib, /dev/sdic...
    FOR    ${l}    IN    a    b    c
        Create Loop Device At    /dev/sdi${l}
    END

# ---------------------------------------------------------------------------
# RGW operations
# ---------------------------------------------------------------------------

Wait For RGW
    [Arguments]    ${expect}    ${tries}=8
    Log To Console    [rgw] Waiting for ${expect} RGW daemon(s)...
    FOR    ${i}    IN RANGE    ${tries}
        ${result}=    Run In VM    sudo microceph.ceph -s | grep -F "rgw:" | sed -E "s/.* ([0-9]+) daemon.*/\1/" || echo 0    30
        ${count}=    Evaluate    int('${result.stdout.strip()}') if '${result.stdout.strip()}'.isdigit() else 0
        IF    ${count} >= ${expect}
            Log To Console    [rgw] Found ${count} RGW daemon(s)
            RETURN
        END
        Sleep    5s
    END
    Run In VM And Check    sudo microceph.ceph -s    30
    Fail    Never reached ${expect} RGW daemon(s)

Enable RGW
    [Documentation]    Enables the Rados Gateway daemon and waits for it to start.
    Log To Console    [rgw] Enabling RGW...
    Run In VM And Check    sudo microceph enable rgw    120
    Wait For RGW    1

Disable RGW
    [Documentation]    Disables the Rados Gateway daemon.
    Log To Console    [rgw] Disabling RGW...
    Run In VM And Check    sudo microceph disable rgw    60

Enable RGW SSL
    [Documentation]    Generates an SSL certificate and enables RGW with SSL.
    Log To Console    [rgw] Generating SSL certificate and enabling RGW with SSL...
    Run In VM And Check    sudo openssl genrsa -out /tmp/ca.key 2048    30
    Run In VM And Check    sudo openssl req -x509 -new -nodes -key /tmp/ca.key -days 1024 -out /tmp/ca.crt -outform PEM -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com"    30
    Run In VM And Check    sudo openssl genrsa -out /tmp/server.key 2048    30
    Run In VM And Check    sudo openssl req -new -key /tmp/server.key -out /tmp/server.csr -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com"    30
    Run In VM And Check    bash -c "echo 'subjectAltName = DNS:localhost' > /tmp/extfile.cnf"    10
    Run In VM And Check    sudo openssl x509 -req -in /tmp/server.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/server.crt -days 365 -extfile /tmp/extfile.cnf    30
    ${cert}=    Run In VM    sudo base64 -w0 /tmp/server.crt    30
    ${key}=    Run In VM    sudo base64 -w0 /tmp/server.key    30
    Run In VM And Check    sudo microceph enable rgw --ssl-certificate="${cert.stdout.strip()}" --ssl-private-key="${key.stdout.strip()}"    120
    Wait For RGW    1
    Wait For RGW SSL Port

Wait For RGW SSL Port
    [Arguments]    ${host}=localhost    ${port}=443    ${tries}=60
    Log To Console    [rgw] Waiting for RGW SSL on ${host}:${port}...
    FOR    ${i}    IN RANGE    ${tries}
        ${result}=    Run In VM    echo | openssl s_client -connect ${host}:${port} 2>/dev/null | grep -q "BEGIN CERTIFICATE" && echo yes || echo no    15
        IF    "${result.stdout.strip()}" == "yes"    RETURN
        Sleep    5s
    END
    Fail    RGW SSL never started on ${host}:${port}

Get RGW SSL CN
    [Arguments]    ${host}=localhost    ${port}=443
    ${result}=    Run In VM    echo | openssl s_client -connect ${host}:${port} 2>/dev/null | openssl x509 -noout -subject 2>/dev/null | sed -n 's/.*CN *= *//p'    30
    RETURN    ${result.stdout.strip()}

Exercise RGW
    [Documentation]    Creates an S3 user, uploads a file via s3cmd, and verifies via curl.
    [Arguments]    ${filename}=test
    Log To Console    [rgw] Exercising RGW (HTTP)...
    Run In VM And Check    sudo microceph.ceph status    30
    Run In VM And Check    sudo systemctl status snap.microceph.rgw --no-pager    30
    ${user_exists}=    Run In VM    sudo microceph.radosgw-admin user list | grep -q test && echo yes || echo no    30
    IF    "${user_exists.stdout.strip()}" != "yes"
        Run In VM And Check    sudo microceph.radosgw-admin user create --uid=test --display-name=test    60
        Run In VM And Check    sudo microceph.radosgw-admin key create --uid=test --key-type=s3 --access-key fooAccessKey --secret-key fooSecretKey    30
    END
    Run In VM And Check    bash -c "echo hello-radosgw > ~/${filename}.txt"    10
    Run In VM And Check    s3cmd --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey --no-ssl mb s3://testbucket    30
    Run In VM And Check    s3cmd --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey --no-ssl put -P ~/${filename}.txt s3://testbucket    30
    Run In VM And Check    curl -s http://localhost/testbucket/${filename}.txt | grep -F hello-radosgw    30

Exercise RGW SSL
    [Documentation]    Exercises RGW over HTTPS using the CA cert from enable_rgw_ssl.
    [Arguments]    ${filename}=test
    Log To Console    [rgw] Exercising RGW (HTTPS)...
    Run In VM And Check    sudo microceph.ceph status    30
    Run In VM And Check    sudo systemctl status snap.microceph.rgw --no-pager    30
    ${user_exists}=    Run In VM    sudo microceph.radosgw-admin user list | grep -q test && echo yes || echo no    30
    IF    "${user_exists.stdout.strip()}" != "yes"
        Run In VM And Check    sudo microceph.radosgw-admin user create --uid=test --display-name=test    60
        Run In VM And Check    sudo microceph.radosgw-admin key create --uid=test --key-type=s3 --access-key fooAccessKey --secret-key fooSecretKey    30
    END
    Run In VM And Check    bash -c "echo hello-radosgw-ssl > ~/${filename}.txt"    10
    Run In VM And Check    s3cmd --ca-certs=/tmp/ca.crt --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey mb s3://testbucketssl    30
    Run In VM And Check    s3cmd --ca-certs=/tmp/ca.crt --host localhost --host-bucket="localhost/%(bucket)" --access_key=fooAccessKey --secret_key=fooSecretKey put -P ~/${filename}.txt s3://testbucketssl    30
    Run In VM And Check    CURL_CA_BUNDLE=/tmp/ca.crt curl -s https://localhost/testbucketssl/${filename}.txt | grep -F hello-radosgw-ssl    30

Generate Self Signed CA And Server Cert In Container
    [Documentation]    Generates a self-signed CA and server cert chain inside ${container}.
    ...    Creates /tmp/ca.key, /tmp/ca.crt, /tmp/server.key, /tmp/server.csr,
    ...    /tmp/server.crt, and /tmp/extfile.cnf inside the container.
    [Arguments]    ${container}
    Run In Container    ${container}    sudo openssl genrsa -out /tmp/ca.key 2048 && sudo openssl req -x509 -new -nodes -key /tmp/ca.key -days 1024 -out /tmp/ca.crt -outform PEM -subj '/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com' && sudo openssl genrsa -out /tmp/server.key 2048 && sudo openssl req -new -key /tmp/server.key -out /tmp/server.csr -subj '/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com' && echo 'subjectAltName = DNS:localhost' > /tmp/extfile.cnf && sudo openssl x509 -req -in /tmp/server.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/server.crt -days 365 -extfile /tmp/extfile.cnf    120

Read Base64 File From Container
    [Documentation]    Returns the base64-encoded (no line wrapping) contents of a file inside ${container}.
    [Arguments]    ${container}    ${path}
    ${result}=    Run In VM    lxc exec ${container} -- bash -c "sudo base64 -w0 ${path}"    30
    RETURN    ${result.stdout.strip()}

Wait For RGW On Head Node
    [Documentation]    Polls until at least ${expect} RGW daemons are running on node-wrk0.
    [Arguments]    ${expect}    ${tries}=20
    Log To Console    [rgw] Waiting for ${expect} RGW daemon(s) on node-wrk0...
    FOR    ${i}    IN RANGE    ${tries}
        ${result}=    Run In VM    lxc exec node-wrk0 -- bash -c "microceph.ceph -s | grep -F 'rgw:' | sed -E 's/.* ([0-9]+) daemon.*/\\1/' || echo 0"    30
        ${count}=    Evaluate    int('${result.stdout.strip()}') if '${result.stdout.strip()}'.isdigit() else 0
        IF    ${count} >= ${expect}    RETURN
        Sleep    5s
    END
    Fail    Never reached ${expect} RGW daemon(s) on head node

Enable RGW Head Node
    [Documentation]    Enables RGW on node-wrk0 and waits for it to start.
    Log To Console    [rgw] Enabling RGW on node-wrk0...
    Run In Container    node-wrk0    microceph enable rgw    120
    Wait For RGW On Head Node    1

Exercise RGW Head Node
    [Documentation]    Exercises RGW on node-wrk0.
    [Arguments]    ${filename}=test
    Log To Console    [rgw] Exercising RGW on node-wrk0...
    Run In Container    node-wrk0    microceph.ceph status    30
    ${user_exists}=    Run In VM    lxc exec node-wrk0 -- bash -c "microceph.radosgw-admin user list | grep -q test && echo yes || echo no"    30
    IF    "${user_exists.stdout.strip()}" != "yes"
        Run In Container    node-wrk0    microceph.radosgw-admin user create --uid=test --display-name=test    60
        Run In Container    node-wrk0    microceph.radosgw-admin key create --uid=test --key-type=s3 --access-key fooAccessKey --secret-key fooSecretKey    30
    END
    Run In Container    node-wrk0    echo hello-radosgw > ~/test.txt    10
    Run In Container    node-wrk0    s3cmd --host localhost --host-bucket=localhost/%\\(bucket\\) --access_key=fooAccessKey --secret_key=fooSecretKey --no-ssl mb s3://testbucket    30
    Run In Container    node-wrk0    s3cmd --host localhost --host-bucket=localhost/%\\(bucket\\) --access_key=fooAccessKey --secret_key=fooSecretKey --no-ssl put -P ~/test.txt s3://testbucket    30
    Run In Container    node-wrk0    curl -s http://localhost/testbucket/test.txt | grep -F hello-radosgw    30

# ---------------------------------------------------------------------------
# Cluster health and config
# ---------------------------------------------------------------------------

Wait For Cluster Health OK
    [Documentation]    Polls microceph.ceph health until HEALTH_OK (${tries} × ${interval}).
    ...    Pass node= (e.g. node-wrk0) to poll inside that LXD container; omit to run
    ...    directly on the outer VM with sudo.
    [Arguments]    ${node}=${EMPTY}    ${tries}=100    ${interval}=3s
    IF    '${node}' == '${EMPTY}'
        ${cmd}=    Set Variable    sudo microceph.ceph health
        ${label}=    Set Variable    outer VM
    ELSE
        ${cmd}=    Set Variable    lxc exec ${node} -- microceph.ceph health
        ${label}=    Set Variable    ${node}
    END
    Log To Console    [health] Waiting for HEALTH_OK (${label})...
    FOR    ${i}    IN RANGE    ${tries}
        ${result}=    Run In VM    ${cmd}    30
        IF    "${result.stdout.strip()}" == "HEALTH_OK"
            Log To Console    [health] HEALTH_OK
            RETURN
        END
        Sleep    ${interval}
    END
    IF    '${node}' == '${EMPTY}'
        Run In VM And Check    sudo microceph.ceph -s    30
    ELSE
        Run In Container    ${node}    microceph.ceph -s    30
    END
    Fail    Cluster did not reach HEALTH_OK

Verify Cluster Health
    [Documentation]    Polls microceph.ceph health until HEALTH_OK on the outer VM.
    Wait For Cluster Health OK

Verify Cluster Health Head Node
    [Documentation]    Polls microceph.ceph health until HEALTH_OK via node-wrk0.
    Wait For Cluster Health OK    node-wrk0

Test Cluster Config Operations
    [Documentation]    Verifies rbd_default_features and tests cluster_network config set/reset.
    Log To Console    [config] Testing cluster config set/reset...
    ${rbd_feat}=    Run In VM    sudo microceph.ceph config get mon rbd_default_features    30
    Should Be Equal As Strings    ${rbd_feat.stdout.strip()}    63    msg=rbd_default_features not 63
    ${cip}=    Run In VM    ip -4 -j route | jq -r '.[] | select(.dst | contains("default")) | .prefsrc' | tr -d '[:space:]'    30
    ${ts}=    Run In VM    sudo systemctl show --property ActiveEnterTimestampMonotonic snap.microceph.osd.service | cut -d= -f2    30
    Run In VM And Check    sudo microceph cluster config set cluster_network ${cip.stdout.strip()}/8 --wait    60
    ${ts2}=    Run In VM    sudo systemctl show --property ActiveEnterTimestampMonotonic snap.microceph.osd.service | cut -d= -f2    30
    ${out}=    Run In VM    sudo microceph cluster config get cluster_network | grep -cim1 'cluster_network'    30
    Should Be True    int('${out.stdout.strip()}') >= 1    msg=config check failed
    Should Be True    int('${ts2.stdout.strip()}') >= int('${ts.stdout.strip()}')    msg=OSD service did not restart after config set
    Run In VM And Check    sudo microceph cluster config reset cluster_network --wait    60
    ${ts3}=    Run In VM    sudo systemctl show --property ActiveEnterTimestampMonotonic snap.microceph.osd.service | cut -d= -f2    30
    Should Be True    int('${ts3.stdout.strip()}') >= int('${ts2.stdout.strip()}')    msg=OSD service did not restart after config reset

Test Pool Replication Operations
    [Documentation]    Tests pool replication-factor set/reset operations.
    Log To Console    [pool] Testing pool replication factor operations...
    Run In VM    sudo microceph.ceph osd pool create mypool    30
    Run In VM And Check    sudo microceph pool set-rf --size 1 ""    30
    Run In VM And Check    sudo microceph.ceph config get osd.1 osd_pool_default_size | grep -Fx "1"    30
    Run In VM And Check    sudo microceph pool list | grep "mypool"    30
    Run In VM And Check    sudo microceph pool list | grep "mypool" | grep -F " 3 "    30
    Run In VM And Check    sudo microceph pool set-rf --size 3 mypool    30
    Run In VM And Check    sudo microceph.ceph osd pool get mypool size | grep -F "size: 3"    30
    Run In VM And Check    sudo microceph pool list | grep "mypool" | grep -F " 3 "    30
    Run In VM And Check    sudo microceph pool set-rf --size 1 "*"    30
    Run In VM And Check    sudo microceph.ceph osd pool get mypool size | grep -F "size: 1"    30
    Run In VM And Check    sudo microceph pool list | grep "mypool" | grep -F " 1 "    30

Test Log Level Control
    [Documentation]    Sets log level to warning (3) and verifies.
    Run In VM And Check    sudo microceph log set-level warning    30
    ${result}=    Run In VM    sudo microceph log get-level    30
    Should Be Equal As Strings    ${result.stdout.strip()}    3    msg=Incorrect log level: ${result.stdout}

Test Snap Disable Enable
    [Documentation]    Tests that snap disable/enable re-enables all services.
    ...    Records the names of services that were enabled+active before disable and verifies
    ...    each one by name is restored after re-enable (not just the aggregate count), and
    ...    fails on timeout. Mirrors bash test_snap_disable_enable + check_snap_service_active_enabled.
    Log To Console    [snap] Testing snap disable/enable service restoration...
    ${before_result}=    Run In VM    snap services microceph | awk '$2 == "enabled" && $3 == "active" {print $1}'    30
    @{services_before}=    Evaluate    $before_result.stdout.split()
    ${count_before}=    Get Length    ${services_before}
    Log To Console    [snap] ${count_before} enabled+active service(s) before disable: ${services_before}
    Run In VM And Check    sudo snap disable microceph    60
    Run In VM And Check    sudo snap enable microceph    60
    ${restored}=    Set Variable    ${False}
    FOR    ${i}    IN RANGE    30
        ${active_count}=    Run In VM    snap services microceph | awk '$2 == "enabled" && $3 == "active"' | wc -l    30
        ${active}=    Evaluate    int('${active_count.stdout.strip()}')
        IF    ${active} >= ${count_before}
            Log To Console    [snap] All ${count_before} service(s) re-enabled after ${i}s
            ${restored}=    Set Variable    ${True}
            BREAK
        END
        Sleep    1s
    END
    IF    not ${restored}    Fail    Not all services re-enabled after 30s (expected ${count_before})
    # Verify each previously enabled+active service is back, by name.
    FOR    ${svc}    IN    @{services_before}
        ${svc_state}=    Run In VM    snap services ${svc}    30
        Should Contain    ${svc_state.stdout}    enabled    msg=Service ${svc} not enabled after re-enable
        Should Contain    ${svc_state.stdout}    active    msg=Service ${svc} not active after re-enable
    END
    Verify Cluster Health

Test SSL Certificate Rotation
    [Documentation]    Tests RGW SSL certificate rotation (with/without --restart).
    Log To Console    [rgw] Testing SSL certificate rotation...
    Run In VM And Check    sudo openssl genrsa -out /tmp/rotated-cert.key 2048    30
    Run In VM And Check    sudo openssl req -new -key /tmp/rotated-cert.key -out /tmp/rotated-cert.csr -subj "/CN=rotated-cert"    30
    Run In VM And Check    bash -c "echo 'subjectAltName = DNS:localhost' > /tmp/rotated-cert-ext.cnf"    10
    Run In VM And Check    sudo openssl x509 -req -in /tmp/rotated-cert.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/rotated-cert.crt -days 365 -extfile /tmp/rotated-cert-ext.cnf    30
    ${cert}=    Run In VM    sudo base64 -w0 /tmp/rotated-cert.crt    30
    ${key}=    Run In VM    sudo base64 -w0 /tmp/rotated-cert.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${cert.stdout.strip()}" --ssl-private-key="${key.stdout.strip()}" --restart    120
    Wait For RGW    1
    Wait For RGW SSL Port
    ${cn}=    Get RGW SSL CN
    Should Be Equal As Strings    ${cn}    rotated-cert    msg=Expected CN=rotated-cert after --restart
    Run In VM And Check    sudo openssl genrsa -out /tmp/rotated-cert-2.key 2048    30
    Run In VM And Check    sudo openssl req -new -key /tmp/rotated-cert-2.key -out /tmp/rotated-cert-2.csr -subj "/CN=rotated-cert-2"    30
    Run In VM And Check    bash -c "echo 'subjectAltName = DNS:localhost' > /tmp/rotated-cert-2-ext.cnf"    10
    Run In VM And Check    sudo openssl x509 -req -in /tmp/rotated-cert-2.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/rotated-cert-2.crt -days 365 -extfile /tmp/rotated-cert-2-ext.cnf    30
    ${cert2}=    Run In VM    sudo base64 -w0 /tmp/rotated-cert-2.crt    30
    ${key2}=    Run In VM    sudo base64 -w0 /tmp/rotated-cert-2.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${cert2.stdout.strip()}" --ssl-private-key="${key2.stdout.strip()}"    60
    Sleep    3s
    ${cn}=    Get RGW SSL CN
    Should Be Equal As Strings    ${cn}    rotated-cert    msg=Old cert should still be served without restart
    Run In VM And Check    sudo snap restart microceph.rgw    60
    Wait For RGW    1
    Wait For RGW SSL Port
    ${cn}=    Get RGW SSL CN
    Should Be Equal As Strings    ${cn}    rotated-cert-2    msg=Expected rotated-cert-2 after manual restart
    ${orig_cert}=    Run In VM    sudo base64 -w0 /tmp/server.crt    30
    ${orig_key}=    Run In VM    sudo base64 -w0 /tmp/server.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${orig_cert.stdout.strip()}" --ssl-private-key="${orig_key.stdout.strip()}" --restart    120
    Wait For RGW    1
    Wait For RGW SSL Port

Test SSL Certificate Rotation With Target
    [Documentation]    Tests RGW SSL certificate rotation with --target flag.
    [Arguments]    ${target}
    Log To Console    [rgw] Testing certificate rotation with --target ${target}...
    ${addr_result}=    Run In VM    sudo microceph status | grep -F "${target}" | grep -oP '\\(\\K[^)]+' || true    30
    ${target_addr}=    Set Variable    ${addr_result.stdout.strip()}
    Run In VM And Check    sudo openssl genrsa -out /tmp/target-cert.key 2048    30
    Run In VM And Check    sudo openssl req -new -key /tmp/target-cert.key -out /tmp/target-cert.csr -subj "/CN=target-cert"    30
    Run In VM And Check    bash -c "echo 'subjectAltName = DNS:localhost' > /tmp/target-cert-ext.cnf"    10
    Run In VM And Check    sudo openssl x509 -req -in /tmp/target-cert.csr -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial -out /tmp/target-cert.crt -days 365 -extfile /tmp/target-cert-ext.cnf    30
    ${cert}=    Run In VM    sudo base64 -w0 /tmp/target-cert.crt    30
    ${key}=    Run In VM    sudo base64 -w0 /tmp/target-cert.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${cert.stdout.strip()}" --ssl-private-key="${key.stdout.strip()}" --target ${target} --restart    120
    Wait For RGW    1
    Wait For RGW SSL Port    ${target_addr}
    ${cn}=    Get RGW SSL CN    ${target_addr}
    Should Be Equal As Strings    ${cn}    target-cert    msg=Expected CN=target-cert on ${target}
    ${orig_cert}=    Run In VM    sudo base64 -w0 /tmp/server.crt    30
    ${orig_key}=    Run In VM    sudo base64 -w0 /tmp/server.key    30
    Run In VM And Check    sudo microceph certificate set rgw --ssl-certificate="${orig_cert.stdout.strip()}" --ssl-private-key="${orig_key.stdout.strip()}" --target ${target} --restart    120
    Wait For RGW    1
    Wait For RGW SSL Port    ${target_addr}

Test SSL Certificate Set When Not Running
    [Documentation]    Verifies that certificate set fails when RGW is not running.
    Log To Console    [rgw] Verifying certificate set fails when RGW is not running...
    Run In VM And Check    sudo microceph disable rgw    60
    Sleep    3s
    ${cert}=    Run In VM    sudo base64 -w0 /tmp/server.crt    30
    ${key}=    Run In VM    sudo base64 -w0 /tmp/server.key    30
    Run In VM Must Fail    sudo microceph certificate set rgw --ssl-certificate="${cert.stdout.strip()}" --ssl-private-key="${key.stdout.strip()}"

Test IPv6 Monitor Address Formatting
    [Documentation]    Reinstalls with IPv6 mon-ip and verifies square brackets in ceph.conf.
    [Arguments]    ${mon_ip}=fd42:7273:f336:a22::1
    Log To Console    [mon] Testing IPv6 monitor address formatting...
    Run In VM And Check    sudo snap remove microceph    300
    ${iface_result}=    Run In VM    ip route show default | awk '/default via/ {print $5}' | head -1    30
    ${iface}=    Set Variable    ${iface_result.stdout.strip()}
    Log To Console    [mon] Adding IPv6 address to interface ${iface}...
    Run In VM And Check    sudo ip -6 addr add dev ${iface} ${mon_ip}    30
    Install And Bootstrap MicroCeph    ${mon_ip}
    ${result}=    Run In VM    grep -F "[${mon_ip}]" /var/snap/microceph/current/conf/ceph.conf    30
    Should Be Equal As Integers    ${result.rc}    0    msg=IPv6 address ${mon_ip} not wrapped in brackets in ceph.conf

Verify Ceph Config Has Public Network
    [Documentation]    Checks that all nodes' ceph.conf contains public_network entry.
    Log To Console    [config] Verifying all nodes have public_network in ceph.conf...
    ${nodes}=    Run In VM    lxc ls -c n --format csv    30
    FOR    ${node}    IN    @{nodes.stdout.strip().split('\n')}
        Run In VM And Check    lxc exec ${node.strip()} -- sh -c "grep -q public_network /var/snap/microceph/current/conf/ceph.conf"    30
    END

# ---------------------------------------------------------------------------
# Multi-node specific helpers
# ---------------------------------------------------------------------------

Check Client Configs
    [Documentation]    Sets cluster-wide and per-host client configs, then verifies and resets.
    Log To Console    [config] Checking client config set/reset across nodes...
    Run In Container    node-wrk0    microceph client config set rbd_cache true    30
    FOR    ${id}    IN    1    2
        ${size}=    Evaluate    512 * ${id}
        Run In Container    node-wrk${id}    microceph client config set rbd_cache_size ${size} --target node-wrk${id}    30
    END
    FOR    ${id}    IN    1    2
        ${size}=    Evaluate    512 * ${id}
        ${r1}=    Run In VM    lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache = true'"    30
        ${r2}=    Run In VM    lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache_size = ${size}'"    30
        Should Be Equal As Strings    ${r1.stdout.strip()}    1    msg=rbd_cache not set on node-wrk${id}
        Should Be Equal As Strings    ${r2.stdout.strip()}    1    msg=rbd_cache_size not set on node-wrk${id}
    END
    Run In Container    node-wrk0    microceph client config reset rbd_cache --yes-i-really-mean-it    30
    Run In Container    node-wrk0    microceph client config reset rbd_cache_size --yes-i-really-mean-it    30
    FOR    ${id}    IN    1    2
        ${r1}=    Run In VM    lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache '"    30
        ${r2}=    Run In VM    lxc exec node-wrk${id} -- sh -c "cat /var/snap/microceph/current/conf/ceph.conf | grep -c 'rbd_cache_size'"    30
        Should Be Equal As Strings    ${r1.stdout.strip()}    0    msg=rbd_cache still in ceph.conf on node-wrk${id}
        Should Be Equal As Strings    ${r2.stdout.strip()}    0    msg=rbd_cache_size still in ceph.conf on node-wrk${id}
    END

Test Service Migration
    [Documentation]    Migrates services from src to dst and verifies placement.
    [Arguments]    ${src}    ${dst}
    Log To Console    [cluster] Migrating services from ${src} to ${dst}...
    Run In Container    node-wrk0    microceph cluster migrate ${src} ${dst}    120
    FOR    ${i}    IN RANGE    8
        ${src_ok}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph status | grep -F -A 1 ${src} | grep -qE '^ {2}Services: osd$' && echo yes || echo no"    30
        ${dst_ok}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph status | grep -F -A 1 ${dst} | grep -qE '^ {2}Services: mds, mgr, mon$' && echo yes || echo no"    30
        IF    "${src_ok.stdout.strip()}" == "yes" and "${dst_ok.stdout.strip()}" == "yes"
            Log To Console    [cluster] Services migrated successfully
            BREAK
        END
        Sleep    10s
    END
    Run In Container    node-wrk0    microceph status    30
    Run In Container    node-wrk0    microceph.ceph -s    30
    ${src_ok}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph status | grep -F -A 1 ${src} | grep -qE '^ {2}Services: osd$' && echo yes || echo no"    30
    ${dst_ok}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph status | grep -F -A 1 ${dst} | grep -qE '^ {2}Services: mds, mgr, mon$' && echo yes || echo no"    30
    Should Be Equal As Strings    ${src_ok.stdout.strip()}    yes    msg=${src} should have only OSD after migration
    Should Be Equal As Strings    ${dst_ok.stdout.strip()}    yes    msg=${dst} should have mds,mgr,mon after migration

Enable Services On Node
    [Documentation]    Enables mon/mds/mgr services on specified node.
    [Arguments]    ${node}
    Log To Console    [cluster] Enabling mon/mds/mgr on ${node}...
    FOR    ${svc}    IN    mon    mds    mgr
        Run In VM And Check    sudo microceph enable ${svc} --target ${node}    120
    END
    FOR    ${i}    IN RANGE    8
        ${result}=    Run In VM    sudo microceph.ceph -s | grep -q "mon: .*daemons.*${node}" && echo yes || echo no    30
        IF    "${result.stdout.strip()}" == "yes"    BREAK
        Sleep    2s
    END
    Run In VM And Check    sudo microceph.ceph -s    30

Remove Node
    [Documentation]    Removes specified node from the cluster.
    [Arguments]    ${node}
    Log To Console    [cluster] Removing node ${node}...
    Run In VM And Check    sudo microceph cluster remove ${node}    120
    FOR    ${i}    IN RANGE    8
        ${result}=    Run In VM    sudo microceph.ceph -s | grep -q "mon: .*daemons.*${node}" && echo yes || echo no    30
        IF    "${result.stdout.strip()}" != "yes"    BREAK
        Sleep    5s
    END
    Sleep    1s
    Run In VM And Check    sudo microceph.ceph -s    30
    Run In VM And Check    sudo microceph status    30

# ---------------------------------------------------------------------------
# NFS operations
# ---------------------------------------------------------------------------

Get Node IP
    [Documentation]    Returns the primary IP of a container (first address from hostname -I).
    [Arguments]    ${container}
    ${result}=    Run In VM    lxc exec ${container} -- bash -c 'hostname -I' | cut -d ' ' -f1    30
    RETURN    ${result.stdout.strip()}

Mount NFS In VM
    [Documentation]    Mounts an NFS share from ${ip}:${export} at ${mountpoint} on the outer VM.
    [Arguments]    ${ip}    ${export}    ${mountpoint}
    Run In VM And Check    sudo mount -t nfs -o rw "${ip}:${export}" ${mountpoint}    30

Write File In VM
    [Documentation]    Writes ${content} to ${path} on the outer VM using sudo tee.
    [Arguments]    ${path}    ${content}
    Run In VM And Check    echo "${content}" | sudo tee ${path}    10

File In VM Should Contain
    [Documentation]    Asserts that ${path} on the outer VM contains ${text}.
    [Arguments]    ${path}    ${text}
    Run In VM And Check    cat ${path} | grep -F "${text}"    10

Enable NFS
    [Arguments]    ${cluster_id}
    Log To Console    [nfs] Enabling NFS cluster ${cluster_id}...
    Run In VM And Check    sudo microceph enable nfs --cluster-id ${cluster_id} --wait    120

Disable NFS
    [Arguments]    ${cluster_id}
    Log To Console    [nfs] Disabling NFS cluster ${cluster_id}...
    Run In VM And Check    sudo microceph disable nfs --cluster-id ${cluster_id}    60

Create NFS FS Volume
    [Arguments]    ${volume_name}
    Log To Console    [nfs] Creating CephFS volume ${volume_name}...
    Run In VM And Check    sudo microceph.ceph fs volume create ${volume_name}    120

Create NFS Export
    [Arguments]    ${cluster_id}    ${fsname}
    Log To Console    [nfs] Creating NFS export for ${cluster_id}/${fsname}...
    Run In VM And Check    sudo microceph.ceph nfs export create cephfs ${cluster_id} /${fsname}dir ${fsname}    60

# ---------------------------------------------------------------------------
# Upgrade helpers
# ---------------------------------------------------------------------------

Upgrade Multi Node
    [Documentation]    Upgrades all 4 inner containers to the local snap build.
    Log To Console    [upgrade] Upgrading all nodes to local snap build...
    FOR    ${container}    IN    node-wrk0    node-wrk1    node-wrk2    node-wrk3
        Log To Console    [upgrade] Upgrading ${container}...
        Run In VM And Check    lxc exec ${container} -- sh -c "sudo snap install --dangerous /mnt/microceph_*.snap"    600
        Run In VM And Check    lxc exec ${container} -- sh -c "snap connect microceph:block-devices && snap connect microceph:hardware-observe && snap connect microceph:mount-observe"    60
        Sleep    15s
        FOR    ${i}    IN RANGE    36
            ${res}=    Run In VM    lxc exec ${container} -- sh -c "microceph.ceph osd status 2>/dev/null | grep -c 'exists,up' || echo 0"    30
            ${count}=    Evaluate    int('${res.stdout.strip()}') if '${res.stdout.strip()}'.isdigit() else 0
            IF    ${count} >= 3    BREAK
            Sleep    10s
        END
        ${res}=    Run In VM    lxc exec ${container} -- sh -c "microceph.ceph osd status 2>/dev/null | grep -c 'exists,up' || echo 0"    30
        ${count}=    Evaluate    int('${res.stdout.strip()}') if '${res.stdout.strip()}'.isdigit() else 0
        Should Be Equal As Integers    ${count}    3    msg=Expected 3 OSD up after upgrading ${container}
    END

Refresh Multi Node Snap
    [Documentation]    Refreshes all 4 inner containers from the Snap Store channel.
    [Arguments]    ${channel}
    FOR    ${container}    IN    node-wrk0    node-wrk1    node-wrk2    node-wrk3
        Run In VM And Check    lxc exec ${container} -- sudo snap refresh microceph --channel ${channel}    300
    END

# ---------------------------------------------------------------------------
# Multi-site replication helpers
# ---------------------------------------------------------------------------

Bootstrap Two Sites
    [Documentation]    Bootstraps sitea (node-wrk0/1) and siteb (node-wrk2/3) each with 2 loop-file OSDs.
    ...    Used by both RBD and CephFS replication suites.
    Log To Console    [replication] Bootstrapping sitea (node-wrk0/1)...
    Run In Container    node-wrk0    microceph cluster bootstrap    120
    Run In Container    node-wrk0    microceph disk add loop,1G,2    120
    ${tok}=    Run In VM    lxc exec node-wrk0 -- microceph cluster add node-wrk1    60
    Run In Container    node-wrk1    microceph cluster join ${tok.stdout.strip()}    120
    Run In Container    node-wrk1    microceph disk add loop,1G,2    120
    Sleep    10s
    Log To Console    [replication] Bootstrapping siteb (node-wrk2/3)...
    Run In Container    node-wrk2    microceph cluster bootstrap    120
    Run In Container    node-wrk2    microceph disk add loop,1G,2    120
    ${tok2}=    Run In VM    lxc exec node-wrk2 -- microceph cluster add node-wrk3    60
    Run In Container    node-wrk3    microceph cluster join ${tok2.stdout.strip()}    120
    Run In Container    node-wrk3    microceph disk add loop,1G,2    120
    Sleep    10s

Exchange Remote Site Tokens
    [Documentation]    Exports tokens from each site and imports them cross-site with --local-name.
    ...    After this call, sitea knows siteb and siteb knows sitea.
    Log To Console    [replication] Exchanging site tokens...
    ${sitea_token}=    Run In VM    lxc exec node-wrk0 -- microceph cluster export siteb    60
    ${siteb_token}=    Run In VM    lxc exec node-wrk2 -- microceph cluster export sitea    60
    Run In Container    node-wrk0    microceph remote import siteb ${siteb_token.stdout.strip()} --local-name=sitea    60
    Run In Container    node-wrk2    microceph remote import sitea ${sitea_token.stdout.strip()} --local-name=siteb    60
    Sleep    15s    reason=Allow remote credentials to propagate across the cluster

Verify Remote Authentication On All Nodes
    [Documentation]    Verifies ceph commands against the remote cluster work on all 4 nodes.
    Log To Console    [replication] Verifying remote authentication on all nodes...
    Run In Container    node-wrk0    microceph.ceph -s    30
    Run In Container    node-wrk0    microceph.ceph -s --cluster siteb --id sitea    30
    Run In VM And Check    lxc exec node-wrk0 -- microceph remote list --json | jq -e 'any(.[]; .name == "siteb")'    30
    Run In Container    node-wrk1    microceph.ceph -s    30
    Run In Container    node-wrk1    microceph.ceph -s --cluster siteb --id sitea    30
    Run In VM And Check    lxc exec node-wrk1 -- microceph remote list --json | jq -e 'any(.[]; .local_name == "sitea")'    30
    Run In Container    node-wrk2    microceph.ceph -s    30
    Run In Container    node-wrk2    microceph.ceph -s --cluster sitea --id siteb    30
    Run In VM And Check    lxc exec node-wrk2 -- microceph remote list --json | jq -e 'any(.[]; .name == "sitea")'    30
    Run In Container    node-wrk3    microceph.ceph -s    30
    Run In Container    node-wrk3    microceph.ceph -s --cluster sitea --id siteb    30
    Run In VM And Check    lxc exec node-wrk3 -- microceph remote list --json | jq -e 'any(.[]; .local_name == "siteb")'    30

Node Is In Mon List
    [Documentation]    Returns "yes" if ${node} appears in the mon daemons line of ceph -s via ${head_node}.
    [Arguments]    ${node}    ${head_node}=node-wrk0
    ${result}=    Run In VM    lxc exec ${head_node} -- sh -c "microceph.ceph -s | grep -q 'mon: .*daemons.*${node}' && echo yes || echo no"    30
    RETURN    ${result.stdout.strip()}

Wait For N Nodes In Cluster
    [Documentation]    Polls microceph status on ${head_node} until at least ${n} nodes appear (8 x 2 s).
    [Arguments]    ${n}    ${head_node}=node-wrk0
    FOR    ${i}    IN RANGE    8
        ${res}=    Run In VM    lxc exec ${head_node} -- sh -c "microceph status | grep -cE '^- node' || true"    30
        IF    int('${res.stdout.strip()}') >= ${n}    RETURN
        Sleep    2s
    END
    Fail    Cluster did not reach ${n} node(s) after 16 s

Mount CephFS From Container
    [Documentation]    Pulls ceph.conf and keyring from ${container} into /etc/ceph/ on the outer VM,
    ...    then mounts the CephFS volume ${vol} at ${mountpoint}.
    [Arguments]    ${container}    ${mountpoint}    ${vol}
    Run In VM And Check    sudo lxc file pull ${container}/var/snap/microceph/current/conf/ceph.conf /etc/ceph/    30
    Run In VM And Check    sudo lxc file pull ${container}/var/snap/microceph/current/conf/ceph.keyring /etc/ceph/    30
    Run In VM And Check    sudo mkdir -p ${mountpoint}    10
    Run In VM And Check    sudo mount -t ceph :/ ${mountpoint} -o name=admin,fs=${vol}    60

Get Synced Image Count On Node
    [Documentation]    Returns string: total images across all RBD replication entries on ${node}.
    ...    jq expression: '[.[].Images | length] | add // 0'
    [Arguments]    ${node}
    ${result}=    Run In VM    lxc exec ${node} -- sh -c "sudo microceph replication list rbd --json | jq '[.[].Images | length] | add // 0'"    30
    RETURN    ${result.stdout.strip()}

Get Primary Image Count On Node
    [Documentation]    Returns string: count of RBD images where is_primary==true on ${node}.
    [Arguments]    ${node}
    ${result}=    Run In VM    lxc exec ${node} -- sh -c "sudo microceph replication list rbd --json | jq '[.[].Images[] | select(.is_primary==true)] | length'"    30
    RETURN    ${result.stdout.strip()}

Get RBD Mirror Pool Health
    [Documentation]    Returns the health string from rbd mirror pool status on ${node} for ${pool}.
    [Arguments]    ${node}    ${pool}
    ${result}=    Run In VM    lxc exec ${node} -- sh -c "sudo microceph.rbd mirror pool status ${pool} --format json 2>/dev/null | grep -o '\"health\"[[:space:]]*:[[:space:]]*\"[^\"]*\"' | head -1 | sed 's/.*\"\\([^\"]*\\)\"$/\\1/' || echo UNKNOWN"    15
    RETURN    ${result.stdout.strip()}

Wait For CephFS Replication List Non Empty
    [Documentation]    Polls until the CephFS replication list for ${vol} on ${node} is non-empty.
    [Arguments]    ${node}    ${vol}    ${attempts}=50
    FOR    ${i}    IN RANGE    ${attempts}
        ${empty}=    Run In VM    lxc exec ${node} -- sh -c "sudo microceph replication list cephfs --json | jq '.${vol} | . == {}'"    30
        IF    "${empty.stdout.strip()}" == "false"    RETURN
        IF    ${i} == ${attempts} - 1    Fail    CephFS replication list for ${vol} still empty after ${attempts} attempts
        Sleep    5s
    END

Wait For CephFS Snaps Synced
    [Documentation]    Polls until total snaps_synced for volume ${vol} on ${node} reaches ${threshold}.
    ...    NOTE: jq runs on the outer VM (not inside the container) — the lxc exec output is piped
    ...    to the outer VM's jq binary. This is intentional and must be preserved.
    [Arguments]    ${node}    ${vol}    ${threshold}    ${attempts}=100
    FOR    ${i}    IN RANGE    ${attempts}
        ${result}=    Run In VM    lxc exec ${node} -- sh -c "microceph replication status cephfs ${vol} --json" | jq '[.peers[].mirror_status | .[] | .snaps_synced // 0] | add // 0'    30
        ${total}=    Evaluate    int('${result.stdout.strip()}') if '${result.stdout.strip()}'.isdigit() else 0
        IF    ${total} >= ${threshold}    RETURN
        Sleep    5s
    END
    Fail    CephFS snaps_synced for ${vol} never reached ${threshold} after ${attempts} attempts

Read File In VM
    [Documentation]    Returns the result of running cat ${path} on the outer VM.
    [Arguments]    ${path}
    ${result}=    Run In VM    cat ${path}    10
    RETURN    ${result}

Wait For Pool Crush Rule
    [Documentation]    Polls osd pool ls detail until at least one pool carries the given crush_rule id
    ...    (30 tries × 2 s = 60 s max). Mirrors actionutils.sh wait_for_pool_crush_rule.
    ...    Required after the failure-domain auto-switch because the mgr-created .mgr pool may not yet
    ...    exist, or its crush_rule migration may still be in flight, when this check runs.
    [Arguments]    ${rule_id}    ${tries}=30
    Log To Console    [crush] Waiting for pool with crush_rule ${rule_id}...
    FOR    ${i}    IN RANGE    ${tries}
        ${result}=    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph osd pool ls detail 2>/dev/null || true"    30
        IF    "crush_rule ${rule_id}" in $result.stdout
            Log To Console    [crush] Found pool with crush_rule ${rule_id}
            RETURN
        END
        Sleep    2s
    END
    Run In VM    lxc exec node-wrk0 -- sh -c "microceph.ceph osd pool ls detail || true"    30
    Fail    No pool reached crush_rule ${rule_id} after ${tries} tries

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

# ---------------------------------------------------------------------------
# Single-node test helpers
# ---------------------------------------------------------------------------

Poll Ceph Status Contains
    [Documentation]    Polls `ceph status` on the outer VM until the output contains ${substring}.
    ...    Fails if the condition is not met within ${tries} × ${sleep} seconds.
    [Arguments]    ${substring}    ${tries}=16    ${sleep}=15s
    FOR    ${i}    IN RANGE    ${tries}
        ${result}=    Run In VM    sudo microceph.ceph status    30
        Log    Attempt ${i}: ${result.stdout}
        IF    "${substring}" in $result.stdout
            Log To Console    [status] PASS: '${substring}' found (attempt ${i})
            RETURN
        END
        IF    ${i} == ${tries} - 1
            Fail    ceph status never contained '${substring}' after ${tries} attempts
        END
        Sleep    ${sleep}
    END

Verify Mount Check
    [Documentation]    Verifies that adding a mounted root device fails with "is currently mounted",
    ...    even when --wipe is specified. Mirrors actionutils.sh verify_mount_check.
    Log To Console    [disk] Testing mount check...
    ${rootdev}=    Run In VM    findmnt --noheadings --output SOURCE /    30
    ${err}=    Run In VM    sudo microceph disk add --wipe ${rootdev.stdout.strip()} 2>&1 || true    30
    Should Contain    ${err.stdout}    is currently mounted    msg=Expected mount check failure for ${rootdev.stdout.strip()}

Test RGW Stale Run Dir Migration
    [Documentation]    Injects a stale run dir into radosgw.conf, restarts the snap, and verifies
    ...    the daemon repairs the config to use the stable 'current' symlink.
    ...    Mirrors actionutils.sh test_rgw_stale_run_dir_migration.
    Log To Console    [rgw] Testing RGW stale run dir migration...
    Run In VM And Check    sudo sed -i 's|run dir = .*|run dir = /var/snap/microceph/1/run|' /var/snap/microceph/current/conf/radosgw.conf    10
    Run In VM And Check    grep "run dir" /var/snap/microceph/current/conf/radosgw.conf    10
    Run In VM And Check    sudo snap stop microceph    120
    Run In VM And Check    sudo snap start microceph    60
    Wait For RGW    1
    Run In VM And Check    grep -q "run dir = /var/snap/microceph/current/run" /var/snap/microceph/current/conf/radosgw.conf    10

# ---------------------------------------------------------------------------
# Teardown
# ---------------------------------------------------------------------------

Collect MicroCeph Diagnostics
    [Documentation]    Collects diagnostics from the outer VM; errors are ignored.
    ${r}=    Run In VM    sudo microceph status 2>/dev/null || true
    Log    microceph status: ${r.stdout}
    ${r}=    Run In VM    sudo microceph.ceph -s 2>/dev/null || true
    Log    ceph -s: ${r.stdout}
    ${r}=    Run In VM    sudo snap logs microceph -n 200 2>/dev/null || true
    Log    snap logs: ${r.stdout}

Destroy LXD Instances
    [Documentation]    Force-stops and force-deletes the outer VM.
    Log    Destroying outer VM: ${OUTER_VM}
    ${stop}=    Set Variable    bash -c "lxc stop ${OUTER_VM} --force 2>/dev/null || true"
    Run Process    ${stop}    shell=True    timeout=60
    ${del}=    Set Variable    bash -c "lxc delete ${OUTER_VM} --force 2>/dev/null || true"
    Run Process    ${del}    shell=True    timeout=60
    Log    Outer VM ${OUTER_VM} destroyed

Detach Loop Devices
    ${cmd}=    Set Variable    bash -c "losetup -a | grep -E 'mctest-' | cut -d: -f1 | xargs -r losetup -d 2>/dev/null || true"
    Run Process    ${cmd}    shell=True

Teardown MicroCeph Environment
    [Documentation]    Always-run suite teardown: collect diagnostics then destroy VM.
    Run Keyword And Ignore Error    Collect MicroCeph Diagnostics
    Run Keyword And Ignore Error    Destroy LXD Instances
    Run Keyword And Ignore Error    Detach Loop Devices

Clear IPTables
    [Documentation]    Clears Docker-injected iptables FORWARD rules inside the outer VM.
    Run In VM And Check    sudo iptables -P FORWARD ACCEPT || true    10
    Run In VM And Check    sudo ip6tables -P FORWARD ACCEPT || true    10
    Run In VM And Check    sudo iptables -F FORWARD || true    10
    Run In VM And Check    sudo ip6tables -F FORWARD || true    10

Free Runner Disk
    [Documentation]    Removes large packages from the outer VM to reclaim disk space.
    Run In VM And Check    sudo rm -rf /usr/local/lib/android /usr/local/.ghcup; imgs=$(sudo docker images -q 2>/dev/null || true); [ -n "$imgs" ] && sudo docker rmi $imgs || true    120

Install Hurl
    [Documentation]    Installs hurl HTTP test runner (v5.0.1) on the outer VM.
    Log To Console    [setup] Installing hurl...
    Run In VM And Check    bash -c "curl --location --remote-name https://github.com/Orange-OpenSource/hurl/releases/download/5.0.1/hurl_5.0.1_amd64.deb && sudo dpkg -i ./hurl_5.0.1_amd64.deb"    120

Prepare Disk API Hurl Fixtures
    [Documentation]    Creates loop devices and lists disks for hurl API tests.
    Log To Console    [setup] Preparing disk API hurl fixtures...
    Create Loop Devices
    Run In VM And Check    sudo microceph disk list --json    30

Run Hurl
    [Documentation]    Runs a hurl test file against the MicroCeph control socket.
    [Arguments]    ${hurl_file}
    Log To Console    [hurl] Running ${hurl_file}...
    Run In VM And Check    sudo hurl --unix-socket /var/snap/microceph/common/state/control.socket --test ${hurl_file}    120
