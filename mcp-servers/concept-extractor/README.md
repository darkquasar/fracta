# concept-extractor

Fracta catalog entry for the `concept-extractor` MCP server. Source, Dockerfile, and CI live in the peer repo:

https://github.com/darkquasar/fracta-mcp-servers (`servers/concept-extractor/`)

This directory only carries the catalog metadata fracta needs to wire the server into Docker Compose and Kubernetes deployments — fracta consumes the published GHCR image; it does **not** build it.

## Tools

| Tool | What it does |
|---|---|
| `keybert_extract_tool` | KeyBERT keyphrase extraction (MMR-diverse, sentence-transformer scoring). |
| `gliner_extract_tool` | Zero-shot NER via GLiNER. Caller passes label taxonomy per call. |
| `spacy_extract_tool` | spaCy built-in NER plus noun chunks. |

## Intended use

The knowledge-garden strategy family (spec-41) uses these as the "smart classifier" alternative to the v1 n-gram heuristic. A strategy can invoke GLiNER (`labels: ["Concept", "Person", "Work", "Theory", ...]`), then KeyBERT for keyphrase coverage, then combine with YAKE/RAKE results computed in-process and score before deciding which `Concept` nodes to MERGE.

## Image

- Registry: `ghcr.io/darkquasar/fracta-mcp-servers/concept-extractor`
- Tags: `:latest`, `:sha-<short>`, `:main-<run_number>`, plus `:vX.Y.Z`/`:X.Y`/`:X` on semver tags.
- Architectures: `linux/amd64`, `linux/arm64`.

## Smoke test

Once the gateway is up:

```bash
fracta mcp list-tools concept-extractor
# expects: keybert_extract_tool, gliner_extract_tool, spacy_extract_tool
```

Promote status from `candidate` to `tested` in `server.yaml` once verified.
