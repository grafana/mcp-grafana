from mcp_grafana.settings import GrafanaSettings


def test_settings():
    # Test we can instantiate settings with defaults.
    settings = GrafanaSettings(url="http://localhost:3000")
    assert settings.api_key is None
    assert settings.tools.search.enabled
    assert settings.tools.search.limit == 1000
