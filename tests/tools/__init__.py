import pytest

mark_integration = [
    # Without marking the loop scope as session, the event loop
    # is closed after each test runs, which causes subsequent tests
    # to fail.
    pytest.mark.asyncio(loop_scope="session"),
    pytest.mark.skipif("not config.getoption('integration')"),
]

mark_cloud = [
    # Without marking the loop scope as session, the event loop
    # is closed after each test runs, which causes subsequent tests
    # to fail.
    pytest.mark.asyncio(loop_scope="session"),
    pytest.mark.skipif("not config.getoption('cloud')"),
]
