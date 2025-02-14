import pytest


def pytest_addoption(parser):
    """
    Add command line options for integration and cloud tests.

    This is used as an alternative to relying on custom markers,
    which do not stop the tests running by default.

    To run integration tests, use:

        pytest --integration

    To run cloud tests, use:

        pytest --integration --cloud

    These two can be combined.
    """
    parser.addoption(
        "--integration",
        action="store_true",
        dest="integration",
        default=False,
        help="enable integration tests",
    )
    parser.addoption(
        "--cloud",
        action="store_true",
        dest="cloud",
        default=False,
        help="enable cloud integration tests",
    )


@pytest.fixture
def anyio_backend():
    return "asyncio"
