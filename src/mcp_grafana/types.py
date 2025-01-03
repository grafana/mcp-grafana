"""
Pydantic models for the Grafana API.
"""

import textwrap
from datetime import datetime
from typing import Annotated, Any, Literal

from pydantic import UUID4, BaseModel, ConfigDict, Field, PlainSerializer, TypeAdapter

from .settings import grafana_settings


CustomDateTime = Annotated[
    datetime,
    PlainSerializer(lambda _datetime: _datetime.isoformat(), return_type=str),
]


class Datasource(BaseModel):
    id: int
    uid: str
    name: str
    type: str
    access: str
    url: str
    user: str
    database: str
    basic_auth: bool = Field(alias="basicAuth")
    basic_auth_user: str = Field(alias="basicAuthUser")
    is_default: bool = Field(alias="isDefault")
    with_credentials: bool = Field(alias="withCredentials")
    json_data: dict[str, Any] = Field(alias="jsonData", default_factory=dict)
    secure_json_fields: dict[str, bool] = Field(
        alias="secureJsonData", default_factory=dict
    )


class SearchDashboardsArguments(BaseModel):
    query: str | None
    tags: list[str] | None = None
    type: Literal["dash-db"] | Literal["dash-folder"] | None = None
    dashboard_ids: list[int] | None = None
    dashboard_uids: list[str] | None = None
    folder_uids: list[str] | None = None
    only_starred: bool = False
    limit: int = grafana_settings.tools.search.limit
    page: int = 1


class DatasourceRef(BaseModel):
    uid: str = Field(description="The unique uid of the datasource")
    type: str = Field(description="The type of the datasource")


class Query(BaseModel):
    # Queries can have arbitrary other fields.
    model_config = ConfigDict(extra="allow")

    refId: str = Field(alias="ref_id")
    datasource: DatasourceRef = Field(description="The datasource to query")
    queryType: str = Field(
        alias="query_type", description="The type of query, e.g. 'range'"
    )
    intervalMs: int | None = Field(
        alias="interval_ms",
        description="The time series time interval in milliseconds",
        default=None,
    )


query_list = TypeAdapter(list[Query])


Labels = dict[str, str]


class Config(BaseModel):
    display_name_from_ds: str | None = Field(alias="displayNameFromDS", default=None)


class SchemaField(BaseModel):
    name: str
    labels: dict[str, str] = Field(default_factory=dict)
    config: Config | None = Field(default=None)


class FrameSchema(BaseModel):
    name: str | None = None
    fields: list[SchemaField]


class FrameData(BaseModel):
    values: list[list[Any]]


class Frame(BaseModel):
    frame_schema: FrameSchema = Field(alias="schema")
    data: FrameData


class QueryResult(BaseModel):
    frames: list[Frame]
    error: str | None = None
    status: int | None = None


class DSQueryResponse(BaseModel):
    results: dict[str, QueryResult]


class ListIncidentsArguments(BaseModel):
    query: str | None = None
    limit: int = Field(default=10, ge=1, le=100)
    status: Literal["resolved", "active"] | None = Field(default=None)


class IncidentPreviewQuery(BaseModel):
    limit: int
    orderDirection: str = Field(alias="order_direction")
    orderField: str = Field(alias="order_field")
    queryString: str = Field(alias="query_string")


class IncidentPreviewBody(BaseModel):
    query: IncidentPreviewQuery
    includeCustomFieldValues: Literal[True] = Field(alias="include_custom_field_values")


class Label(BaseModel):
    key: str
    label: str


class IncidentPreview(BaseModel):
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
    roomPrefix: str = Field(alias="room_prefix", default="incident")
    isDrill: bool = Field(alias="is_drill", default=False)
    status: Literal["resolved", "active"] = Field(default="active")
    attachCaption: str = Field(alias="attach_caption", default="")
    attachUrl: str = Field(alias="attach_url", default="")
    labels: list[Label] = Field(alias="labels", default=[])


class AddActivityToIncidentArguments(BaseModel):
    incidentId: str = Field(alias="incident_id")
    activityKind: Literal["userNote"] = Field(
        alias="activity_kind",
        default="userNote",
        description="The type of activity this item represents. For now, only 'userNote' is supported.",
    )
    body: str = Field(
        description="A human readable description of the activity. URLs will be parsed and attached as context."
    )
    eventTime: CustomDateTime | None = Field(
        alias="event_time",
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
    requestData: SiftInvestigationRequestData = Field(
        alias="request_data", description="Inputs to the investigation."
    )

    # Don't bother with these for now.
    # inputs: list[SiftInvestigationInput] = Field(
    #     alias="inputs",
    #     description="The inputs to the investigation.",
    # )


class ResponseWrapper[T](BaseModel):
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
