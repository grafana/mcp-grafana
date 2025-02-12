from datetime import datetime
from typing import Literal

from mcp.server import FastMCP
from pydantic import BaseModel, Field

from ..client import (
    AddActivityToIncidentArguments,
    CreateIncidentArguments,
    grafana_client,
)
from ..grafana_types import QueryIncidentPreviewsRequest, IncidentPreviewsQuery


class ListIncidentsArguments(BaseModel):
    """
    Arguments for the list_incidents tool.
    """

    # Note: this differs from what the client expects (which is a closer match
    # to the API) so that we can make it easier for LLM clients to use sensible
    # arguments without them having to know the query syntax.

    limit: int = Field(default=10, ge=1, le=100)
    drill: bool = Field(
        default=False, description="Whether to include drill incidents."
    )
    status: Literal["resolved", "active"] | None = Field(
        default=None,
        description="The status of the incidents to include. If not provided, all incidents will be included.",
    )


async def list_incidents(arguments: ListIncidentsArguments) -> bytes:
    """
    List incidents from the Grafana Incident incident management tool.

    Incidents will be returned in descending order by creation time.
    """
    query_string = "isdrill:false" if not arguments.drill else ""
    if arguments.status is not None:
        query_string += f" and status:{arguments.status}"
    body = QueryIncidentPreviewsRequest(
        query=IncidentPreviewsQuery(
            queryString=query_string,
            orderDirection="DESC",
            orderField="createdTime",
            limit=arguments.limit,
        ),
        includeCustomFieldValues=True,
    )
    return await grafana_client.get().list_incidents(body)


async def create_incident(arguments: CreateIncidentArguments) -> bytes:
    """
    Create an incident in the Grafana Incident incident management tool.
    """
    return await grafana_client.get().create_incident(arguments)


async def add_activity_to_incident(
    incident_id: str, body: str, event_time: datetime | None = None
) -> bytes:
    """
    Add an activity to an incident in the Grafana Incident incident management tool.

    :param incident_id: The ID of the incident to add the activity to.
    :param body: The body of the activity. URLs will be parsed and attached as context.
    :param event_time: The time that the activity occurred. If not provided, the current time will be used.
                       If provided, it must be in RFC3339 format.
    """
    return await grafana_client.get().add_activity_to_incident(
        AddActivityToIncidentArguments(
            incidentId=incident_id,
            body=body,
            activityKind="userNote",
            eventTime=event_time,
        )
    )


async def resolve_incident(incident_id: str, summary: str) -> bytes:
    """
    Resolve an incident in the Grafana Incident incident management tool.

    :param incident_id: The ID of the incident to resolve.
    :param summary: The summary of the incident now it has been resolved.
                    This should be succint but thorough and informative,
                    enough to serve as a mini post-incident-report.
    """
    return await grafana_client.get().close_incident(incident_id, summary)


def add_tools(mcp: FastMCP):
    mcp.add_tool(list_incidents)
    mcp.add_tool(create_incident)
    mcp.add_tool(add_activity_to_incident)
    mcp.add_tool(resolve_incident)
