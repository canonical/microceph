#!/usr/bin/env python3
"""Keep full-tree selection/output while giving singleton SMB to its owner."""
import argparse
import copy
import os
from pathlib import Path
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
# The adjacent wrapper must not shadow the installed Robot Framework package.
sys.path[:] = [path for path in sys.path if Path(path or os.getcwd()).resolve() != HERE]

from robot.api import ExecutionResult, ResultWriter, TestSuiteBuilder
from robot.conf import RobotSettings
from robot.errors import DataError, Information
from robot.model import SuiteVisitor
from robot.model.modifier import ModelModifier
from robot.output import LOGGER
from robot.result import TestSuite
from robot.run import RobotFramework


class _SingletonSelection(SuiteVisitor):
    def __init__(self, source):
        self.source = source
        self.suite = None

    def start_suite(self, suite):
        if suite.source == self.source:
            self.suite = suite
            return False


def _singleton(suite):
    visitor = _SingletonSelection(HERE / "smb-singleton-tests")
    suite.visit(visitor)
    return visitor.suite


def _case_names(suite):
    if suite is None:
        return set()
    names = {test.name for test in suite.tests}
    for child in suite.suites:
        names.update(_case_names(child))
    return names


def _arguments(options):
    """Serialize Robot's canonical parsed options, never parse aliases ourselves."""
    arguments = []
    for key, value in options.items():
        if isinstance(value, bool):
            arguments.append("--" + ("" if value else "no") + key)
        else:
            for item in value if isinstance(value, list) else [value]:
                arguments.extend(["--" + key, str(item)])
    return arguments


def _run(command):
    # ROBOT_OPTIONS was already expanded by the owning parser, including aliases
    # and argument files. Reapplying it could change qualified selection/output.
    env = dict(os.environ, PYTHONUNBUFFERED="1", PYTHONDONTWRITEBYTECODE="1")
    env.pop("ROBOT_OPTIONS", None)
    return subprocess.run(command, env=env).returncode


def _full(arguments, snap_path):
    options, sources = RobotFramework().parse_arguments([*arguments, str(HERE)])
    settings = RobotSettings(options)
    if settings.pythonpath:
        sys.path[:0] = settings.pythonpath
    suite = TestSuiteBuilder(included_extensions=settings.extension,
                             included_files=settings.parse_include,
                             custom_parsers=settings.parsers, rpa=settings.rpa,
                             lang=settings.languages,
                             allow_empty_suite=settings.run_empty_suite).build(*sources)
    if settings.pre_run_modifiers:
        suite.visit(ModelModifier(settings.pre_run_modifiers, settings.run_empty_suite, LOGGER))
    complete = _singleton(suite)
    expected = _case_names(complete)
    # Parse-time filtering may intentionally omit SMB altogether. A discovered
    # singleton must still be complete, even when parse-time filters are used.
    if len(expected) != 3 and (complete is not None or not settings.parse_include):
        raise DataError("Full discovery must contain the complete three-case SMB singleton scenario")
    suite.configure(**settings.suite_config)
    selected = _singleton(suite)
    selected_names = _case_names(selected)
    if selected_names and selected_names != expected:
        raise DataError("Select all three SMB singleton cases or none; partial scenarios cannot allocate a lab")
    root_name, root_source = suite.name, suite.source
    before = copy.deepcopy(suite)
    after = copy.deepcopy(suite)
    if selected:
        index = list(suite.suites).index(selected)
        before.suites = list(before.suites)[:index]
        after.suites = list(after.suites)[index + 1:]
    else:
        after.suites = []
    # Configuration/selection was applied to the full tree. Reapplying qualified
    # selectors to a separately rooted coordinator would select different cases.
    forwarded = {key: value for key, value in options.items() if key not in {
        "suite", "test", "include", "exclude", "rerunfailed", "rerunfailedsuites",
        "prerunmodifier", "randomize", "name"}}
    outputdir = settings.output_directory
    outputdir.mkdir(parents=True, exist_ok=True)
    rc = 0
    outputs = []
    with tempfile.TemporaryDirectory(prefix=".robot-full-", dir=outputdir) as directory:
        private = Path(directory)
        # Preserve full-tree order and --exitonfailure without a future suite
        # table: only the singleton's position splits the ordinary population.
        for label, part in (("before", before), ("singleton", None), ("after", after)):
            if rc and settings.exit_on_failure:
                break
            if part is not None:
                if not part.test_count and (selected_names or label == "after"):
                    continue
                source = private / (label + ".rbt")
                part.to_json(source)
                non_smb = private / label
                command = [sys.executable, "-u", "-m", "robot", *_arguments(forwarded),
                           "--outputdir", str(non_smb), "--output", "output.xml",
                           "--log", "NONE", "--report", "NONE", "--xunit", "NONE", "--notimestampoutputs", str(source)]
                part_rc = _run(command)
                outputs.append(non_smb / "output.xml")
            elif selected_names:
                command = [sys.executable, "-u", str(HERE / "smb_runner.py"),
                           *_arguments(forwarded), "--snap-path", snap_path,
                           "--outputdir", str(outputdir), "--output", "output.xml",
                           "--log", "NONE", "--report", "NONE", "--xunit", "NONE", "--notimestampoutputs"]
                part_rc = _run(command)
                smb_xml = outputdir / "smb-results/singleton/output.xml"
                if smb_xml.is_file():
                    result = ExecutionResult(smb_xml)
                    smb_root = result.suite
                    result.suite = TestSuite(name=root_name, source=root_source)
                    result.suite.suites.append(smb_root)
                    wrapped = private / "singleton.xml"
                    result.save(wrapped)
                    outputs.append(wrapped)
                else:
                    return rc or part_rc or 252
            else:
                continue
            rc = rc or part_rc
        if not outputs or any(not path.is_file() for path in outputs):
            return rc or 252
        combined = private / "combined.xml"
        aggregate = ExecutionResult(*outputs, merge=True)
        aggregate.save(combined)
        if not any((settings.output, settings.log, settings.report, settings.xunit)):
            return max(rc, aggregate.return_code if settings.status_rc else 0)
        rebot = settings.get_rebot_settings()
        rebot["Output"] = settings.output
        rebot_rc = ResultWriter(combined).write_results(rebot)
        return max(rc, rebot_rc if rebot_rc >= 0 else 252)


def main(argv=None):
    parser = argparse.ArgumentParser(add_help=False, allow_abbrev=False)
    parser.add_argument("--snap-path", default="")
    args, arguments = parser.parse_known_args(argv)
    try:
        return _full(arguments, args.snap_path)
    except Information as error:
        print(error)
        return 251
    except (DataError, OSError, ValueError) as error:
        print("Full Robot execution failed: " + str(error), file=sys.stderr)
        return 252


if __name__ == "__main__":
    raise SystemExit(main())
