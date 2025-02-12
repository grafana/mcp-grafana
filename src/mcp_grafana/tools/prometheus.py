import itertools
import re
from datetime import datetime
from typing import Literal

from mcp.server import FastMCP

from ..client import grafana_client
from ..grafana_types import (
    DatasourceRef,
    DSQueryResponse,
    PrometheusMetricMetadata,
    Query,
    ResponseWrapper,
    Selector,
)


PrometheusQueryType = Literal["range", "instant"]


async def query_prometheus(
    datasource_uid: str,
    expr: str,
    start_rfc3339: str,
    end_rfc3339: str | None = None,
    step_seconds: int | None = None,
    query_type: PrometheusQueryType = "range",
) -> DSQueryResponse:
    """
    Query Prometheus using a range request.

    # Parameters.

    datasource_uid: The uid of the datasource to query.
    expr: The PromQL expression to query.
    start_rfc3339: The start time in RFC3339 format.
    end_rfc3339: The end time in RFC3339 format. Ignored if `query_type` is 'instant'.
    step_seconds: The time series step size in seconds. Ignored if `query_type` is 'instant'.
    query_type: The type of query to use. Either 'range' or 'instant'.
    """
    if query_type == "range" and (end_rfc3339 is None or step_seconds is None):
        raise ValueError(
            "end_rfc3339 and step_seconds must be provided when query_type is 'range'"
        )
    start = datetime.fromisoformat(start_rfc3339)
    end = datetime.fromisoformat(end_rfc3339) if end_rfc3339 is not None else start
    interval_ms = step_seconds * 1000 if step_seconds is not None else None
    query = Query(
        refId="A",
        datasource=DatasourceRef(
            uid=datasource_uid,
            type="prometheus",
        ),
        queryType=query_type,
        expr=expr,  # type: ignore
        intervalMs=interval_ms,
    )
    response = await grafana_client.get().query(start, end, [query])
    return DSQueryResponse.model_validate_json(response)


async def list_prometheus_metric_metadata(
    datasource_uid: str,
    limit: int = 10,
    limit_per_metric: int = 10,
    metric: str | None = None,
) -> dict[str, list[PrometheusMetricMetadata]]:
    """
    List metadata for all metrics in Prometheus.

    # Parameters.

    datasource_uid: The uid of the Grafana datasource to query.
    limit: The maximum number of results to return. Defaults to 10.
    limit_per_metric: The maximum number of results to return per metric.
                      Defaults to 10.
    metric: Optionally, a metric name to filter the results by.

    # Returns.

    A mapping from metric name to all available metadata for that metric.
    """
    response = await grafana_client.get().list_prometheus_metric_metadata(
        datasource_uid,
        limit=limit,
        limit_per_metric=limit_per_metric,
        metric=metric,
    )
    return (
        ResponseWrapper[dict[str, list[PrometheusMetricMetadata]]]
        .model_validate_json(response)
        .data
    )


async def list_prometheus_metric_names(
    datasource_uid: str,
    regex: str,
    limit: int = 10,
    page: int = 1,
) -> list[str]:
    """
    List metric names in a Prometheus datasource that match the given regex.

    # Parameters.

    datasource_uid: The uid of the Grafana datasource to query.
    regex: The regex to match against the metric names. Uses Python's re.match.
    limit: The maximum number of results to return. Defaults to 10.
    page: The page number to return. Defaults to 1.

    # Returns.

    A list of metric names that match the given regex.
    """
    name_label_values = await list_prometheus_label_values(datasource_uid, "__name__")
    compiled = re.compile(regex)
    matches = (name for name in name_label_values if compiled.match(name))
    start = (page - 1) * limit
    stop = start + limit
    return list(itertools.islice(matches, start, stop))


async def list_prometheus_label_names(
    datasource_uid: str,
    matches: list[Selector] | None = None,
    start: datetime | None = None,
    end: datetime | None = None,
    limit: int = 100,
) -> list[str]:
    """
    List the label names in a Prometheus datasource, optionally filtered to those
    matching the given selectors and within the given time range.

    If you want to list the label names for a specific metric, pass a matcher
    like `{__name__="metric_name"}` to the `matches` parameter.

    # Parameters.

    datasource_uid: The uid of the Grafana datasource to query.
    matches: Optionally, a list of label matchers to filter the results by.
    start: Optionally, the start time of the time range to filter the results by.
    end: Optionally, the end time of the time range to filter the results by.
    limit: Optionally, the maximum number of results to return. Defaults to 100.
    """
    response = await grafana_client.get().list_prometheus_label_names(
        datasource_uid,
        matches=matches,
        start=start,
        end=end,
        limit=limit,
    )
    return ResponseWrapper[list[str]].model_validate_json(response).data


async def list_prometheus_label_values(
    datasource_uid: str,
    label_name: str,
    matches: list[Selector] | None = None,
    start: datetime | None = None,
    end: datetime | None = None,
    limit: int = 100,
):
    """
    Get the values of a label in Prometheus.

    # Parameters.

    datasource_uid: The uid of the Grafana datasource to query.
    label_name: The name of the label to query.
    matches: Optionally, a list of selectors to filter the results by.
    start: Optionally, the start time of the query.
    end: Optionally, the end time of the query.
    limit: Optionally, the maximum number of results to return. Defaults to 100.
    """
    response = await grafana_client.get().list_prometheus_label_values(
        datasource_uid,
        label_name,
        matches=matches,
        start=start,
        end=end,
        limit=limit,
    )
    return ResponseWrapper[list[str]].model_validate_json(response).data


def add_tools(mcp: FastMCP):
    mcp.add_tool(query_prometheus)
    mcp.add_tool(list_prometheus_metric_metadata)
    mcp.add_tool(list_prometheus_metric_names)
    mcp.add_tool(list_prometheus_label_names)
    mcp.add_tool(list_prometheus_label_values)
