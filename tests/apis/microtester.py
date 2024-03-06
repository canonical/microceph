import os
import sys
import logging
import json
import yaml
import argparse
from abc import ABC, abstractmethod
from enum import Enum
from typing import List
from functools import partialmethod
from requests import sessions, exceptions
from requests_unixsocket import DEFAULT_SCHEME, UnixAdapter
from urllib.parse import quote

logger = logging.getLogger(__name__)


class InvalidAPIPathError(Exception):
    """Requested API path is not Invalid."""


class APIResponseParseError(Exception):
    """API response is non-standard."""


class InvalidExpectationType(Exception):
    """Unsupported Expectation type."""


class ExpectationTypeMismatchError(Exception):
    """The type of the expectation value does not match the expected value."""


class ExpectationFailed(Exception):
    """Expected response value was not received."""


class PrerequisiteCheckFailed(Exception):
    """The pre-requisites for the expectation did not match."""


class InvalidTestData(Exception):
    """Provided Test data does not meet expectations."""


class InvalidTestSuite(Exception):
    """Provided Testsuite YAML file is invalid."""


class Logger:
    def __init__(self, caller: str, print_debug: bool = False):
        self.caller = caller.upper()
        self.is_debug = print_debug

    def log_dbg(self, *args, **kwargs):
        """Print Debug level execution info"""
        if self.is_debug:
            print(self.caller, "DEBUG", *args, file=sys.stdout, **kwargs)

    def log_err(self, *args, **kwargs):
        """Print Error level execution info"""
        print(self.caller, "ERROR", *args, file=sys.stderr, **kwargs)

    def print(self, *args, **kwargs):
        print(*args, file=sys.stdout, **kwargs)


class Client:
    """The client object used to perform the API requests."""

    def __init__(self, sock_path: str, print_debug: bool):
        """Initialise the client object with socket file for endpoint."""
        self.is_debug = print_debug
        self.logger = Logger(self.__class__.__name__, print_debug=print_debug)
        self._session = sessions.session()
        self._session.mount(DEFAULT_SCHEME, UnixAdapter())
        # encode '/' as %2
        self.server = f"{DEFAULT_SCHEME}{quote(sock_path, safe='')}"
        # Test the connection, exception is raised if failed.
        self.request(method="GET", path="")
        self.logger.log_dbg(f"Attached Client to {sock_path} successfully.")

    def request(self, method: str, path: str, **kwargs):
        """Iniatiate a METHOD request to provided server API path."""
        if len(path) != 0:
            # Dont process API requests to paths that dont start with '/'
            if path[0] != "/":
                raise InvalidAPIPathError("API paths should always start with a '/'")

        request_url = f"{self.server}{path}"
        self.logger.log_dbg(f"Request: {method.upper()} {path}: Payload({kwargs})")
        try:
            resp = self._session.request(method=method, url=request_url, **kwargs)
            self.logger.log_dbg(f"Response: {resp.text}")
            return resp.json()
        except exceptions.ConnectionError as e:
            self.logger.log_err(f"Failed to connect to {request_url}: {e}")


class ExpectationType(str, Enum):
    """Enumeration for Expectation type."""

    CODE = "response_code"
    RESPONSE = "response_value"
    CONTAINS = "response_dict_contains"
    COUNT = "response_list_count"


class Expectation:
    """Implements API response expectation"""

    def __init__(self, type: str, expectation, print_debug: bool = False):
        self.type = type
        self.expectation = expectation
        self.logger = Logger(self.__class__.__name__, print_debug=print_debug)

    @abstractmethod
    def validate(self, response: dict):
        """Validates the API response"""
        self.logger.log_dbg(f"Response: {response}")

        response_keys = response.keys()
        key_check_list = [
            "type",
            "status",
            "status_code",
            "operation",
            "error_code",
            "error",
            "metadata",
        ]

        # Generic checks to ensure all the expected fields are present in the API response.
        for key in key_check_list:
            self.assert_prerequisites(
                id=f"Check for {key} in API response:", condition=(key in response_keys)
            )

    def assert_prerequisites(self, condition: bool, id: str = ""):
        if not condition:
            raise PrerequisiteCheckFailed(f"FAILED {id}: {condition}.")

    def assert_expectation(self, expected, actual, id: str = ""):
        if type(expected) != type(actual):
            raise ExpectationTypeMismatchError(
                f"FAILED {id}: Expected(Type: {type(expected)}), Actual(Type: {type(actual)})"
            )
        if expected != actual:
            raise ExpectationFailed(
                f"FAILED {id}: Expected({expected}), Actual({actual})"
            )

        self.logger.log_dbg(f"SUCCESS {id}: Expected({expected}), Actual({actual})")


class CodeExpectation(Expectation):
    """Implements API response check that matches HTTP code expectation"""

    def __init__(self, expectation: int, print_debug: bool = False):
        super().__init__(
            type=ExpectationType.CODE, expectation=expectation, print_debug=print_debug
        )

    def validate(self, response: dict):
        """Validates the HTTP response code against expected value"""
        # Run generic checks.
        super().validate(response)

        # Assert Pre-Requisites
        self.assert_prerequisites(
            id="Check for integer expectation value",
            condition=isinstance(self.expectation, int),
        )

        # Assert Expectation
        if self.expectation == 200:
            self.assert_expectation(
                id="Check for Error Code",
                expected=0,
                actual=response.get("error_code", None),
            )
            self.assert_expectation(
                id="Check for Response Code",
                expected=self.expectation,
                actual=response.get("status_code", None),
            )
        else:
            self.assert_expectation(
                id="Check for Response Code",
                expected=0,
                actual=response.get("status_code", None),
            )
            self.assert_expectation(
                id="Check for Error Code",
                expected=self.expectation,
                actual=response.get("error_code", None),
            )


class ResponseExpectation(Expectation):
    """Implements API response check that matches response payload expectation"""

    def __init__(self, expectation, print_debug: bool = False):
        super().__init__(
            type=ExpectationType.RESPONSE,
            expectation=expectation,
            print_debug=print_debug,
        )

    def validate(self, response: dict):
        """Validates the API response against expected value."""
        # Run generic checks.
        super().validate(response)

        # Assert Expectation
        self.assert_expectation(
            id="Check for API response",
            expected=self.expectation,
            actual=response.get("metadata", None),
        )


class ResponseListCountExpectation(Expectation):
    """Implements API response check for list response type that matches element count expectation"""

    def __init__(self, expectation: int, print_debug: bool = False):
        super().__init__(
            type=ExpectationType.COUNT, expectation=expectation, print_debug=print_debug
        )

    def validate(self, response: list):
        """Validates the API response to be of list type and contains expected number of elements."""
        # Run generic checks.
        super().validate(response)

        # Assert Pre-Requisite
        self.assert_prerequisites(
            id="Check for list API response type",
            condition=(type(response.get("metadata")) == type([]))
        )

        # Assert Expectation
        self.assert_expectation(
            id="Check for list API response elem count",
            expected=self.expectation,
            actual=len(response.get("metadata")),
        )


class ResponseDictContainsExpectation(Expectation):
    """Implements API response check for dict response type that matches kv pair expectation"""

    def __init__(self, expectation: dict, print_debug: bool = False):
        super().__init__(
            type=ExpectationType.CONTAINS,
            expectation=expectation,
            print_debug=print_debug,
        )

    def validate(self, response: dict):
        """Validates the API response to be of dict type and contains expected kv pairs."""
        # Run generic checks.
        super().validate(response)

        # Assert Pre-Requisite
        self.assert_prerequisites(
            id="Check for dict API response type",
            condition=(type(response.get("metadata")) == type({}))
        )

        # Assert Expectation
        metadata = response.get("metadata")
        response_keys = metadata.keys()
        for key in self.expectation.keys():
            self.assert_expectation(
                id="Check for key {key} present in response",
                expected=True,
                actual=(key in response_keys),
            )
            self.assert_expectation(
                id="Check for key {key} value in response",
                expected=self.expectation[key],
                actual=metadata[key],
            )


class TestResult:
    def __init__(self, name: str, total: int, passes: int):
        self.logger = Logger(self.__class__.__name__)
        self.name = name
        self.total = total
        self.passes = passes
        self.failures = total - passes

        self.logger.print(
            f"Validation Result: Total: {self.total}, Pass: {self.passes}, Fail: {self.failures}.",
            "\n",
        )


class ExpectationValidator:
    """Implements API response expectation validator."""

    def __init__(
        self, test_name: str, expectations: List[Expectation], print_debug: bool = False
    ):
        self.test_name = test_name
        self.expectations = expectations
        self.logger = Logger(self.__class__.__name__, print_debug=print_debug)

    def need_validation(self) -> bool:
        return len(self.expectations) > 0

    def validate(self, response: dict):
        """Runs Validation for all the expectations of a test."""
        success_count = 0
        for expectation in self.expectations:
            try:
                expectation.validate(response)
                success_count += 1
            except (
                ExpectationTypeMismatchError,
                ExpectationFailed,
                PrerequisiteCheckFailed,
            ) as e:
                self.logger.print(e)
                continue
        return TestResult(
            name=self.test_name, total=len(self.expectations), passes=success_count
        )


class TestValidator:
    """Runs validations for an API"""

    def __init__(self, sock_path: str, test_data: dict, print_debug: bool = False):
        test_data_keys = test_data.keys()
        key_check_list = ["name", "path", "method"]
        for key in key_check_list:
            if key not in test_data_keys:
                raise InvalidTestData(
                    f"Test {test_data.get('name', None)} does not have {key} field defined."
                )

        self.test_data = test_data

        expectation_translation_table = {
            ExpectationType.CODE: CodeExpectation,
            ExpectationType.RESPONSE: ResponseExpectation,
            ExpectationType.COUNT: ResponseListCountExpectation,
            ExpectationType.CONTAINS: ResponseDictContainsExpectation,
        }

        expectations = []
        exp_dict = self.test_data.get("expectations")
        supported_expectations = expectation_translation_table.keys()
        for key in exp_dict.keys():
            if key not in supported_expectations:
                raise InvalidTestData(
                    f"Expectation {key} is not supported use one of {supported_expectations}."
                )
            expectations.append(
                # Instantiate required Expectation object.
                expectation_translation_table[key](
                    expectation=exp_dict[key], print_debug=print_debug
                )
            )

        self.validator = ExpectationValidator(
            test_name=test_data.get("name", None),
            expectations=expectations,
            print_debug=print_debug,
        )
        self.client = Client(sock_path=sock_path, print_debug=print_debug)
        self.logger = Logger(self.__class__.__name__, print_debug=print_debug)

    def test(self) -> bool:
        # Perform API request
        self.logger.print(f"Executing Test {self.test_data['name']}")
        data = self.test_data.get("input", None)
        try:
            response = self.client.request(
                method=self.test_data["method"],
                path=self.test_data["path"],
                data=json.dumps(data),
            )
        except InvalidAPIPathError as e:
            self.logger.print(f"Skipping Test {self.test_data['name']}: {e}")
            return

        if self.validator.need_validation():
            result = self.validator.validate(response)
            return result.total == result.passes
        else:
            return True


class TestsuiteValidator:
    """Parses a single Test suite file and validates all requests tests."""

    def __init__(self, testsuite_file: str, sock_path: str, print_debug: bool = False):
        self.file = testsuite_file
        self.sock_path = sock_path
        self.print_debug = print_debug
        self.logger = Logger(str(self.__class__.__name__), print_debug=print_debug)
        # check if testsuite_file is a valid yaml file.
        if not os.path.isfile(testsuite_file):
            raise InvalidTestSuite(f"{testsuite_file} is not a file.")

        with open(testsuite_file) as stream:
            try:
                self.testsuite = yaml.safe_load(stream)
            except yaml.YAMLError as e:
                raise InvalidTestSuite(f"{testsuite_file} is not a valid YAML file.")

    def validate(self) -> bool:
        self.logger.print(f"Validating Testsuite: {os.path.basename(self.file)}")
        all_pass = True
        for test in self.testsuite.get("tests", None):
            test_passed = TestValidator(
                sock_path=self.sock_path, test_data=test, print_debug=self.print_debug
            ).test()
            if not test_passed:
                all_pass = False
        return all_pass


def execute_test(args):
    """Execute requested tests."""
    if os.path.isfile(args.path):
        result = TestsuiteValidator(args.path, args.sock, args.debug).validate()
        # True corresponds to non-zero(1) exit code.
        sys.exit(not result)
    elif os.path.isdir(args.path):
        testsuites = []
        for file in os.listdir(args.path):
            if file.endswith(".yaml"):
                testsuites.append(f"{args.path}/{file}")

        testsuites = sorted(testsuites)
        if args.debug:
            print("Running tests for:")
            print(*testsuites, sep="\n")

        all_pass = True
        for testsuite in testsuites:
            result = TestsuiteValidator(testsuite, args.sock, args.debug).validate()
            if not result:
                all_pass = False
        # True corresponds to non-zero(1) exit code.
        sys.exit(not all_pass)
    else:
        sys.exit(f"{args.path} is not a valid file or directory path.")


if __name__ == "__main__":

    argparse = argparse.ArgumentParser(
        description="MicroCluster API Tester",
        epilog="Ex: python3 microtester.py --sock=<socket_path> --test-suite=<testsuite_yaml_path>",
    )

    argparse.add_argument(
        "path",
        type=str,
        help="Path to the testsuite file or a directory containing testsuite files.",
    )

    argparse.add_argument(
        "--sock",
        type=str,
        required=True,
        help="Control socket file path",
    )

    argparse.add_argument(
        "--debug",
        default=False,
        action="store_true",
        help="Print debug information.",
    )

    # Parse the args.
    args = argparse.parse_args()

    execute_test(args=args)
