import os
from unittest.mock import patch

from mcp_grafana.settings import GrafanaSettings


@patch.dict(os.environ, clear=True)
def test_settings():
    # Test we can instantiate settings with defaults.
    settings = GrafanaSettings(url="http://localhost:3000")
    assert settings.api_key is None
    assert settings.tools.search.enabled
    assert settings.tools.search.limit == 1000


@patch.dict(
    os.environ,
    {
        "GRAFANA_URL": "http://localhost:3001",
        "GRAFANA_API_KEY": "my-api-key",
        "GRAFANA_TOOLS__SEARCH__ENABLED": "false",
        "GRAFANA_TOOLS__SEARCH__LIMIT": "100",
    },
)
def test_settings_from_env():
    # Test we can instantiate settings from environment variables with delimiters.
    settings = GrafanaSettings()
    assert settings.url == "http://localhost:3001"
    assert settings.api_key == "my-api-key"
    assert not settings.tools.search.enabled
    assert settings.tools.search.limit == 100
