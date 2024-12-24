from mcp.server import FastMCP

from . import search


def add_tools(mcp: FastMCP):
    search.add_tools(mcp)
