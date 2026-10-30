"""Pure clustered fixture/readiness controls, with no LXD or installed snap."""
import copy

from smb_guest import resource_documents
from smb_ops import observations_stable, role_tokens, snapshot_ready


def snapshot():
    return {'node': 'n0', 'owner': 'robot-smb', 'rank': '0', 'applied_generation': 7,
            'status': {'cluster_id': 'robot-smb', 'ready': True, 'clustered': True, 'ctdb_pnn': 0},
            'context': {'clustered': True, 'ctdb_pnn': 0, 'smb_port': 1445, 'startup': {'smbd': {'prepared': True}}},
            'roles': {role: {'ActiveState': 'active', 'MainPID': str(pid), 'ExecMainStartTimestampMonotonic': '99'}
                      for role, pid in [('smbd', 40), ('ctdbd', 41)]}}


def test_clustered_resources_keep_singleton_and_direct_modes():
    docs = resource_documents(['192.0.2.1'], 'secret', 'refreshed')
    for kind in ('clustered', 'clustered-refresh'):
        cluster = docs[kind]['resources'][1]
        assert cluster['clustering'] == 'always'
        assert cluster['cluster_id'] == 'robot-smb'
        assert docs[kind]['resources'][2]['cephfs']['provider'] == 'samba-vfs/new'
    assert docs['clustered-refresh']['resources'][1]['custom_ports']['smb'] == 2445
    assert docs['nonclustered']['resources'][1]['clustering'] == 'never'
    assert docs['refresh']['resources'][2]['readonly'] is True


def test_clustered_readiness_requires_assigned_rank_and_ctdb():
    state = snapshot()
    assert snapshot_ready(state, 'robot-smb', 1445)
    for mutate in (lambda s: s['roles']['ctdbd'].update(ActiveState='inactive'),
                   lambda s: s.update(rank='1'),
                   lambda s: s['status'].update(ctdb_pnn=1),
                   lambda s: s['context'].update(ctdb_pnn=True)):
        broken = copy.deepcopy(state)
        mutate(broken)
        assert not snapshot_ready(broken, 'robot-smb', 1445)


def test_clustered_stability_covers_both_serving_roles_and_applied_state():
    state = snapshot()
    assert role_tokens([state], 'ctdbd') == {'n0': ('41', '99')}
    assert observations_stable([state], [[copy.deepcopy(state)]])
    for mutate in (lambda s: s['roles']['ctdbd'].update(MainPID='42'),
                   lambda s: s['roles']['smbd'].update(MainPID='43'),
                   lambda s: s.update(applied_generation=8),
                   lambda s: s['status'].update(error='pending')):
        broken = copy.deepcopy(state)
        mutate(broken)
        assert not observations_stable([state], [[broken]])
    assert not observations_stable([state], [])
