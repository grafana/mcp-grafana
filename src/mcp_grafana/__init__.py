from mcp.server import FastMCP

from .tools import add_tools

# Create an MCP server
mcp = FastMCP("Grafana", log_level="DEBUG")
add_tools(mcp)
