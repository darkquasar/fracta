# Elastic MCP

## What It Is

`elastic` is the Elasticsearch MCP backend. Fracta uses it for Elasticsearch-backed hunting and enrichment workflows.

## Local-Process Mode

Configured in `deployment/local-process/fracta.yaml`:

```yaml
elastic:
  local:
    command: podman
    args: ["run", "-i", "--rm", "-e", "ES_URL", "-e", "ES_API_KEY", "docker.elastic.co/mcp/elasticsearch", "stdio"]
```

There is no repo-owned Dockerfile for this mode. The image is published externally as:

```text
docker.elastic.co/mcp/elasticsearch:latest
```

## In-Cluster Mode

Configured by `deployment/k8s-local-cluster/manifests/elastic-mcp.yaml`.

The pod uses the same external image:

```text
docker.elastic.co/mcp/elasticsearch:latest
```

The container runs the HTTP/SSE form:

```text
http --address 0.0.0.0:8000 --sse
```

The Kubernetes Service exposes it as `http://elastic-mcp.fracta.svc:3000/mcp`.

## Image Handling

There is no local Docker build or load target because fracta does not own this image. The Kubernetes manifest uses `imagePullPolicy: IfNotPresent`, so the local cluster pulls the public image from `docker.elastic.co`.

Secrets are created by `make k8s-secrets`:

- `ES_URL`
- `ES_API_KEY`
