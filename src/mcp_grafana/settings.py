from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class SearchSettings(BaseSettings):
    enabled: bool = True
    limit: int = 1000


class ToolSettings(BaseSettings):
    search: SearchSettings = Field(default_factory=SearchSettings)


class GrafanaSettings(BaseSettings):
    model_config: SettingsConfigDict = SettingsConfigDict(
        env_prefix="GRAFANA_", env_file=".env", env_nested_delimiter="__"
    )

    url: str = "http://localhost:3000"
    api_key: str | None = None

    tools: ToolSettings = Field(default_factory=ToolSettings)


grafana_settings = GrafanaSettings()
