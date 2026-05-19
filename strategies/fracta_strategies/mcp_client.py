"""MCP Gateway client for mid-execution tool calls from strategy steps.

Connects to the fracta gateway's per-agent MCP endpoint (/agents/{task}/mcp)
using standard HTTP JSON-RPC. Tool visibility is enforced server-side by the
gateway — the client does not need to know or enforce the policy.
"""

import json
import urllib.request
import urllib.error
from urllib.parse import quote
from typing import Any


class MCPGatewayClient:
    """Calls MCP tools through the fracta gateway, scoped to an agent's visibility.

    Usage in a strategy step:
        @step("Enrich IPs")
        def enrich(self, ctx):
            if not ctx.mcp:
                return {"error": "no gateway access"}
            tools = ctx.mcp.list_tools()
            result = ctx.mcp.call_tool("elastic.platform_core_search", {
                "query": "source.ip:10.0.0.1"
            })
            return result
    """

    def __init__(self, gateway_url: str, agent_task: str, timeout: int = 30):
        base = gateway_url.rstrip("/")
        self.endpoint = f"{base}/agents/{quote(agent_task, safe='')}/mcp"
        self.agent_task = agent_task
        self.timeout = timeout
        self._request_id = 0
        self._initialized = False

    def _next_id(self) -> int:
        self._request_id += 1
        return self._request_id

    def _ensure_initialized(self):
        if self._initialized:
            return
        self._send_jsonrpc("initialize", {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "fracta-strategy-sidecar", "version": "1.0.0"},
        })
        self._initialized = True

    def _send_jsonrpc(self, method: str, params: dict | None = None) -> Any:
        payload: dict[str, Any] = {
            "jsonrpc": "2.0",
            "id": self._next_id(),
            "method": method,
        }
        if params is not None:
            payload["params"] = params

        data = json.dumps(payload).encode("utf-8")
        req = urllib.request.Request(
            self.endpoint,
            data=data,
            headers={"Content-Type": "application/json"},
            method="POST",
        )

        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                body = resp.read().decode("utf-8")
                result = json.loads(body)
                if "error" in result:
                    err = result["error"]
                    raise MCPToolError(
                        err.get("message", "Unknown MCP error"),
                        code=err.get("code", -1),
                    )
                return result.get("result", {})
        except (MCPToolError, MCPGatewayConnectionError):
            raise
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", errors="replace")
            raise MCPToolError(
                f"Gateway returned HTTP {e.code}: {body[:200]}",
                code=e.code,
            ) from e
        except urllib.error.URLError as e:
            raise MCPGatewayConnectionError(
                f"Gateway connection failed ({self.endpoint}): {e.reason}"
            ) from e
        except (json.JSONDecodeError, OSError, TimeoutError, ValueError, TypeError) as e:
            raise MCPGatewayConnectionError(
                f"Gateway communication error ({self.endpoint}): {e}"
            ) from e
        except Exception as e:
            raise MCPGatewayConnectionError(
                f"Unexpected gateway error ({self.endpoint}): {type(e).__name__}: {e}"
            ) from e

    def list_tools(self) -> list[dict]:
        """List tools visible to this agent (filtered by gateway policy)."""
        self._ensure_initialized()
        result = self._send_jsonrpc("tools/list")
        return result.get("tools", [])

    def call_tool(self, tool_name: str, arguments: dict | None = None) -> Any:
        """Call a namespaced MCP tool (e.g. 'elastic.platform_core_search').

        Returns the tool's result content. Text content is parsed as JSON
        if possible, otherwise returned as a string.

        Raises:
            MCPToolError: Tool returned an error or was rejected by policy.
            MCPGatewayConnectionError: Gateway is unreachable.
        """
        self._ensure_initialized()
        params: dict[str, Any] = {"name": tool_name}
        if arguments:
            params["arguments"] = arguments
        result = self._send_jsonrpc("tools/call", params)

        if result.get("isError"):
            content = result.get("content", [])
            msg = "Tool call failed"
            if content and isinstance(content, list) and content[0].get("text"):
                msg = content[0]["text"]
            raise MCPToolError(msg, code=-32000)

        content = result.get("content", [])
        if content and isinstance(content, list) and len(content) == 1:
            item = content[0]
            if item.get("type") == "text":
                text = item["text"]
                try:
                    return json.loads(text)
                except (json.JSONDecodeError, TypeError):
                    return text
        return content


class MCPGatewayConnectionError(Exception):
    """Raised when the gateway HTTP endpoint is unreachable."""

    pass


class MCPToolError(Exception):
    """Raised when a tool call fails at the MCP protocol level.

    Attributes:
        code: MCP error code (-1 for generic, HTTP status for transport errors).
    """

    def __init__(self, message: str, code: int = -1):
        super().__init__(message)
        self.code = code 
