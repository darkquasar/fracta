---
title: Adding a New Runtime to Fracta
description: How to onboard a new LLM CLI as a fracta host runtime
---

## Overview

Fracta supports multiple LLM runtime implementations. A runtime is an adapter between fracta's
orchestrator and a specific LLM CLI or API. Runtimes declare their capabilities and fracta
routes accordingly. Claude parity is not required.

For configuring existing runtimes (Claude, Codex, OpenCode), see [runtime-configuration.md](/guides/authentication/runtime-configuration).

## Capability Tiers

| Tier | Required Capabilities | Optional | Examples |
|------|----------------------|----------|----------|
| **Batch** | `BuildBatchCommand`, `ParseBatchOutput` | Nothing else | CLI wrappers, stateless API adapters |
| **Interactive** | Batch + `Stream` | `ResumeToken` | Hosts with interactive sessions |
| **Full-Agent** | Batch + `Stream` + `ResumeToken` + `AgentMCP` | `ToolPermissions` | Claude, future full-featured hosts |

A Batch-only host is production-valid. The system degrades gracefully for missing capabilities.

## Steps

### 1. Implement `host.Host`

Create `internal/host/<name>/` with a struct implementing `host.Host`:

```go
type Host struct{}

func (Host) WriteWorkspace(workdir string, allowedTools []string, cfg host.WorkspaceConfig) error {
    // Write host-specific config files into the workspace.
    // For a Batch host, this can be a no-op (return nil).
    return nil
}

func (Host) Bootstrap(task, baseBranch, contract string) host.BootstrapResult {
    // Return the task file name and initial prompt.
    // FileName can be "" if the host doesn't use file-based contracts.
    return host.BootstrapResult{
        FileName:      "",                    // or "TASK.md", etc.
        InitialPrompt: "Execute: " + task,
    }
}

func (Host) BuildBatchCommand(prompt, model, resumeToken string) host.CommandSpec {
    return host.CommandSpec{
        Command: "your-cli",
        Args:    []string{"--prompt", prompt},
    }
}

func (Host) ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
    // Parse your CLI's output into a host.Result.
    return host.Result{Output: string(stdout)}, nil
}

func (Host) StartStream(workdir, model, logPath string) (host.StreamSession, error) {
    // Return ErrStreamNotSupported for Batch-only hosts.
    return nil, host.ErrStreamNotSupported
}

func (Host) Capabilities() host.Capabilities {
    return host.Capabilities{
        Stream:          false,
        AgentMCP:        false,
        ToolPermissions: false,
        ResumeToken:     false,
    }
}
```

### 2. Register the runtime

In `cmd/helpers.go` `buildRuntimeRegistry()`:

```go
func buildRuntimeRegistry() *host.MapRegistry {
    reg := host.NewMapRegistry("claude") // default runtime
    reg.Register("claude", claude.Host{})
    reg.Register("myruntime", myruntime.Host{})
    return reg
}
```

### 3. Add runtime config to `fracta.yaml`

```yaml
agents:
  agent_runtimes:
    myruntime:
      adapter: myruntime
      model: my-default-model
      model_tiers:
        heavy: my-large-model
        medium: my-default-model
        light: my-small-model
      env:                        # local execution env
        MY_API_KEY: "${MY_API_KEY}"
      kubernetes:                 # K8s execution env
        env:
          - name: MY_API_KEY
            secret_ref:
              name: myruntime-secret
              key: api-key
```

### 4. Spawn with the runtime

```bash
fracta spawn --task my-task --runtime myruntime
```

Or via MCP:
```json
{"task": "my-task", "runtime": "myruntime"}
```

## What  fracta handles automatically

- Host resolution from `host_type` parameter or config default
- Model resolution from host-specific `model_tiers`
- Capability enforcement (stream/resume/AgentMCP checked before side effects)
- Host env injection for local and K8s backends
- Workspace creation and cleanup

## What the host adapter handles

- CLI command construction (`BuildBatchCommand`)
- Output parsing (`ParseBatchOutput`)
- Workspace file artifacts (`WriteWorkspace` — can be no-op)
- Streaming protocol (`StartStream` — can return `ErrStreamNotSupported`)
- Bootstrap file/prompt (`Bootstrap` — `FileName` can be empty)

## Incremental shipping

A host can ship in stages:
1. Batch-only (Tier 1) — implement `BuildBatchCommand` + `ParseBatchOutput`
2. Add streaming (Tier 2) — implement `StartStream`
3. Add resume + agent-mode (Tier 3) — add `ResumeToken` + `AgentMCP` capabilities

Each stage is independently production-valid.
