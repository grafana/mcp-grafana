from pydantic_settings import BaseSettings, SettingsConfigDict


class GrafanaSettings(BaseSettings):
    model_config: SettingsConfigDict = SettingsConfigDict(
        env_prefix="GRAFANA_", env_file=".env"
    )

    url: str = "http://localhost:3000"
    api_key: str | None = None


grafana_settings = GrafanaSettings()
