*** Settings ***
Documentation    Blocking compatibility gate for the stable snapd channel.
...    The MicroCeph snap requires snapd 2.78 for scoped identity switching.
...    This suite remains red until that feature reaches stable, making the merge
...    blocker explicit while the rest of CI exercises the configured edge channel.
Resource         ../resources/microceph_harness.resource
Suite Setup      Snapd Stable Compatibility Suite Setup
Suite Teardown   Teardown MicroCeph Environment
Test Tags        snapd    compatibility    stable    lxd    integration

*** Variables ***
${OUTER_VM_IMAGE}    ubuntu:26.04

*** Keywords ***
Snapd Stable Compatibility Suite Setup
    Launch Outer Test VM    vm_name=microceph-snapd-stable-vm
    Verify Resolute Outer VM
    Copy Snap To VM

*** Test Cases ***
Install Local Snap On Stable Snapd
    [Documentation]    Passes once stable snapd supports the snapd2.78 assumption.
    Prepare Snapd In VM
    Run In VM And Check    sudo snap install core26 || true    120
    Run In VM And Check    sudo snap install --dangerous ~/microceph_*.snap    600
