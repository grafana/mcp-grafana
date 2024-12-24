from pydantic_settings import BaseSettings, SettingsConfigDict


class GrafanaSettings(BaseSettings):
    model_config: SettingsConfigDict = SettingsConfigDict(
        env_prefix="GRAFANA_", env_file=".env"
    )

    url: str
    api_key: str | None = None


grafana_settings = GrafanaSettings()
