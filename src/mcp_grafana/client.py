"""
A client for the Grafana API.

In future we may generate this from the OpenAPI spec but
for now we just use a custom client.

It's a bit messy for now because it came out of a Hackathon.
We should separate HTTP types from tool types.
"""

import contextvars
import math
from datetime import datetime
from typing import Any

import httpx
from pydantic import UUID4

from .settings import grafana_settings, GrafanaSettings
from .grafana_types import (
    AddActivityToIncidentArguments,
    CreateIncidentArguments,
    CreateSiftInvestigationArguments,
    QueryIncidentPreviewsRequest,
    Query,
    SearchDashboardsArguments,
    Selector,
    query_list,
)


class GrafanaError(Exception):
    """
    An error returned by the Grafana API.
    """

    pass


class BearerAuth(httpx.Auth):
    def __init__(self, api_key: str):
        self.api_key = api_key

    def auth_flow(self, request):
        request.headers["Authorization"] = f"Bearer {self.api_key}"
        yield request


class GrafanaClient:
    def __init__(self, url: str, api_key: str | None = None) -> None:
        auth = BearerAuth(api_key) if api_key is not None else None
        self.c = httpx.AsyncClient(
            base_url=url, auth=auth, timeout=httpx.Timeout(timeout=30.0)
        )

    @classmethod
    def from_settings(cls, settings: GrafanaSettings) -> "GrafanaClient":
        """
        Create a Grafana client from the given settings.
        """
        return cls(settings.url, settings.api_key)

    @classmethod
    def for_current_request(cls) -> "GrafanaClient":
        """
        Create a Grafana client for the current request.

        This will use the Grafana settings from the current contextvar.
        If running with the stdio transport then these settings will be
        the ones in the MCP server's settings. If running with the SSE
        transport then the settings will be extracted from the request
        headers if possible, falling back to the defaults in the MCP
        server's settings.
        """
        return cls.from_settings(grafana_settings.get())

    async def get(self, path: str, params: dict[str, str] | None = None) -> bytes:
        r = await self.c.get(path, params=params)
        if not r.is_success:
            raise GrafanaError(r.read().decode())
        return r.read()

    async def post(self, path: str, json: dict[str, Any]) -> bytes:
        r = await self.c.post(path, json=json)
        if not r.is_success:
            raise GrafanaError(r.read().decode())
        return r.read()

    async def list_datasources(self) -> bytes:
        return await self.get("/api/datasources")

    async def get_datasource(
        self, uid: str | None = None, name: str | None = None
    ) -> bytes:
        if uid is not None:
            return await self.get(f"/api/datasources/uid/{uid}")
        elif name is not None:
            return await self.get(f"/api/datasources/name/{name}")
        else:
            raise ValueError("uid or name must be provided")

    async def search_dashboards(self, arguments: SearchDashboardsArguments) -> bytes:
        params = arguments.model_dump(exclude_defaults=True)
        if params["query"] is None:
            del params["query"]
        return await self.get(
            "/api/search",
            params=params,
        )

    async def get_dashboard(self, dashboard_uid: str) -> bytes:
        return await self.get(f"/api/dashboards/uid/{dashboard_uid}")

    # TODO: split incident stuff into a separate client.
    async def list_incidents(self, body: QueryIncidentPreviewsRequest) -> bytes:
        return await self.post(
            "/api/plugins/grafana-incident-app/resources/api/IncidentsService.QueryIncidentPreviews",
            json=body.model_dump(),
        )

    async def create_incident(self, arguments: CreateIncidentArguments) -> bytes:
        return await self.post(
            "/api/plugins/grafana-incident-app/resources/api/IncidentsService.CreateIncident",
            json=arguments.model_dump(),
        )

    async def add_activity_to_incident(
        self, arguments: AddActivityToIncidentArguments
    ) -> bytes:
        return await self.post(
            "/api/plugins/grafana-incident-app/resources/api/ActivityService.AddActivity",
            json=arguments.model_dump(),
        )

    async def close_incident(self, incident_id: str, summary: str) -> bytes:
        return await self.post(
            "/api/plugins/grafana-incident-app/resources/api/IncidentsService.CloseIncident",
            json={
                "incidentID": incident_id,
                "summary": summary,
            },
        )

    async def create_sift_investigation(
        self, arguments: CreateSiftInvestigationArguments
    ) -> bytes:
        return await self.post(
            "/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations",
            json=arguments.model_dump(),
        )

    async def get_sift_investigation(
        self,
        investigation_id: UUID4,
    ) -> bytes:
        return await self.get(
            f"/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/{investigation_id}"
        )

    async def get_sift_analyses(
        self,
        investigation_id: UUID4,
    ) -> bytes:
        return await self.get(
            f"/api/plugins/grafana-ml-app/resources/sift/api/v1/investigations/{investigation_id}/analyses"
        )

    async def query(self, _from: datetime, to: datetime, queries: list[Query]) -> bytes:
        body = {
            "from": str(math.floor(_from.timestamp() * 1000)),
            "to": str(math.floor(to.timestamp() * 1000)),
            "queries": query_list.dump_python(queries, by_alias=True),
        }
        return await self.post("/api/ds/query", json=body)

    async def list_prometheus_metric_metadata(
        self,
        datasource_uid: str,
        limit: int | None = None,
        limit_per_metric: int | None = None,
        metric: str | None = None,
    ) -> bytes:
        params: dict[str, Any] = {}
        if limit is not None:
            params["limit"] = limit
        if limit_per_metric is not None:
            params["limitPerMetric"] = limit_per_metric
        if metric is not None:
            params["metric"] = metric
        return await self.get(
            f"/api/datasources/proxy/uid/{datasource_uid}/api/v1/metadata", params
        )

    async def list_prometheus_label_names(
        self,
        datasource_uid: str,
        matches: list[Selector] | None = None,
        start: datetime | None = None,
        end: datetime | None = None,
        limit: int | None = None,
    ) -> bytes:
        params = {}
        if matches is not None:
            params["match[]"] = [str(m) for m in matches]
        if start is not None:
            params["start"] = start.isoformat()
        if end is not None:
            params["end"] = end.isoformat()
        if limit is not None:
            params["limit"] = limit

        return await self.get(
            f"/api/datasources/proxy/uid/{datasource_uid}/api/v1/labels",
            params,
        )

    async def list_prometheus_label_values(
        self,
        datasource_uid: str,
        label_name: str,
        matches: list[Selector] | None = None,
        start: datetime | None = None,
        end: datetime | None = None,
        limit: int | None = None,
    ) -> bytes:
        params = {}
        if matches is not None:
            params["match[]"] = [str(m) for m in matches]
        if start is not None:
            params["start"] = start.isoformat()
        if end is not None:
            params["end"] = end.isoformat()
        if limit is not None:
            params["limit"] = limit

        return await self.get(
            f"/api/datasources/proxy/uid/{datasource_uid}/api/v1/label/{label_name}/values",
            params,
        )


grafana_client = contextvars.ContextVar("grafana_client")
grafana_client.set(GrafanaClient.for_current_request())
