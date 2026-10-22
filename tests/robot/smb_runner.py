#!/usr/bin/env python3
"""One owned lab, independent ordered SMB scenarios, honest combined verdict."""
import argparse
import itertools
import json
import os
from pathlib import Path
import subprocess
import sys
import xml.etree.ElementTree as ET

HERE = Path(__file__).resolve().parent
SUITES = {'singleton': ('smb-singleton-tests', 3), 'clustered': ('smb-clustered-tests', 6), 'ad': ('smb-ad-tests', 9)}


def suite_plan(order, repeat=1, permutations=False):
    """Validate the requested suites before any lab allocation."""
    if not order or len(order) != len(set(order)) or any(name not in SUITES for name in order) or repeat < 1:
        raise ValueError('Select distinct SMB suites: singleton,clustered,ad; repeat must be positive')
    if permutations:
        if set(order) != set(SUITES):
            raise ValueError('Permutation validation requires all three SMB suites')
        return [name for permutation in itertools.permutations(order) for name in permutation] * repeat
    return list(order) * repeat


def run_sequence(plan, execute, cleanup):
    """Keep failures; release each scenario once; block on an uncertain boundary."""
    rows = []
    blocked = False
    for ordinal, name in enumerate(plan, 1):
        row = {'suite': name, 'ordinal': ordinal}
        if blocked:
            row['status'] = 'BLOCKED'
            rows.append(row)
            continue
        try:
            row.update(execute(name, ordinal))
            row['status'] = 'PASS' if row.get('rc', 1) == 0 else 'FAIL'
        except Exception as error:
            row.update(status='FAIL', rc=1, error=str(error))
        finally:
            try:
                cleanup(name, ordinal)
            except Exception as error:
                row['cleanup_error'] = str(error)
                row['status'] = 'FAIL'
                blocked = True
        rows.append(row)
    return {'rc': int(any(row['status'] != 'PASS' for row in rows)), 'suites': rows}


def _robot(target, directory, variables, options, dryrun=False):
    directory.mkdir(parents=True, exist_ok=True)
    command = [sys.executable, '-u', '-m', 'robot', *options, '--outputdir', str(directory), '--exitonfailure']
    if dryrun:
        command.append('--dryrun')
    for variable in variables:
        command += ['--variable', variable]
    command.append(str(target))
    result = subprocess.run(command, cwd=HERE.parents[1],
                            env=dict(os.environ, PYTHONDONTWRITEBYTECODE='1', PYTHONUNBUFFERED='1'))
    return result.returncode


def _result(directory, expected):
    output = directory / 'output.xml'
    tests = ET.parse(output).findall('.//test')
    statuses = [test.find('status').attrib['status'] for test in tests]
    return {'rc': int(len(tests) != expected or any(s != 'PASS' for s in statuses)),
            'passed': statuses.count('PASS'), 'failed': statuses.count('FAIL'), 'skipped': statuses.count('SKIP'),
            'output': str(output)}


def _fixture_error(root, messages):
    # A failing infrastructure record must also appear in the combined Robot
    # report, rather than a green report beside a nonzero process exit.
    from robot.api import TestSuite
    suite = TestSuite('SMB shared-lab infrastructure')
    suite.tests.create('Shared lab lifecycle failed').body.create_keyword('Fail', args=['; '.join(messages)])
    result = suite.run(outputdir=str(root / 'infrastructure-error'), log='NONE', report='NONE', console='quiet')
    return str(root / 'infrastructure-error/output.xml')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, allow_abbrev=False)
    parser.add_argument('--snap-path', default='')
    parser.add_argument('--suites', default=','.join(SUITES))
    parser.add_argument('--repeat', type=int, default=1)
    parser.add_argument('--permutations', action='store_true')
    parser.add_argument('--dryrun', action='store_true')
    parser.add_argument('--outputdir', default='.')
    args, options = parser.parse_known_args(argv)
    try:
        plan = suite_plan(args.suites.split(','), args.repeat, args.permutations)
    except ValueError as error:
        parser.error(str(error))
    if any(option in options for option in ('--test', '--suite')):
        parser.error('Cases within each scenario are ordered; select complete scenarios with --suites')
    os.umask(0o077)
    root = Path(args.outputdir).resolve()
    root.mkdir(parents=True, exist_ok=True)
    results = root / 'smb-results'
    results.mkdir(exist_ok=False)
    private = results / 'private'
    private.mkdir(mode=0o700)
    manifest = private / 'lab.json'
    variables = ['SNAP_PATH:' + args.snap_path, 'SMB_LAB_MANIFEST:' + str(manifest)]
    outputs = []
    errors = []
    setup_executions = 0
    summary = {'rc': 1, 'suites': [{'suite': name, 'status': 'BLOCKED'} for name in plan], 'requested_plan': plan}
    # This directory also contains the CLI wrapper robot.py. Do not let it
    # shadow the installed Robot Framework package in the coordinator process.
    sys.path[:] = [p for p in sys.path if Path(p or os.getcwd()).resolve() != HERE]
    sys.path.insert(0, str(HERE / 'resources'))
    from smb_ops import smb_ops

    def execute(name, ordinal):
        directory = results / f'{ordinal:02d}-{name}'
        rc = _robot(HERE / SUITES[name][0], directory, variables,
                    options + ['--name', f'{ordinal:02d}-{name}'], args.dryrun)
        record = _result(directory, SUITES[name][1])
        record['rc'] = int(rc != 0 or record['rc'] != 0)
        if record['rc'] and not record['failed']:
            errors.append(f'{ordinal:02d}-{name}: incomplete, skipped or unsuccessful Robot execution')
        outputs.append(record['output'])
        return record

    def cleanup(name, ordinal):
        if args.dryrun:
            return
        # Suite teardown owns its one retirement attempt. Never retry a failed
        # stop here: uncertain cleanup blocks subsequent scenarios.
        observer = smb_ops()
        observer.use_smb_lab(str(manifest))
        observer.verify_smb_lab()
        observer.assert_smb_scenario_idle()
        if observer.ad_dc.command('systemctl', 'is-active', 'robot-ad-dc', quiet=True).stdout.strip() != 'active':
            raise AssertionError('Fixture directory was not restored; later suites are blocked')
        observer.wait_for_smb_ceph_health()

    try:
        if not args.dryrun:
            fixture = private / 'fixture.robot'
            fixture.write_text('*** Settings ***\nResource    ' + str(HERE / 'resources/smb_suite.resource') +
                               '\n*** Test Cases ***\nProvision Shared SMB Lab\n    SMB Shared Lab Setup\n')
            setup_executions += 1
            rc = _robot(fixture, results / 'setup', variables, options)
            setup = _result(results / 'setup', 1)
            if rc != 0 or setup['rc'] != 0:
                errors.append('Shared SMB lab setup failed; scenarios were not executed')
                summary['suites'] = [{'suite': name, 'status': 'BLOCKED'} for name in plan]
            else:
                summary = run_sequence(plan, execute, cleanup)
        else:
            summary = run_sequence(plan, execute, cleanup)
    except Exception as error:
        errors.append(str(error))
    finally:
        if not args.dryrun and manifest.exists():
            try:
                owner = smb_ops()
                owner.use_smb_lab(str(manifest), partial=True)
                # The directory is disposable and belongs to this job. Restore
                # a possibly interrupted outage before its final destruction.
                if owner.ad_dc:
                    present = json.loads(owner._host('lxc', 'query', 'local:/1.0/instances?recursion=1&project=default'))
                    matches = [r for r in present if r['name'] == owner.ad_dc.name]
                    if matches and matches[0]['config'].get('user.microceph-test-owner') == owner.owner:
                        unit = owner.ad_dc.command('test', '-f', '/etc/systemd/system/robot-ad-dc.service', quiet=True)
                        if unit.rc == 0:
                            restored = owner.ad_dc.command('systemctl', 'start', 'robot-ad-dc', quiet=True, timeout=90)
                            if restored.rc != 0:
                                errors.append('Fixture DC restoration failed')
                owner.destroy_smb_lab()
            except Exception as error:
                errors.append('Owned shared-lab teardown failed: ' + str(error))
        guards = [row['cleanup_error'] for row in summary['suites'] if row.get('cleanup_error')]
        execution_errors = [row['error'] for row in summary['suites'] if row.get('error')]
        if errors or guards or execution_errors:
            outputs.append(_fixture_error(results, errors + guards + execution_errors))
        summary.update(rc=int(summary.get('rc', 1) != 0 or bool(errors or guards or execution_errors)),
                       requested_plan=plan, errors=errors, dryrun=args.dryrun,
                       shared_lab_setup_executions=setup_executions)
        (root / 'smb-summary.json').write_text(json.dumps(summary, indent=2) + '\n')
        if outputs:
            from robot import rebot
            rc = rebot(*outputs, name='SMB Integration', output=str(root / 'output.xml'),
                       log=str(root / 'log.html'), report=str(root / 'report.html'))
            if rc:
                summary['rc'] = 1
                (root / 'smb-summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    return summary['rc']


if __name__ == '__main__':
    raise SystemExit(main())
