package orchestrator

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/darkquasar/fracta/internal/model"
)

const maxPeekSize = 8192 // 8KB

func (o *Orchestrator) Peek(task string) (string, error) {
	agent, err := o.Store.FindAgent(context.Background(), task)
	if err != nil {
		return "", fmt.Errorf("looking up agent: %w", err)
	}
	if agent == nil {
		return "", fmt.Errorf("agent %q not found", task)
	}

	// Try to tail the log file first
	logFile := filepath.Join(o.Root, model.FractaDir, model.LogsDir, task+".log")
	data, err := tailFile(logFile, maxPeekSize)
	if err == nil && len(data) > 0 {
		return string(data), nil
	}

	// Fall back to last output from state
	if agent.LastOutput != "" {
		return agent.LastOutput, nil
	}

	return "", fmt.Errorf("no output available for agent %q", task)
}

// tailFile reads the last maxBytes from a file, seeking from the end.
func tailFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if fi.Size() > maxBytes {
		if _, err := f.Seek(-maxBytes, io.SeekEnd); err != nil {
			return nil, err
		}
	}

	return io.ReadAll(f)
}
