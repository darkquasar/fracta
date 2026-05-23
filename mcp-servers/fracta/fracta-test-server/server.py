"""Streamable-HTTP MCP test server used to exercise gateway policy enforcement.

Exposes four trivially-shaped tools so the gateway's tool-policy
filtering can be observed end-to-end:

  ping                - always visible
  echo                - always visible
  forbidden_action    - placed on the policy deny list
  restricted_action   - excluded from the policy allow_only list
"""

import os

from mcp.server.fastmcp import FastMCP

mcp = FastMCP(
    "fracta-test-server",
    host=os.environ.get("FASTMCP_HOST", "0.0.0.0"),
    port=int(os.environ.get("FASTMCP_PORT", "8000")),
)


@mcp.tool()
def ping() -> str:
    return "pong"


@mcp.tool()
def echo(message: str) -> str:
    return f"echo: {message}"


@mcp.tool()
def forbidden_action() -> str:
    return "should be blocked by deny policy"


@mcp.tool()
def restricted_action() -> str:
    return "should be excluded by allow_only policy"


if __name__ == "__main__":
    mcp.run(transport="streamable-http")
