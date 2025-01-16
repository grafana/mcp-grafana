from datetime import datetime, timedelta

import pytest

from mcp_grafana.client import GrafanaError
from mcp_grafana.grafana_types import DSQueryResponse, LabelMatcher, Selector
from mcp_grafana.tools.prometheus import (
    query_prometheus,
    list_prometheus_metric_metadata,
    list_prometheus_metric_names,
    list_prometheus_label_names,
    list_prometheus_label_values,
)

from . import mark_integration

pytestmark = mark_integration


class TestPrometheusQueries:
    @pytest.mark.parametrize("step_seconds", [15, 60, 300])
    async def test_query_prometheus_range(self, step_seconds):
        end = datetime.now()
        start = end - timedelta(minutes=10)

        results = await query_prometheus(
            "robustperception",
            start_rfc3339=start.isoformat(),
            end_rfc3339=end.isoformat(),
            step_seconds=step_seconds,
            expr="node_load1",
            query_type="range",
        )
        query_result = results.results["A"]
        assert query_result.status == 200
        assert (
            query_result.frames[0].data.values[0][0] - start.timestamp() * 1000
            < step_seconds * 1000
        )
        assert (
            len(query_result.frames[0].data.values[0])
            == (end - start).total_seconds() // step_seconds + 1
        )

    async def test_query_prometheus_instant(self):
        timestamp = datetime.now()
        results = await query_prometheus(
            "robustperception",
            start_rfc3339=timestamp.isoformat(),
            expr="up",
            query_type="instant",
        )
        query_result = results.results["A"]

        # Verify the response
        assert query_result.status == 200
        assert len(query_result.frames) > 0
        # Instant queries should return a single data point
        assert len(query_result.frames[0].data.values[0]) == 1
        # Verify we have both time and value columns
        assert len(query_result.frames[0].data.values) == 2
        # Verify the timestamp is close to our request time (within 60 seconds,
        # we don't know what the scrape interval is).
        assert (
            abs(query_result.frames[0].data.values[0][0] - timestamp.timestamp() * 1000)
            < 60000
        )

    async def test_query_prometheus_range_missing_params(self):
        start = datetime.now()
        with pytest.raises(
            ValueError, match="end_rfc3339 and step_seconds must be provided"
        ):
            await query_prometheus(
                "robustperception",
                start_rfc3339=start.isoformat(),
                expr="node_load1",
                query_type="range",
            )

    async def test_query_prometheus_invalid_query(self):
        end = datetime.now()
        start = end - timedelta(minutes=10)

        try:
            results = await query_prometheus(
                "robustperception",
                start_rfc3339=start.isoformat(),
                end_rfc3339=end.isoformat(),
                step_seconds=60,
                expr="invalid_metric{",  # Invalid PromQL syntax
                query_type="range",
            )
        except Exception as e:
            assert isinstance(e, GrafanaError)
            results = DSQueryResponse.model_validate_json(str(e))
        query_result = results.results["A"]
        assert query_result.status == 400
        assert query_result.error is not None
        assert "parse error" in query_result.error

    async def test_query_prometheus_empty_result(self):
        end = datetime.now()
        start = end - timedelta(minutes=10)

        results = await query_prometheus(
            "robustperception",
            start_rfc3339=start.isoformat(),
            end_rfc3339=end.isoformat(),
            step_seconds=60,
            expr='metric_that_does_not_exist{label="value"}',
            query_type="range",
        )
        query_result = results.results["A"]
        assert query_result.status == 200
        assert len(query_result.frames) == 1
        assert len(query_result.frames[0].data.values) == 0


async def test_list_prometheus_metric_metadata():
    # Test fetching metric metadata
    metadata = await list_prometheus_metric_metadata("robustperception", 10)
    assert 0 < len(metadata) <= 10


async def test_list_prometheus_metric_names():
    # Test listing available metric names
    metric_names = await list_prometheus_metric_names("robustperception", ".*")
    assert isinstance(metric_names, list)
    assert len(metric_names) > 0


async def test_list_prometheus_label_names():
    # Test listing label names for a metric
    label_names = await list_prometheus_label_names(
        "robustperception",
        [Selector(filters=[LabelMatcher(name="job", value="node")])],
    )
    assert isinstance(label_names, list)
    assert len(label_names) > 0
    assert "instance" in label_names  # 'instance' is a common label


async def test_list_prometheus_label_values():
    # Test listing values for a specific label
    label_values = await list_prometheus_label_values("robustperception", "job")
    assert isinstance(label_values, list)
    assert len(label_values) > 0
