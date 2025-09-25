====================================
Best practices for secure deployment
====================================

Incorporate security from the initial setup of your MicroCeph instance.

Network architecture
--------------------

* Segmentation: If the MicroCeph host has multiple network interfaces, configure Ceph's
  public_network and cluster_network settings appropriately (check MicroCeph docs for details),
  configure the microcephd listen/advertise addresses if needed for clustering, and use the firewall
  to enforce segregation between client access networks, cluster networks, and management networks.  
* As a best practice, use firewalling or VLANs to segregate into these zones:  

  * External (optional): If applicable, expose specific endpoints for external untrusted consumption, e.g. RGW.  
  * Storage Access: Client access (including RGW if no external access provided), MON access.  
  * Cluster Network: OSD replication and heartbeat traffic. Isolating this improves performance and security.  

* Firewalls: Implement strict firewall rules (e.g. using iptables, nftables) on all nodes:  

  * Deny all traffic by default.  
  * Allow only necessary ports between specific hosts/networks (refer to the port table).  
  * Restrict access to management interfaces (SSH, Juju, Dashboard) to trusted administrative networks.

Minimum privileges
------------------


* Cephx Keys: Create dedicated Cephx keys for each client/application with minimal capabilities.
  Don't use client.admin routinely.  
* OS Users: Limit sudo access on the host machine. Restrict who can run microceph commands.
  Run other applications on the host as unprivileged users. Protect access to the snap's
  data directories.  
* Explicit Assignment: Ensure all access relies on explicit permissions/capabilities
  rather than default permissive settings.

Auditing and centralized logging
--------------------------------

* Enable Auditing:  

  * Configure Ceph logging levels via Ceph configuration options
    (e.g., log_to_file \= true, debug_mon, debug_osd). Check MicroCeph
    documentation for how to set these. Ceph logs are found in /var/snap/microceph/common/logs/ceph/.  
  * microcephd logs to /var/log/syslog, see the MicroCeph documentation for details on setting log levels.  

* Centralized Logging: Configure host-level standard log shipping mechanisms
  (e.g., rsyslog, journald forwarding) to send Ceph logs, microcephd logs,
  and host system logs (syslog, auth.log, kern.log, journald) to a central
  logging system (like Loki or ELK).  
* Monitor and Audit: Regularly review logs for anomalies and security events
  (e.g., repeated auth failures, crashes, microcephd errors).

Alerting
--------

* Configure Monitoring: Enable the Prometheus MGR module
  (sudo microceph.ceph mgr module enable Prometheus) and configure it if necessary
  via Ceph MGR configuration options (e.g., sudo microceph.ceph config set mgr mgr/prometheus/...).  
* Security Alerts: Configure alerts for security anomalies and health issues such as:  

  * Ceph health status changes (HEALTH_WARN, HEALTH_ERR).  
  * Ceph daemon crashes or restarts (via systemd unit status or logs).  
  * microcephd service failures or restarts.  
  * Significant performance deviations.  
  * Host system issues (CPU, RAM, Disk I/O).
