import os

import pytest
import pytest_asyncio
from langevals import expect
from langevals_langevals.llm_boolean import (
    CustomLLMBooleanEvaluator,
    CustomLLMBooleanSettings,
)
from litellm import acompletion
from mcp.types import Tool
from mcp import ClientSession
from mcp.client.sse import sse_client

models = ["gpt-3.5-turbo", "gpt-4-turbo", "groq/llama3-70b-8192"]


@pytest.fixture
def mcp_url():
    return os.environ.get("MCP_GRAFANA_URL", "http://localhost:8000/sse")


@pytest.fixture
def grafana_headers():
    headers = {
        "X-Grafana-URL": os.environ.get("GRAFANA_URL", "http://localhost:3000"),
    }
    if key := os.environ.get("GRAFANA_API_KEY"):
        headers["X-Grafana-API-Key"] = key
    return headers


@pytest_asyncio.fixture
async def mcp_client(mcp_url, grafana_headers):
    async with sse_client(mcp_url, headers=grafana_headers) as (
        read,
        write,
    ):
        async with ClientSession(read, write) as session:
            # Initialize the connection
            await session.initialize()
            yield session


@pytest.mark.asyncio
@pytest.mark.parametrize("model", models)
async def test_loki(model: str, mcp_client: ClientSession):
    tools = await mcp_client.list_tools()
    prompt = "what are the most recent log lines from Grafana?"

    response = await acompletion(
        model=model,
        messages=[
            {"role": "system"},
            {"role": "user", "content": prompt},
        ],
        tools=[convert_tool(t) for t in tools.tools],
    )

    # Check that there is at least one tool call in the response.

    # Call the tool with the requested args.

    # Call the LLM including the tool call result.

    # Check that there's another tool call.

    # Call the tool with the requested args.

    # Call the LLM including the tool call result.

    # Check that the response has some log lines.
    content = response.choices[0].message.content  # type: ignore
    log_line_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response look like it contains Grafana log lines?",
        )
    )
    expect(input=prompt, output=content).to_pass(log_line_checker)


def convert_tool(tool: Tool) -> dict:
    return {
        "type": "function",
        "function": {
            "name": tool.name,
            "description": tool.description,
            "parameters": tool.inputSchema,
        },
    }
