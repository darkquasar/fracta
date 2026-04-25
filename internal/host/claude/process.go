package claude

import (
	"encoding/json"
	"fmt"

	"github.com/darkquasar/fracta/internal/host"
)

// BuildBatchCommand returns the CommandSpec for running Claude CLI in batch mode.
// This is the Claude implementation of host.Host.BuildBatchCommand.
func BuildBatchCommand(prompt, mdl, resumeToken string) host.CommandSpec {
	var args []string

	if resumeToken != "" {
		args = append(args, "-r", resumeToken)
	}

	args = append(args, "-p", prompt)
	args = append(args, "--output-format", "json")
	args = append(args, "--permission-mode", "dontAsk")

	if mdl != "" {
		args = append(args, "--model", mdl)
	}

	return host.CommandSpec{
		Command: "claude",
		Args:    args,
	}
}

// ParseBatchOutput parses the stdout of a completed Claude CLI batch process.
// This is the Claude implementation of host.Host.ParseBatchOutput.
func ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	if waitErr != nil {
		if len(stdout) > 0 {
			var resp Response
			if jsonErr := json.Unmarshal(stdout, &resp); jsonErr == nil {
				return host.Result{
					ResumeToken: resp.SessionID,
					Output:      resp.Result,
					IsError:     true,
				}, nil
			}
		}
		return host.Result{}, fmt.Errorf("running claude: %w", waitErr)
	}

	var resp Response
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return host.Result{}, err
	}

	return host.Result{
		ResumeToken: resp.SessionID,
		Output:      resp.Result,
		IsError:     resp.IsError,
	}, nil
}

// BuildStreamArgs constructs the argument list for Claude CLI streaming mode.
func BuildStreamArgs(mdl string) []string {
	args := []string{
		"--print",
		"--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--permission-mode", "dontAsk",
	}
	if mdl != "" {
		args = append(args, "--model", mdl)
	}
	return args
}
