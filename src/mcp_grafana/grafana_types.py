"""
Pydantic models for the Grafana API.
"""

import textwrap
from datetime import datetime
from typing import Annotated, Any, Generic, Literal, TypeVar


import pydantic
from pydantic import UUID4, ConfigDict, Field, PlainSerializer, TypeAdapter

from .settings import grafana_settings


class BaseModel(pydantic.BaseModel):
    """
    Custom base model that excludes unset and None values, and uses aliases
    when serializing.
    """

    def model_dump(self, *args, **kwargs):
        return super().model_dump(
            exclude_unset=True, exclude_none=True, by_alias=True, *args, **kwargs
        )


CustomDateTime = Annotated[
    datetime,
    PlainSerializer(lambda _datetime: _datetime.isoformat(), return_type=str),
]


class Datasource(BaseModel):
    """
    A Grafana datasource.
    """

    id: int = Field(description="The ID of the datasource")
    uid: str = Field(description="The UID of the datasource")
    name: str = Field(description="The name of the datasource")
    type: str = Field(description="The type of the datasource")
    access: str = Field(description="The access mode of the datasource")
    url: str = Field(description="The URL of the datasource")
    user: str = Field(description="The user to connect to the datasource")
    database: str = Field(description="The database to connect to the datasource")
    basic_auth: bool = Field(alias="basicAuth", description="Whether to use basic auth")
    basic_auth_user: str | None = Field(
        None, alias="basicAuthUser", description="The user to use for basic auth"
    )
    is_default: bool = Field(
        alias="isDefault", description="Whether the datasource is the default"
    )
    json_data: dict[str, Any] = Field(alias="jsonData", default_factory=dict)
    secure_json_fields: dict[str, bool] = Field(
        alias="secureJsonData", default_factory=dict
    )


class SearchDashboardsArguments(BaseModel):
    """
    Arguments for the `search_dashboards` tool and API endpoint.
    """

    query: str | None = Field(description="The query to search for")
    tags: list[str] | None = Field(
        default=None, description="Only return dashboards with these tags"
    )
    type: Literal["dash-db"] | Literal["dash-folder"] | None = Field(
        default=None,
        description="Whether to search for dashboards ('dash-db') or folders ('dash-folder').",
    )
    dashboard_ids: list[int] | None = Field(
        default=None,
        alias="dashboardIds",
        description="List of dashboard IDs to search for",
    )
    dashboard_uids: list[str] | None = Field(
        default=None,
        serialization_alias="dashboardUIDs",
        description="List of dashboard UIDs to search for",
    )
    folder_uids: list[str] | None = Field(
        default=None,
        serialization_alias="folderUIDs",
        description="List of folder UIDs to search for",
    )
    starred: bool = Field(default=False, description="Only include starred dashboards")
    limit: int = Field(
        default=grafana_settings.get().tools.search.limit,
        description="Limit the number of returned results",
    )
    page: int = Field(default=1)


class DatasourceRef(BaseModel):
    """
    A reference to a Grafana datasource, generally used in queries.
    """

    uid: str = Field(description="The unique uid of the datasource")
    type: str = Field(description="The type of the datasource")


class Query(BaseModel):
    """
    A query to run against a datasource using the `/api/ds/query` endpoint.
    """

    # Queries can have arbitrary other fields.
    model_config = ConfigDict(extra="allow")

    ref_id: str = Field(alias="refId")
    datasource: DatasourceRef = Field(description="The datasource to query")
    query_type: str = Field(
        alias="queryType", description="The type of query, e.g. 'range'"
    )
    interval_ms: int | None = Field(
        None,
        alias="intervalMs",
        description="The time series time interval in milliseconds",
    )


query_list = TypeAdapter(list[Query])


Labels = dict[str, str]


class Config(BaseModel):
    """
    Configuration for a field in a DataFrame's schema.
    """

    display_name_from_ds: str | None = Field(alias="displayNameFromDS", default=None)


class SchemaField(BaseModel):
    """
    The schema of a DataFrame's field.
    """

    name: str
    labels: dict[str, str] = Field(default_factory=dict)
    config: Config | None = Field(default=None)


class FrameSchema(BaseModel):
    """
    The schema of a DataFrame.
    """

    name: str | None = None
    fields: list[SchemaField]


class FrameData(BaseModel):
    """
    The data of a DataFrame.
    """

    values: list[list[Any]]


class Frame(BaseModel):
    """
    A DataFrame returned by a query.
    """

    frame_schema: FrameSchema = Field(alias="schema")
    data: FrameData


class QueryResult(BaseModel):
    """
    The result of executing a query.
    """

    frames: list[Frame] = Field(description="The data frames returned by the query")
    error: str | None = Field(None, description="An error message if the query failed")
    status: int | None = Field(None, description="The status code of the query")


class DSQueryResponse(BaseModel):
    """
    The response from the `/api/ds/query` endpoint.
    """

    results: dict[str, QueryResult] = Field(
        description="The results of the query, keyed by the refId of the input queries"
    )


class ListIncidentsArguments(BaseModel):
    """
    Arguments for the `list_incidents` tool.
    """

    query: str | None = None
    limit: int = Field(default=10, ge=1, le=100)
    status: Literal["resolved", "active"] | None = Field(default=None)


class IncidentPreviewsQuery(BaseModel):
    """
    A query for the `QueryIncidentPreviews` endpoint.

    Ref: https://grafana.com/docs/grafana-cloud/alerting-and-irm/irm/incident/api/reference/#incidentpreviewsquery
    """

    limit: int
    order_direction: str = Field(alias="orderDirection")
    order_field: str = Field(alias="orderField")
    query_string: str = Field(alias="queryString")


class QueryIncidentPreviewsRequest(BaseModel):
    """
    A request to the `QueryIncidentPreviews` endpoint.

    Ref: https://grafana.com/docs/grafana-cloud/alerting-and-irm/irm/incident/api/reference/#queryincidentpreviews
    """

    query: IncidentPreviewsQuery
    include_custom_field_values: Literal[True] = Field(alias="includeCustomFieldValues")


class Label(BaseModel):
    key: str
    label: str


class IncidentPreview(BaseModel):
    """
    A preview of an incident.
    """

    incident_id: str = Field(alias="incidentID")
    title: str
    description: str
    summary: str
    severity_label: str = Field(alias="severityLabel")
    status: str
    is_drill: bool = Field(alias="isDrill")
    incident_type: str = Field(alias="incidentType")
    created_time: str = Field(alias="createdTime")
    closed_time: str = Field(alias="closedTime")
    labels: list[Label]


class IncidentPreviewResponse(BaseModel):
    incident_previews: list[IncidentPreview] = Field(alias="incidentPreviews")


class CreateIncidentArguments(BaseModel):
    title: str = Field(description="The title of the incident")
    description: str = Field(description="A human readable description of the incident")
    severity: Literal["minor", "major", "critical"] | None = Field(
        description="The severity of the incident", default=None
    )
    room_prefix: str = Field(alias="roomPrefix", default="incident")
    is_drill: bool = Field(alias="isDrill", default=False)
    status: Literal["resolved", "active"] = Field(default="active")
    attach_caption: str = Field(alias="attachCaption", default="")
    attach_url: str = Field(alias="attachUrl", default="")
    labels: list[Label] = Field(alias="labels", default=[])


class AddActivityToIncidentArguments(BaseModel):
    incident_id: str = Field(alias="incidentId")
    activity_kind: Literal["userNote"] = Field(
        alias="activityKind",
        description="The type of activity this item represents. For now, only 'userNote' is supported.",
    )
    body: str = Field(
        description="A human readable description of the activity. URLs will be parsed and attached as context."
    )
    event_time: CustomDateTime | None = Field(
        alias="eventTime",
        default=None,
        description="The time that the activity occurred. If not provided, the current time will be used.",
    )


class SiftInvestigationRequestData(BaseModel):
    labels: dict[str, str] = Field(
        alias="labels",
        description=textwrap.dedent(
            """
                Labels used to scope the investigation.
                These will be used when searching for relevant metrics, logs and traces within Grafana datasources.
                Including the 'cluster' and 'namespace' labels is highly recommended, where possible."
            """
        ).strip(),
    )
    start: CustomDateTime = Field(
        alias="start",
        description=textwrap.dedent(
            """
                The start time of the investigation.
                If not provided, the current time minus 30 minutes will be used.
            """
        ).strip(),
    )
    end: CustomDateTime = Field(
        alias="end",
        description=textwrap.dedent(
            """
                The end time of the investigation.
                If not provided, the current time will be used.
            """
        ).strip(),
    )


# class SiftLabelMatcherInput(BaseModel):
#     name: str = Field(description="The name of the label to match.")
#     value: str = Field(description="The value of the label.")
#     type: Literal[0, 1, 2, 3] = Field(
#         description=textwrap.dedent(
#             """
#             The type of the label matcher:

#                 0: 'Equal' ('=')
#                 1: 'Not equal' ('!=')
#                 2: 'Regex' ('~')
#                 3: 'Not regex' ('!~')
#         """
#         ).strip(),
#     )

# SiftInvestigationInput = SiftLabelMatcherInput | ...


class CreateSiftInvestigationArguments(BaseModel):
    name: str = Field(description="The name of the investigation.")
    request_data: SiftInvestigationRequestData = Field(
        alias="requestData", description="Inputs to the investigation."
    )

    # Don't bother with these for now.
    # inputs: list[SiftInvestigationInput] = Field(
    #     alias="inputs",
    #     description="The inputs to the investigation.",
    # )


T = TypeVar("T")


class ResponseWrapper(BaseModel, Generic[T]):
    status: str
    data: T


SiftAnalysisStatus = Literal["pending", "skipped", "running", "finished"]
SiftCheckStage = Literal[
    "Experimental", "PublicPreview", "GeneralAvailability", "Deprecated", "Dummy"
]


class SiftAnalysisPreview(BaseModel):
    """
    A reduced version of a Sift analysis returned in the investigation body.
    """

    id: UUID4
    name: str
    status: SiftAnalysisStatus
    stage: SiftCheckStage
    interesting: bool = Field(
        description="Whether the analysis found interesting results"
    )


class SiftAnalysisMeta(BaseModel):
    counts_by_stage: dict[SiftCheckStage, dict[str, int]] = Field(
        description="The number of checks that were run for each stage, and their status.",
        alias="countsByStage",
    )
    items: list[SiftAnalysisPreview] | None = Field(
        description=textwrap.dedent(
            """
            A preview of the analyses that comprise this investigation.

            Full details can be obtained using the 'get Sift analysis' API.
        """
        ).strip(),
    )


SiftInvestigationStatus = Literal["pending", "running", "finished", "failed"]


class SiftInvestigation(BaseModel):
    id: UUID4 = Field(description="The ID of the investigation.")
    name: str = Field(description="The name of the investigation.")
    analyses: SiftAnalysisMeta = Field(
        description="Summary of analyses that were run for this investigation."
    )
    status: SiftInvestigationStatus

    @property
    def terminated(self) -> bool:
        """
        Whether the investigation is in a terminal state i.e. failed or finished.
        """
        return self.status in ("finished", "failed")


class SiftAnalysisEvent(BaseModel):
    name: str = Field(description="The name of the event")
    description: str = Field(description="The description of the event")
    details: Any = Field(description="The details of the event")
    start_time: datetime = Field(
        alias="startTime", description="The start time of the event"
    )
    end_time: datetime = Field(alias="endTime", description="The end time of the event")


class SiftAnalysisResult(BaseModel):
    interesting: bool = Field(
        description="Whether the analysis found interesting results"
    )
    message: str = Field("The summary message of the analysis")
    details: Any = Field(
        "The details of the analysis. The content of this field will vary depending on the check."
    )
    events: list[SiftAnalysisEvent] | None = Field(default=None)


class SiftAnalysis(BaseModel):
    id: UUID4 = Field(description="The ID of the analysis")
    investigation_id: UUID4 = Field(
        description="The ID of the investigation", alias="investigationId"
    )
    name: str = Field(description="The name of the analysis")
    stage: SiftCheckStage = Field(description="The stage of the check")
    status: SiftAnalysisStatus = Field(description="The status of the analysis")
    title: str = Field("The title of the analysis")
    result: SiftAnalysisResult = Field(description="The result of the analysis")


class PrometheusMetricMetadata(BaseModel):
    type: str = Field(
        description="The type of the metric, e.g. counter, gauge, histogram, summary."
    )
    help: str = Field(description="The help text for the metric.")
    unit: str = Field(description="The unit of the metric.")


class LabelMatcher(BaseModel):
    name: str = Field(description="The name of the label to match against.")
    value: str = Field(description="The value of the label.")
    type: Literal["=", "!=", "~", "!~"] = Field(
        default="=",
        description=textwrap.dedent(
            """
            The type of the label matcher:
            
            - `=`: Exact match
            - `!=`: Not equal
            - `~`: Regex match
            - `!~`: Not regex match
            """
        ).strip(),
    )

    def __str__(self):
        return f"{self.name}{self.type}'{self.value}'"


class Selector(BaseModel):
    """
    A selector is a combination of label matchers.

    All label matchers in a selector must match for the selector to match.

    Example:

        `{name="foo", bar!="baz"}`

        is represented as

        `Selector(filters=[LabelMatcher(name="name", value="foo"), LabelMatcher(name="bar", type="!=", value="baz")])`

    """

    filters: list[LabelMatcher]

    def __str__(self):
        filters = ", ".join(str(f) for f in self.filters)
        return f"{{{filters}}}"
