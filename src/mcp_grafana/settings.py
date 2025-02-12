import contextvars

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class DatasourcesSettings(BaseSettings):
    """
    Settings for the datasources tools.
    """

    enabled: bool = Field(
        default=True, description="Whether to enable the datasources tools."
    )


class SearchSettings(BaseSettings):
    """
    Settings for the search tools.
    """

    enabled: bool = Field(
        default=True, description="Whether to enable the search tools."
    )
    limit: int = Field(
        default=1000,
        description="The default maximum number of results to return, unless overridden by the client.",
    )


class IncidentSettings(BaseSettings):
    """
    Settings for the Incident tools.
    """

    enabled: bool = Field(
        default=True, description="Whether to enable the incident tools."
    )


class PrometheusSettings(BaseSettings):
    """
    Settings for the Prometheus tools.
    """

    enabled: bool = Field(
        default=True, description="Whether to enable the Prometheus tools."
    )


class ToolSettings(BaseSettings):
    search: SearchSettings = Field(default_factory=SearchSettings)
    datasources: DatasourcesSettings = Field(default_factory=DatasourcesSettings)
    incident: IncidentSettings = Field(default_factory=IncidentSettings)
    prometheus: PrometheusSettings = Field(default_factory=PrometheusSettings)


class GrafanaSettings(BaseSettings):
    model_config: SettingsConfigDict = SettingsConfigDict(
        env_prefix="GRAFANA_", env_file=".env", env_nested_delimiter="__"
    )

    url: str = Field(
        default="http://localhost:3000", description="The URL of the Grafana instance."
    )
    api_key: str | None = Field(
        default=None,
        description="A Grafana API key or service account token with the necessary permissions to use the tools.",
    )

    tools: ToolSettings = Field(default_factory=ToolSettings)


# This contextvar can be updated by middleware to reflect the Grafana settings
# for the current request.

# If the middleware is not used, the default settings will be used.
grafana_settings: contextvars.ContextVar[GrafanaSettings] = contextvars.ContextVar(
    "grafana_settings"
)
grafana_settings.set(GrafanaSettings())
