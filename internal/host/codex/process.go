package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/darkquasar/fracta/internal/host"
)

// BuildBatchCommand returns the CommandSpec for running Codex CLI in batch mode.
// Initial run: codex exec --json --full-auto --ephemeral --skip-git-repo-check "<prompt>"
// Resume:      codex exec resume <threadID> --json --full-auto "<prompt>"
//
// Ephemeral flags are added on initial runs (no resumeToken) so that K8s pods
// and fresh workspaces work without requiring a git repo or persistent state.
// The -c flag for MCP injection in untrusted K8s projects is handled at the
// orchestrator/worker layer — callers append -c flags to CommandSpec.Args.
func BuildBatchCommand(prompt, model, resumeToken string) host.CommandSpec {
	var args []string

	if resumeToken != "" {
		args = append(args, "exec", "resume", resumeToken)
	} else {
		args = append(args, "exec")
	}

	args = append(args, "--json", "--full-auto")

	// Add ephemeral flags for initial runs (K8s pods, fresh workspaces).
	// Omitted on resume — the thread already has established context.
	if resumeToken == "" {
		args = append(args, "--ephemeral", "--skip-git-repo-check")
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	args = append(args, prompt)

	return host.CommandSpec{
		Command: "codex",
		Args:    args,
	}
}

// ParseBatchOutput parses JSONL output from `codex exec --json`.
// Extracts: thread_id (ResumeToken), last agent message (Output), error status.
//
// Handles all stable Codex event types:
//   - thread.started: captures thread_id for resume
//   - turn.started: marks turn in progress
//   - item.started: long-running item begin (informational, logged for observability)
//   - item.completed: captures agent_message text and command_execution output
//   - turn.completed: marks successful completion with usage stats
//   - error: captures error details
func ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	var threadID string
	var lastMessage string
	var turnCompleted bool
	var eventCount int
	var lastError string

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	// Increase buffer to 1 MiB to handle large command outputs or long agent messages.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			continue // skip non-JSON lines
		}

		eventCount++

		switch event.Type {
		case "thread.started":
			threadID = event.ThreadID

		case "item.started":
			// Informational: long-running item begin. No action needed for parsing,
			// but counted in eventCount for empty-output detection.

		case "item.completed":
			if event.Item != nil && event.Item.Type == "agent_message" && event.Item.Text != "" {
				lastMessage = event.Item.Text
			}

		case "turn.completed":
			turnCompleted = true

		case "error":
			if event.Error != nil {
				lastError = event.Error.Message
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return host.Result{}, fmt.Errorf("scanning codex JSONL output: %w", err)
	}

	// If process failed and we got no output, return error.
	if waitErr != nil && threadID == "" && lastMessage == "" {
		return host.Result{}, fmt.Errorf("running codex: %w", waitErr)
	}

	// If process failed but we have partial output, return it as error result.
	if waitErr != nil {
		output := lastMessage
		if output == "" && lastError != "" {
			output = lastError
		}
		return host.Result{
			ResumeToken: threadID,
			Output:      output,
			IsError:     true,
		}, nil
	}

	// If we got events but no turn.completed, something went wrong.
	if eventCount > 0 && !turnCompleted {
		errMsg := "codex exec did not complete (no turn.completed event)"
		if lastError != "" {
			errMsg = fmt.Sprintf("codex exec error: %s", lastError)
		}
		return host.Result{}, fmt.Errorf("%s", errMsg)
	}

	// No events at all — empty output.
	if eventCount == 0 {
		return host.Result{}, fmt.Errorf("codex exec produced no output")
	}

	// Require a thread_id for a successful result.
	// Without thread_id, the agent cannot be resumed — the main value of this host.
	if threadID == "" {
		return host.Result{}, fmt.Errorf("codex exec produced no thread_id (cannot resume)")
	}

	return host.Result{
		ResumeToken: threadID,
		Output:      lastMessage,
		IsError:     false,
	}, nil
}
