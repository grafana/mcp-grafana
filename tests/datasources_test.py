import json
import uuid

import pytest
from mcp import ClientSession

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop


pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_create_datasource_flow(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    # Datasource names must be unique in Grafana, so use a fresh name each run to
    # avoid collisions across the parametrized models, flaky reruns, and repeated
    # suite invocations.
    datasource_name = f"Test Prometheus DS {uuid.uuid4().hex[:8]}"

    # create_datasource enforces a two-call flow: the first call returns a field
    # schema, and the second call (with schemaReviewed=true) actually creates the
    # datasource. There is no human in this loop, so the prompt pre-confirms every
    # value and tells the model to proceed without pausing to ask.
    prompt = (
        f"Create a new Prometheus datasource named '{datasource_name}' with URL "
        "http://localhost:9090 and access mode proxy. I confirm all of these values "
        "are correct; do not ask me any follow-up questions. Review the field schema "
        "and then proceed to create the datasource."
    )

    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    create_calls = [tc for tc in tools_called if tc.name == "create_datasource"]
    assert create_calls, "create_datasource was not in tools_called"

    # The two-call flow means the final, creating call must carry schemaReviewed=true.
    reviewed_calls = [
        tc for tc in create_calls if tc.args.get("schemaReviewed") is True
    ]
    assert reviewed_calls, (
        "Expected at least one create_datasource call with schemaReviewed=true, "
        f"got args: {[tc.args for tc in create_calls]}"
    )

    creating_call = reviewed_calls[-1]
    assert creating_call.args.get("type") == "prometheus", (
        f"Expected type='prometheus', got {creating_call.args.get('type')!r}"
    )

    # The successful creation result carries the new datasource UID.
    created_uid = None
    for tc in reviewed_calls:
        if tc.result and tc.result.content:
            try:
                payload = json.loads(tc.result.content[0].text)
            except (json.JSONDecodeError, AttributeError, IndexError):
                continue
            if payload.get("uid"):
                created_uid = payload["uid"]
    assert created_uid, "create_datasource did not return a UID for the new datasource"

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response confirm that a Prometheus datasource was created "
        "successfully (e.g. mentions the datasource name, a UID, or a config link)?",
        expected_tools="create_datasource",
    )
