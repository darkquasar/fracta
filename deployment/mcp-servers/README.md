# Fracta MCP Server Catalog

This directory is fracta's catalog for MCP servers that can be wired into local-process, Docker Compose, or Kubernetes deployments.

Each MCP has its own subdirectory:

- `server.yaml` is the structured source of truth for auth, transports, launch modes, Docker ownership, support status, and smoke-test expectations.
- `README.md` explains the operational setup for a human.
- `Dockerfile` lives beside the server metadata only when fracta owns the image.

`catalog.yaml` is the top-level index of all cataloged MCPs.

## Status

| Status | Meaning |
|---|---|
| `tested` | Connected and listed tools through fracta in at least one supported mode. |
| `documented` | Official or well-documented upstream exists, but fracta smoke test is still pending. |
| `candidate` | Community or mode-specific entry that needs fracta smoke testing before promotion. |

## Current Catalog

| MCP server key | Category | Status | Local-process mode | Docker Compose | Kubernetes | Auth shape | Notes |
|---|---|---|---|---|---|---|---|
| `fracta` | first-party | `tested` | fracta binary over stdio | `fracta/agent:latest` | `fracta/agent:latest` | fracta gateway token | First-party platform MCP surface. |
| `elastic` | security | `tested` | `podman run ... docker.elastic.co/mcp/elasticsearch stdio` | public image | public image | env token | External vendor image. |
| `vendor` | security | `tested` | `uvx` from VendorSecurity GitHub repo | repo-built image | repo-built image | env token | Image lives in `vendor/Dockerfile`. |
| `readwise` | knowledge | `documented` | `mcp-remote` proxy | blocked/pending | blocked/pending | OAuth | Official hosted MCP; native fracta path needs gateway OAuth. |
| `notion` | knowledge | `documented` | `mcp-remote` proxy | blocked/pending | blocked/pending | OAuth | Official hosted MCP; native fracta path needs gateway OAuth. |
| `obsidian` | knowledge | `candidate` | local `npx obsidian-mcp-server` | not recommended | not supported | local API token | Requires Obsidian desktop and Local REST API plugin. |
| `raindrop` | knowledge | `documented` | remote bearer or `mcp-remote` | bearer token | bearer token | OAuth or bearer token | Official hosted MCP; usable before OAuth with API token. |
| `zotero` | knowledge | `candidate` | local `uvx zotero-mcp` | possible | possible | local library or API token | Several community servers exist; preferred one needs smoke test. |
| `google-drive` | knowledge | `candidate` | local reference server | blocked/pending | blocked/pending | OAuth | Needs Google OAuth credentials/token handling. |
| `logseq` | knowledge | `candidate` | local `npx logseq-mcp-tools` | not recommended | not supported | local API token | Desktop-local graph access. |
| `joplin` | knowledge | `candidate` | local `npx joplin-mcp-server` | possible | possible | token | Needs token and sync/backend verification. |
| `apple-notes` | knowledge | `candidate` | macOS local MCP | not supported | not supported | macOS permissions | Local Apple Notes database/app dependency. |
| `noteplan` | knowledge | `documented` | local `npx @noteplanco/noteplan-mcp` | not supported | not supported | local app permissions | Official local NotePlan MCP. |
| `tana` | knowledge | `documented` | local HTTP API or `npx tana-mcp` | not recommended | possible with token mode | local app or token | Official local API plus Input API option. |

## Knowledge Garden Bundle

For personal knowledge management, prioritize this order:

1. `readwise` for highlights, Reader documents, and reading history.
2. `obsidian` for local markdown vaults and backlinks.
3. `notion` for mainstream structured workspaces.
4. `raindrop` for bookmarks, tags, highlights, and saved page content.
5. `zotero` for papers, citations, PDFs, and annotations.
6. `google-drive` for shared docs and broad document corpora.
7. `logseq`, `joplin`, `apple-notes`, `noteplan`, and `tana` for user-specific note systems.

## Local K8s Image Loading

The Makefile does not prompt with an interactive menu. Use `K8S_IMAGE_LOADER` to select the local cluster image loader:

```bash
make docker-load vendor-mcp-load
K8S_IMAGE_LOADER=kind KIND_CLUSTER=<name> make docker-load vendor-mcp-load
K8S_IMAGE_LOADER=minikube MINIKUBE_PROFILE=<profile> make docker-load vendor-mcp-load
K8S_IMAGE_LOADER=k3d K3D_CLUSTER=<name> make docker-load vendor-mcp-load
```

The default is `K8S_IMAGE_LOADER=docker-desktop`. Only repo-built local images need these load targets. Public images, such as Elastic MCP, are pulled by the cluster.

## Adding Or Changing An MCP Server

1. Create or update `deployment/mcp-servers/<server-key>/server.yaml`.
2. Create or update `deployment/mcp-servers/<server-key>/README.md`.
3. Put any repo-owned Dockerfile in that same subdirectory.
4. Add the entry to `deployment/mcp-servers/catalog.yaml`.
5. Update the matching mode config under `deployment/<mode>/` if fracta should enable it by default.
6. Update the in-cluster manifest under `deployment/k8s-local-cluster/manifests/` if the server runs as a pod.
7. Add or update Makefile targets if the image must be built or loaded into local K8s.

Use `status: documented` or `status: candidate` until fracta has a repeatable smoke test for the server. Promote to `tested` only after the gateway can connect and list tools.
