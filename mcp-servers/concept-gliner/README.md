# concept-gliner

Fracta catalog entry for the `concept-gliner` MCP server. Source, Dockerfile, and CI live in the peer repo:

https://github.com/darkquasar/fracta-mcp-servers (`servers/concept-gliner/`)

This directory only carries the catalog metadata fracta needs to wire the server into Docker Compose and Kubernetes deployments — fracta consumes the published GHCR image; it does **not** build it.

One of three successors to the retired `concept-extractor` monolith. Splitting the per-library servers lets operators deploy only what they actually use; smaller, simpler images; independent scaling.

## Tool

| Tool | What it does |
|---|---|
| `gliner_extract_tool` | Zero-shot NER via GLiNER. Caller passes label taxonomy per call (no model fine-tuning needed). |

## Intended use

The knowledge-garden strategy family (spec-41) uses this as one of three concept-extraction backends. GLiNER's zero-shot mode means strategies can pass arbitrary label sets per call (`labels: ["Concept", "Person", "Work", "Theory", ...]`) without retraining.

## Image

- Registry: `ghcr.io/darkquasar/fracta-mcp-servers/concept-gliner`
- Tags: `:latest`, `:sha-<short>`, `:main-<run_number>`, plus `:vX.Y.Z`/`:X.Y`/`:X` on semver tags.
- Architectures: `linux/amd64`, `linux/arm64`.

## Smoke test

Once the gateway is up and the entry is registered:

```bash
fracta debug gateway policy --direct --gateway-url http://localhost:8080 --verbose | grep concept-gliner
```

Expect `+ concept-gliner.gliner_extract_tool` in the visible-tools listing.
