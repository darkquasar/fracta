# Minimal streamable-HTTP MCP test server.
# Build from the repo root so the build context includes only this dir:
#   make mcp-build/fracta-fracta-test-server
FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    MCP_HOST=0.0.0.0 \
    MCP_PORT=8000 \
    FASTMCP_HOST=0.0.0.0 \
    FASTMCP_PORT=8000

WORKDIR /app

COPY pyproject.toml ./
RUN pip install --no-cache-dir "mcp[cli]>=1.2.0" "uvicorn>=0.30"

COPY server.py ./

EXPOSE 8000

CMD ["python", "server.py"]
