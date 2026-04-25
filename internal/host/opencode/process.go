package opencode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darkquasar/fracta/internal/host"
)

// BuildBatchCommand returns the CommandSpec for running OpenCode CLI in batch mode.
// Initial run: opencode run --format json "<prompt>"
// Resume:      opencode run --format json --session <id> "<prompt>"
//
// Does NOT include --dangerously-skip-permissions by default — the permission
// policy written by WriteWorkspace (task:deny) must remain effective to mitigate
// subagent overuse (spec §9 Risk 2).
func BuildBatchCommand(prompt, model, resumeToken string) host.CommandSpec {
	args := []string{"run", "--format", "json"}

	if model != "" {
		args = append(args, "--model", model)
	}
	if resumeToken != "" {
		args = append(args, "--session", resumeToken)
	}

	args = append(args, prompt)

	return host.CommandSpec{
		Command: "opencode",
		Args:    args,
	}
}

// ParseBatchOutput parses nd-JSON output from `opencode run --format json`.
// Extracts: sessionID (ResumeToken), text output (Output), error status.
func ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	var sessionID string
	var textParts []string
	var hasError bool
	var errorMsg string

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	// Increase buffer to 1 MiB to handle large outputs.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event ndEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue // skip non-JSON lines
		}

		// Extract sessionID from the first event that contains it.
		if event.SessionID != "" && sessionID == "" {
			sessionID = event.SessionID
		}

		switch event.Type {
		case "text":
			var part ndPart
			if err := json.Unmarshal(event.Part, &part); err == nil && part.Text != "" {
				textParts = append(textParts, part.Text)
			}

		case "error":
			hasError = true
			if event.Error != nil {
				errorMsg = event.Error.Data.Message
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return host.Result{}, fmt.Errorf("scanning opencode nd-JSON output: %w", err)
	}

	output := strings.Join(textParts, "\n")

	// If process failed and we got no output, return error.
	if waitErr != nil && sessionID == "" && output == "" {
		return host.Result{}, fmt.Errorf("running opencode: %w", waitErr)
	}

	// If process failed but we have partial output, return it as error result.
	if waitErr != nil {
		return host.Result{
			ResumeToken: sessionID,
			Output:      output,
			IsError:     true,
		}, nil
	}

	// If we saw an error event, return the output with error flag.
	if hasError {
		if output == "" && errorMsg != "" {
			output = errorMsg
		}
		return host.Result{
			ResumeToken: sessionID,
			Output:      output,
			IsError:     true,
		}, nil
	}

	// Successful result — sessionID may be empty for very short sessions.
	return host.Result{
		ResumeToken: sessionID,
		Output:      output,
		IsError:     false,
	}, nil
}
