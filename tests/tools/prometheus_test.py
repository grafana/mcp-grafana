from datetime import datetime, timedelta

import pytest

from mcp_grafana.grafana_types import LabelMatcher, Selector
from mcp_grafana.tools.prometheus import (
    query_prometheus,
    get_prometheus_metric_metadata,
    get_prometheus_metric_names,
    get_prometheus_label_names,
    get_prometheus_label_values,
)

from . import mark_integration

pytestmark = mark_integration


@pytest.mark.parametrize("step_seconds", [15, 60, 300])
async def test_query_prometheus_range(step_seconds):
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


async def test_get_prometheus_metric_metadata():
    # Test fetching metric metadata
    metadata = await get_prometheus_metric_metadata("robustperception", 10)
    assert 0 < len(metadata) <= 10


async def test_get_prometheus_metric_names():
    # Test getting list of available metric names
    metric_names = await get_prometheus_metric_names("robustperception", ".*")
    assert isinstance(metric_names, list)
    assert len(metric_names) > 0
    assert "up" in metric_names  # 'up' metric should always be present


async def test_get_prometheus_label_names():
    # Test getting list of label names for a metric
    label_names = await get_prometheus_label_names(
        "robustperception",
        [Selector(filters=[LabelMatcher(name="job", value="node")])],
    )
    assert isinstance(label_names, list)
    assert len(label_names) > 0
    assert "instance" in label_names  # 'instance' is a common label


async def test_get_prometheus_label_values():
    # Test getting values for a specific label
    label_values = await get_prometheus_label_values("robustperception", "job")
    assert isinstance(label_values, list)
    assert len(label_values) > 0
