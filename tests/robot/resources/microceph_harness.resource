*** Settings ***
Documentation    Shared resource for MicroCeph Robot Framework tests.
...    All test commands run directly inside the outer LXD VM (no actionutils.sh dependency).
Library         OperatingSystem
Library         Process
Library         String
Library         Collections
Library         microceph_harness.py
Library         streaming_process.py
Library         snap_services.py
Library         cephfs_replication.py

*** Variables ***
${SNAP_PATH}          ${EMPTY}
${OUTER_VM}           microceph-test-vm
${OUTER_VM_DISK}      50GiB
${OUTER_VM_CPU}            4
${OUTER_VM_MEMORY}         6GiB
${OUTER_VM_IMAGE}         ubuntu:24.04
${INNER_NODE_IMAGE}       ubuntu:22.04
${LXD_STORAGE_LOOP_SIZE}   25
${REPO_ROOT}          ${CURDIR}/../../..
# Default for bash -x tracing; override with --variable XTRACE:True.
${XTRACE}             ${False}

*** Keywords ***

# ---------------------------------------------------------------------------
# Single-node MicroCeph setup
# ---------------------------------------------------------------------------

Install And Bootstrap MicroCeph
    [Documentation]    Installs snap and bootstraps the cluster.
    [Arguments]    ${mon_ip}=${EMPTY}
    Install MicroCeph From Local Snap
    Bootstrap MicroCeph Cluster    ${mon_ip}

Enable DM Crypt
    [Documentation]    Connects the dm-crypt snap interface and restarts microceph.daemon.
    Run In VM And Check    sudo snap connect microceph:dm-crypt    30
    Run In VM And Check    sudo snap restart microceph.daemon    60

# ---------------------------------------------------------------------------
# Multi-node LXD container setup
# ---------------------------------------------------------------------------

Provision Multinode VM
    [Documentation]    Launches the outer VM and provisions LXD + 4 inner MicroCeph nodes.
    ...    The sequence itself is not retried, so setup failures surface instead of being
    ...    masked (Launch Outer Test VM retries the bare lxc launch internally).
    [Arguments]    ${vm_name}    ${disk_size}    ${network_type}=public
    Launch Outer Test VM    vm_name=${vm_name}    disk_size=${disk_size}
    Copy Scripts To VM
    Copy Snap To VM
    Clear IPTables
    Free Runner Disk
    Setup LXD In VM
    Create LXD Containers With Loop Devices    ${network_type}
    Install MicroCeph On All Nodes

Setup LXD In VM
    [Documentation]    Installs and initialises LXD inside the outer VM.
    Log To Console    [setup] Setting up LXD inside ${OUTER_VM} (may take several minutes)...
    ${check}=    Run In VM    sudo snap list | grep -cF "lxd" || true    30
    IF    "${check.stdout.strip()}" != "1"
        Run In VM And Check    sudo snap install lxd    300
    END
    Run In VM And Check    sudo snap refresh    300
    Run In VM And Check    sudo snap set lxd daemon.group=adm    30
    # Force a btrfs (copy-on-write) storage pool. On the default "dir" backend every
    # lxc init / publish is a full rootfs copy, which serializes ~6 x 1.5GB copies in
    # multi-node setup; btrfs makes instance creation a near-instant reflink snapshot.
    # Size the loop file explicitly: the auto default (~20% of free space, capped at
    # 30GB) can be too small for the base image + 4 node rootfs deltas + loop OSDs
    # that replication suites create inside container rootfs.
    Run In VM And Check    sudo lxd init --auto --storage-backend btrfs --storage-create-loop ${LXD_STORAGE_LOOP_SIZE}    60

# ---------------------------------------------------------------------------
# OSD / disk operations
# ---------------------------------------------------------------------------

Add OSD To Node
    [Documentation]    Adds /dev/sdia as an OSD on the specified inner container.
    [Arguments]    ${container}
    Log To Console    [osd] Adding OSD to ${container}...
    Exec In Container    ${container}    microceph    disk    add    /dev/sdia    --wipe    timeout=120    check=True
    Sleep    1s

# ---------------------------------------------------------------------------
# RGW operations
# ---------------------------------------------------------------------------

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
    # Diagnostic only -- Unchecked so it never raises. systemctl status exits 3 when the unit
    # is inactive; a raising call here would newly fail the ungated post-upgrade caller
    # (Test Exercise RGW After Upgrade) if RGW has not finished restarting after the upgrade.
    Run In Container Unchecked    node-wrk0    sudo systemctl status snap.microceph.rgw --no-pager    30
    ${user_exists}=    Run In Container Unchecked    node-wrk0    microceph.radosgw-admin user list | grep -q test && echo yes || echo no    30
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

Verify Cluster Health
    [Documentation]    Polls microceph.ceph health until HEALTH_OK on the outer VM.
    Wait For Cluster Health OK

Verify Cluster Health Head Node
    [Documentation]    Polls microceph.ceph health until HEALTH_OK via node-wrk0.
    Wait For Cluster Health OK    node-wrk0

# ---------------------------------------------------------------------------
# NFS operations
# ---------------------------------------------------------------------------

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
# Teardown
# ---------------------------------------------------------------------------

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

# ---------------------------------------------------------------------------
# Deferred-Ceph / role-managed placement helpers
#
# The API access, polling, and assertion keywords (MicroCeph API Get/Put/
# Delete, Assert Lifecycle State, Wait For ..., Assert Member Has Control
# Services, ...) live in microceph_harness.py, with their parse decisions in
# placement_status.py. Only linear "do X" lifecycle keywords remain here.
# ---------------------------------------------------------------------------

Deferred Bootstrap Head Node
    [Documentation]    Bootstraps MicroCluster only (Ceph deferred) on node-wrk0.
    ...    Mirrors Bootstrap Head Node but adds --defer-ceph and does NOT expect Ceph services.
    ...    Ceph network flags are rejected with --defer-ceph, so --public-network is NOT passed
    ...    here; the public network CIDR is captured into ${DEFERRED_PUBLIC_NETWORK} and applied
    ...    later by `cluster bootstrap-ceph --public-network` (see Ceph Only Bootstrap Target).
    Log To Console    [deferred-ceph] Deferred bootstrap on node-wrk0...
    ${result}=    Run In VM    lxc network list --format=csv | grep 'public' | cut -d, -f4    30
    ${nw}=    Set Variable    ${result.stdout.strip()}
    Set Suite Variable    ${DEFERRED_PUBLIC_NETWORK}    ${nw}
    Run In Container    node-wrk0    microceph cluster bootstrap --defer-ceph    120
    Sleep    5s
    Run In Container    node-wrk0    microceph status    30

Deferred Join Worker Nodes
    [Documentation]    Joins node-wrk1..3 to the cluster with --defer-ceph (no Ceph auto-placement).
    Log To Console    [deferred-ceph] Deferred join workers...
    FOR    ${i}    IN    1    2    3
        ${tok_result}=    Run In VM    lxc exec node-wrk0 -- microceph cluster add node-wrk${i}    60
        ${tok}=    Set Variable    ${tok_result.stdout.strip()}
        Run In Container    node-wrk${i}    microceph cluster join --defer-ceph ${tok}    120
    END
    Wait For N Nodes In Cluster    4
    Run In Container    node-wrk0    microceph status    30

Ceph Only Bootstrap Target
    [Documentation]    Runs `microceph cluster bootstrap-ceph --target <member> --public-network=<nw>`
    ...    from node-wrk0 and waits for Ceph to come up on the target. Returns nothing.
    ...    The public network (${DEFERRED_PUBLIC_NETWORK}, captured by Deferred Bootstrap Head Node)
    ...    is applied here because --defer-ceph rejects Ceph network flags on `cluster bootstrap`.
    [Arguments]    ${target}
    Log To Console    [deferred-ceph] Ceph-only bootstrap targeting ${target}...
    Run In Container    node-wrk0    microceph cluster bootstrap-ceph --target ${target} --public-network=${DEFERRED_PUBLIC_NETWORK}    300
    Sleep    5s
