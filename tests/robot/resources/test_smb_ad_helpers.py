"""External-directory fixture contracts; no VM or credential allocation."""
import importlib.util
from pathlib import Path

import pytest
from robot.api import TestSuiteBuilder as RobotSuiteBuilder

HERE = Path(__file__).resolve().parent


def fixture_module():
    path = HERE / "smb_ad_guest.py"
    assert path.is_file(), "the actual external DC/client fixture is required"
    spec = importlib.util.spec_from_file_location("smb_ad_guest_contract", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_actual_ad_resources_and_forced_protocols():
    module = fixture_module()
    documents = module.resource_documents(["192.0.2.1", "192.0.2.2"], "192.0.2.53", "synthetic-private")
    resources = documents["ad"]["resources"]
    cluster = next(row for row in resources if row["resource_type"] == "ceph.smb.cluster")
    assert cluster["auth_mode"] == "active-directory"
    assert cluster["clustering"] == "always"
    assert cluster["custom_ports"] == {"smb": 1445}
    assert cluster["domain_settings"]["join_sources"] == [{"source_type": "resource", "ref": "robot-join"}]
    assert "user_group_settings" not in cluster
    for protocol, flag in (("kerberos", "required"), ("kerberos-missing", "required"), ("ntlm", "off")):
        argv = module.client_argv("192.0.2.1", "data", "alice", protocol, "ls")
        assert "--use-kerberos=" + flag in argv
        assert "synthetic-private" not in repr(argv)
    with pytest.raises(ValueError):
        module.client_argv("192.0.2.1", "data", "alice", "desired", "ls")


def test_ad_case_count_and_excluded_roles():
    path = HERE.parent / "smb-ad-tests"
    assert path.is_dir(), "the complete installed AD9 scenario is required"
    assert RobotSuiteBuilder().build(path).test_count == 9
    source = (path / "smb_ad_tests.robot").read_text()
    assert "smbmetrics" not in source and "cephfs-proxy" not in source
    assert "kerberos-missing" in source


def test_mappings_and_ticket_evidence_refuse_missing_identity():
    module = fixture_module()
    raw = {"alice": ["S-1-5-21-1-2-3-1000 SID_USER (1)", "ROBOT\\alice:x:2200:2300::/:/bin/false"],
           "bob": ["S-1-5-21-1-2-3-1001 SID_USER (1)", "ROBOT\\bob:x:2201:2300::/:/bin/false"],
           "writers": ["S-1-5-21-1-2-3-2000 SID_DOM_GROUP (2)", "ROBOT\\writers:x:2400:"]}
    assert module.parse_mappings(raw)["alice"]["uid"] == 2200
    raw["alice"][0] = "no SID"
    with pytest.raises(ValueError):
        module.parse_mappings(raw)
    assert module.ticket_evidence("cifs/robot-ad.robot.test@ROBOT.TEST") == "cifs/robot-ad.robot.test@ROBOT.TEST"
    assert module.ticket_evidence("krbtgt/ROBOT.TEST@ROBOT.TEST") == ""
    with pytest.raises(ValueError):
        module.account_values({})
