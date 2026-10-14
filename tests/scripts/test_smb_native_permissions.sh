#!/bin/bash
# Early strict-confinement prerequisite test, NOT native SMB qualification.
# Run only inside a newly created, approved disposable LXD VM with this name.
# Requires gcc/libc6-dev, core26 and snapd >= 2.78 inside that VM.
set -euo pipefail
[[ $(hostname) == smb-native-20261013-* ]] || {
    echo 'Refusing to run outside the approved disposable VM prefix' >&2
    exit 1
}
[[ $(id -u) == 0 ]] || { echo 'Run as root inside the disposable VM' >&2; exit 1; }
if snap list microceph >/dev/null 2>&1; then
    echo 'Refusing to replace an existing MicroCeph installation' >&2
    exit 1
fi
work=$(mktemp -d /root/smb-native-permissions.XXXXXX)
echo "Diagnostic artifacts: $work"
mkdir -p "$work/prime/meta"
cat > "$work/probe.c" <<'C'
#define _GNU_SOURCE
#include <errno.h>
#include <grp.h>
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/types.h>
#include <unistd.h>

static void require(int ok, const char *operation) {
    if (!ok) {
        fprintf(stderr, "FAIL %s: %s\n", operation, strerror(errno));
        exit(1);
    }
    printf("PASS %s\n", operation);
    fflush(stdout);
}

int main(int argc, char **argv) {
    require(argc == 2, "one fixed probe role");
    if (!strcmp(argv[1], "identity")) {
        gid_t groups[] = {12346, 12347};
        require(setgroups(2, groups) == 0, "nonempty supplementary groups");
        require(setgid(12345) == 0, "switch primary gid");
        require(setuid(12345) == 0, "switch uid");
        gid_t actual[2];
        require(getgroups(2, actual) == 2 && actual[0] == groups[0] &&
                actual[1] == groups[1], "supplementary groups survive switch");
        require(getgid() == 12345 && getuid() == 12345, "identity readback");
    } else if (!strcmp(argv[1], "mount")) {
        require(unshare(CLONE_NEWNS) == 0, "child mount namespace");
        require(mount(NULL, "/", NULL, MS_REC | MS_SLAVE, NULL) == 0,
                "nonpropagating mount namespace");
        char source[4096];
        require(snprintf(source, sizeof(source), "%s/smb/probe/config/resolv.conf",
                         getenv("SNAP_COMMON")) < (int)sizeof(source), "source path");
        require(mount(source, "/etc/resolv.conf", NULL, MS_BIND, NULL) == 0,
                "private resolver bind");
        FILE *file = fopen("/etc/resolv.conf", "r");
        require(file != NULL, "read private resolver");
        char line[256];
        require(fgets(line, sizeof(line), file) != NULL &&
                !strcmp(line, "nameserver 192.0.2.53\n"), "private resolver contents");
        fclose(file);
    } else {
        errno = EINVAL;
        require(0, "known role");
    }
    return 0;
}
C
gcc -static -Wall -Wextra -Werror -o "$work/prime/probe" "$work/probe.c"
cat > "$work/prime/meta/snap.yaml" <<'YAML'
name: microceph
version: native-permission-probe
summary: Disposable native SMB permission prerequisite probe
description: Diagnostic only, not the MicroCeph native SMB implementation.
base: core26
architectures: [amd64]
confinement: strict
grade: devel
assumes: [snapd2.78]
plugs:
  smb-identity:
    interface: microceph-support
    user-identity-switching: true
  smb-mounts:
    interface: mount-control
    mount:
      - what: /var/snap/microceph/common/smb/*/config/resolv.conf
        where: /etc/resolv.conf
        options: [bind]
      - what: /var/snap/microceph/common/smb/*/config/resolv.conf
        where: /run/systemd/resolve/stub-resolv.conf
        options: [bind]
apps:
  mount:
    command: probe mount
    plugs: [network-control, smb-mounts]
  identity:
    command: probe identity
    plugs: [smb-identity]
YAML
snap pack "$work/prime" "$work"
snap install --dangerous "$work/microceph_native-permission-probe_amd64.snap"
snap connect microceph:network-control
snap connect microceph:smb-identity
snap connect microceph:smb-mounts
snap connections microceph
mkdir -p /var/snap/microceph/common/smb/probe/config
printf 'nameserver 192.0.2.53\n' > /var/snap/microceph/common/smb/probe/config/resolv.conf
chmod 600 /var/snap/microceph/common/smb/probe/config/resolv.conf
sha256sum /etc/resolv.conf > "$work/host-resolver.before"
failed=0
for role in mount identity; do
    echo "Testing $role under strict confinement"
    snap run "microceph.$role" || failed=1
done
sha256sum /etc/resolv.conf > "$work/host-resolver.after"
cmp "$work/host-resolver.before" "$work/host-resolver.after"
echo 'Preserving installed diagnostic snap and policy; no cleanup on failure.'
exit "$failed"
