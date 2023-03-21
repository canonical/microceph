Install Microceph using LXD
======================================================


Setup VMs
~~~~~~~~~

.. code-block:: shell

    lxc launch images:ubuntu/22.04/cloud microceph-1 --vm
    lxc launch images:ubuntu/22.04/cloud microceph-2 --vm
    lxc launch images:ubuntu/22.04/cloud microceph-3 --vm

Add storage
~~~~~~~~~~~

.. code-block:: shell

    for i in $(seq 1 3); do
        lxc storage volume create default osd-$i --type block size=10GiB
        lxc config device add microceph-$i osd-$i disk pool=default source=osd-$i
    done

Prepare VMs
~~~~~~~~~~~

.. code-block:: shell

    for i in $(seq 1 3); do
        lxc exec microceph-$i -- sh -c 'apt-get update; DEBIAN_FRONTEND=noninteractive apt-get upgrade -yq; DEBIAN_FRONTEND=noninteractive apt-get install snapd -yq; sudo snap install snapd; echo dm_crypt | tee -a /etc/modules; reboot'
    done

Install Microceph
~~~~~~~~~~~~~~~~~

.. code-block:: shell

    for i in $(seq 1 3); do
        lxc exec microceph-$i -- sh -c 'snap install microceph'
    done
