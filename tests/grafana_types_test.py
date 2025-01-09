import pytest

from mcp_grafana.grafana_types import (
    AddActivityToIncidentArguments,
    Datasource,
    DatasourceRef,
    IncidentPreviewsQuery,
    ListIncidentsArguments,
    Query,
    QueryIncidentPreviewsRequest,
    SearchDashboardsArguments,
)

cases = [
    (
        SearchDashboardsArguments,
        {
            "query": "machine learning",
            "dashboardIds": [1234],
            "starred": True,
        },
    ),
    (
        Datasource,
        {
            "id": 1,
            "uid": "prometheus",
            "type": "prometheus",
            "name": "Prometheus",
            "user": "",
            "database": "",
            "url": "http://prometheus:9090",
            "access": "proxy",
            "basicAuth": False,
            "isDefault": True,
            "jsonData": {
                "httpMethod": "GET",
            },
        },
    ),
    (
        DatasourceRef,
        {
            "uid": "prometheus",
            "type": "prometheus",
        },
    ),
    (
        Query,
        {
            "refId": "A",
            "datasource": {
                "uid": "prometheus",
                "type": "prometheus",
            },
            "queryType": "range",
            "intervalMs": 10000,
            "expr": "node_load1",
        },
    ),
    (
        ListIncidentsArguments,
        {
            "query": "machine learning",
            "status": "active",
        },
    ),
]


class TestRoundTripTypes:
    """
    Test that our models are configured correctly. This isn't strictly
    needed but I like to have confidence that we're using the right combinations
    of `by_alias`, `exclude_unset`, etc in Pydantic.
    """

    @pytest.mark.parametrize(
        "cls,value",
        cases,
    )
    def test_roundtrip(self, cls, value):
        instance = cls.model_validate(value)
        dict_value = instance.model_dump()
        assert dict_value == value


class TestIncidentModels:
    """
    These models are constructed manually in the code rather than by the LLM,
    but I'm never 100% sure exactly how pydantic's serialization will use aliases,
    so these tests are here they work as expected.
    """

    def test_incident_preview(self):
        query = IncidentPreviewsQuery(
            queryString="machine learning",
            orderDirection="ASC",
            orderField="createdTime",
            limit=10,
        )
        body = QueryIncidentPreviewsRequest(
            query=query,
            includeCustomFieldValues=True,
        )
        assert body.model_dump() == {
            "query": {
                "queryString": "machine learning",
                "orderDirection": "ASC",
                "orderField": "createdTime",
                "limit": 10,
            },
            "includeCustomFieldValues": True,
        }

    def test_add_activity_to_incident(self):
        body = AddActivityToIncidentArguments(
            incidentId="1234",
            activityKind="userNote",
            body="This is a test",
        )
        assert body.model_dump() == {
            "incidentId": "1234",
            "activityKind": "userNote",
            "body": "This is a test",
        }
