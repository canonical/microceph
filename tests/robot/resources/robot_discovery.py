"""Canonical Robot discovery shared by full-tree and owned SMB preflight."""
import sys

from robot.api import TestSuiteBuilder
from robot.conf import RobotSettings
from robot.model.modifier import ModelModifier
from robot.output import LOGGER


def build_suite(options, sources):
    """Build without running suite setup, using Robot's parsed settings."""
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
    return settings, suite


def case_names(suite):
    """Collect the discovered/selected case identities, without execution."""
    if suite is None:
        return set()
    names = {test.name for test in suite.tests}
    for child in suite.suites:
        names.update(case_names(child))
    return names


def arguments(options):
    """Serialize canonical parsed options, never reparse aliases ourselves."""
    result = []
    for key, value in options.items():
        if isinstance(value, bool):
            result.append("--" + ("" if value else "no") + key)
        else:
            for item in value if isinstance(value, list) else [value]:
                result.extend(["--" + key, str(item)])
    return result
