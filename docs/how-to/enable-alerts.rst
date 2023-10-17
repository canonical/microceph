=======================================
Enabling Prometheus Alertmanager alerts
=======================================

Pre-Requisite
-------------
In order to configure alerts, your MicroCeph deployment must enable metrics collections with Prometheus. Follow :doc:`this How-To <enable-metrics>` if you haven't configured it. Also, Alertmanager is distributed as a separate binary which should be installed and running.

Introduction
------------

Prometheus Alertmanager handles alerts sent by the Prometheus server. It takes care of deduplicating, grouping, and routing them to the correct receiver integration such as email. It also takes care of silencing and inhibition of alerts.

Alerts are configured using `Alerting Rules <https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/>`_. These rules allows the user to define alert conditions using Prometheus expressions. Ceph is designed to be configurable with Alertmanager, you can use the default set of alerting rules provided below to get basic alerts from your MicroCeph deployments.

The default alert rules can be downloaded from :download:`here <assets/prometheus_alerts.yaml>`

Configuring Alert rules
-----------------------

Alerting rules and Alertmanager targets are configured in Prometheus using the same config file we used to configure scraping targets.

A simple configuration file with scraping targets, Alertmanager and alerting rules is provided below:

..  code-block:: yaml

    # microceph.yaml
    global:
        external_labels:
            monitor: 'microceph'

    # Scrape Job
    scrape_configs:
      - job_name: 'microceph'

        # Ceph's default for scrape_interval is 15s.
        scrape_interval: 15s

        # List of all the ceph-mgr instances along with default (or configured) port.
        static_configs:
        - targets: ['10.245.165.103:9283', '10.245.165.205:9283', '10.245.165.94:9283']

    rule_files: # path to alerting rules file.
      - /home/ubuntu/prometheus_alerts.yaml

    alerting:
      alertmanagers:
        - static_configs:
          - targets: # Alertmanager <HOST>:<PORT>
            - "10.245.167.132:9093"

Start Prometheus with provided configuration file.

..  code-block:: none

    prometheus --config.file=microceph.yaml

Click on the 'Alerts' tab on Prometheus dashboard to view the configured alerts:

.. figure:: assets/alerts

Look we already have an active 'CephHealthWarning' alert! (shown in red) while the other configured alerts are inactive (shown in green). Hence, Alertmanager is configured and working.
