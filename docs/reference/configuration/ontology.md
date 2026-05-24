---
title: Ontology Schema Configuration
description: Configure which graph schema families the gateway loads at startup via the ontology.schemas URI block.
---

The gateway loads its knowledge-graph schema families from `ontology.schemas:` in the gateway config. Each entry is a URI; the resolver layer maps schemes to backing filesystems.

```yaml
ontology:
  schemas:
    - uri: embed://graph-schema/core
    - uri: embed://graph-schema/threat-hunting
    - uri: embed://graph-schema/fracta-mcp-gateway
    - uri: embed://graph-schema/knowledge-garden
```

Schemas load once at gateway startup. There is no auto-discovery: every family the gateway should know about must appear in this block.

## Supported URI schemes

### `embed://graph-schema/<family>`

Schemas baked into the fracta binary at compile time. This is the default for both docker-compose and Kubernetes deployments — no volume mount, no ConfigMap, no Dockerfile change needed when a new family is added. The four families that ship with fracta are:

- `embed://graph-schema/core`
- `embed://graph-schema/threat-hunting`
- `embed://graph-schema/fracta-mcp-gateway`
- `embed://graph-schema/knowledge-garden`

To add a new family to the binary, drop its directory under `internal/schema/embedfs/graph-schema/<name>/` and rebuild. The embed resolver picks it up automatically.

### `file:///abs/path/to/<family>`

Local filesystem. Used for operator overrides — a custom version of a shipped family, or a family that isn't baked in.

```yaml
ontology:
  schemas:
    - uri: embed://graph-schema/core              # use shipped core
    - uri: file:///etc/fracta/custom/research     # ship a custom family from disk
```

In Kubernetes, mount the custom schema as a ConfigMap or PVC at the configured path:

```yaml
volumes:
  - name: custom-schema
    configMap:
      name: fracta-schema-research
volumeMounts:
  - name: custom-schema
    mountPath: /etc/fracta/custom/research
    readOnly: true
```

Relative `file://./relative/path` works in dev environments and tests; absolute `file:///abs/path` is recommended for production.

## CLI override: `--schema-dir`

```bash
fracta serve --schema-dir embed://graph-schema/core
fracta serve --schema-dir file:///etc/fracta/graph-schema/core
fracta serve --schema-dir /etc/fracta/graph-schema/core   # bare path; auto-wrapped as file://, logs a deprecation warning
```

The bare-path form is honored for one release with a deprecation log and is removed in v0.7. Migrate to the explicit URI form when convenient.

## Migration from `path:` (pre-v0.6)

Earlier configs used `ontology.schemas[].path`. That field was removed in v0.6:

```yaml
# Before (v0.5 and earlier)
ontology:
  schemas:
    - path: graph-schema/core
    - path: graph-schema/threat-hunting

# After (v0.6+)
ontology:
  schemas:
    - uri: embed://graph-schema/core
    - uri: embed://graph-schema/threat-hunting
```

If your config still has `path:` entries, the gateway fails fast at startup with a one-line message naming the new field. There is no auto-migration — the URI shape forces an honest decision between embedded defaults and an explicit on-disk override.

## Future schemes

The resolver registry is open. Planned additions:

- `s3://bucket/prefix/<family>` — schemas in object storage, no image rebuild for schema updates.
- `https://example.com/schemas/<family>.tar.gz` — pull tarballs from a publishing endpoint.
- `configmap://<name>/<key>` — a native k8s ConfigMap reader (the `file://` mount path covers most needs today).

These will appear as single-file additions under `internal/schema/resolve/` without changing the config UX or the loader. Track them in the GitHub issue queue.
