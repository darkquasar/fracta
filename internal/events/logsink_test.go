package events

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestLogSink_FormatsEvent(t *testing.T) {
	// Capture log output.
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(oldDefault)

	sink := NewLogSink()
	e := Info("orchestrator", "create")
	e.Category = "agent"
	e.Resource = "task:research-foo"
	e.Task = "research-foo"
	e.Detail = "host=claude model=haiku"
	e.Attrs = map[string]string{"model": "haiku"}

	if err := sink.Handle(context.Background(), e); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	// Parse the JSON log line.
	var logLine map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logLine); err != nil {
		t.Fatalf("unmarshal log: %v (raw: %s)", err, buf.String())
	}

	checks := map[string]string{
		"component": "events_bus",
		"source":    "orchestrator",
		"action":    "create",
		"category":  "agent",
		"resource":  "task:research-foo",
		"task":      "research-foo",
		"severity":  "info",
		"detail":    "host=claude model=haiku",
	}
	for key, want := range checks {
		got, _ := logLine[key].(string)
		if got != want {
			t.Errorf("log[%q] = %q, want %q", key, got, want)
		}
	}

	// Attrs should be present as a map.
	attrs, ok := logLine["attrs"]
	if !ok {
		t.Error("log should contain 'attrs' key")
	} else {
		am, _ := attrs.(map[string]any)
		if am["model"] != "haiku" {
			t.Errorf("attrs.model = %v, want haiku", am["model"])
		}
	}
}
