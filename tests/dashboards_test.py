import os

from litellm.types.utils import ModelResponse
import pytest
from langevals import expect
from langevals_langevals.llm_boolean import (
    CustomLLMBooleanEvaluator,
    CustomLLMBooleanSettings,
)
from litellm import Message, acompletion
from mcp import ClientSession
from mcp.client.sse import sse_client
from dotenv import load_dotenv

from utils import (
    get_converted_tools,
    llm_tool_call_sequence,
)


load_dotenv()

DEFAULT_GRAFANA_URL = "http://localhost:3000"
DEFAULT_MCP_URL = "http://localhost:8000/sse"

models = ["gpt-4o", "claude-3-5-sonnet-20240620"]

pytestmark = pytest.mark.anyio


@pytest.fixture
def mcp_url():
    return os.environ.get("MCP_GRAFANA_URL", DEFAULT_MCP_URL)


@pytest.fixture
def grafana_headers():
    headers = {
        "X-Grafana-URL": os.environ.get("GRAFANA_URL", DEFAULT_GRAFANA_URL),
    }
    if key := os.environ.get("GRAFANA_API_KEY"):
        headers["X-Grafana-API-Key"] = key
    return headers


@pytest.fixture
async def mcp_client(mcp_url, grafana_headers):
    async with sse_client(mcp_url, headers=grafana_headers) as (
        read,
        write,
    ):
        async with ClientSession(read, write) as session:
            await session.initialize()
            yield session


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_dashboard_panel_queries_tool(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you list the panel queries for the dashboard with UID fe9gm6guyzi0wd?"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    # 1. Call the dashboard panel queries tool
    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "get_dashboard_panel_queries",
        {"uid": "fe9gm6guyzi0wd"}
    )

    # 2. Final LLM response
    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    panel_queries_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain specific information about the panel queries and titles for a grafana dashboard?",
        )
    )
    print("content", content)
    expect(input=prompt, output=content).to_pass(panel_queries_checker)
