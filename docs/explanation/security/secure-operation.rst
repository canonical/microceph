===================================
Best practices for secure operation
===================================

Maintaining security is an ongoing process.

Vulnerability management
------------------------

* Monitor Advisories: Actively track CVEs and security advisories for:  

  * Ceph (via Ceph announce list, security trackers).  
  * MicroCeph snap (check snap channels/updates).  
  * Host OS (use relevant security advisories for the host OS, e.g., USNs for Ubuntu).  

* Patch Management: Implement a process for testing and applying security patches promptly
  using sudo snap refresh microceph and the host OS's package manager
  (e.g., apt update && apt upgrade for Debian/Ubuntu). Use snap channels
  (e.g., the /candidate channel) for testing before refreshing stable.

Incident response
-----------------

* Develop a Plan: Have a documented Incident Response (IR) plan for your
  MicroCeph environment, including steps related to microcephd and the dqlite database.  
* Define Steps: Cover detection, containment (e.g., firewalling the host,
  stopping services like snap.microceph.daemon), eradication, recovery
  (potentially involving database restoration if needed), and post-mortem analysis.  
* Practice: Test the plan periodically.

Perform audits
--------------

* Regular Checks: Conduct periodic security audits of the MicroCeph host, configuration,
  and data directories.  
* Validate Controls: Verify firewall rules, Ceph configuration, microcephd status and
  configuration, Cephx permissions (sudo microceph.ceph auth ls), OS access controls
  (sudo rules, SSH keys, file permissions on /var/snap/microceph/), and encryption settings.

Perform upgrades
----------------

* Stay Current: Regularly upgrade MicroCeph (sudo snap refresh microceph), snapd
  (sudo snap refresh snapd), and the underlying OS (using the host's package manager) for
  security patches and features. Upgrading the MicroCeph snap updates Ceph, microcephd,
  dqlite, and Microcluster together.  
* Schedule Proactively: Plan and test upgrades, especially for security vulnerabilities.
  Utilize snap channels for pre-production testing.

Release notes
-------------

* Always read the release notes for Ceph versions included in MicroCeph snap updates,
  the MicroCeph snap itself, and the host OS before upgrading or making significant changes,
  as they contain information about security fixes, new features, and potential issues.
