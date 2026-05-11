package mcpcatalog

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// ImageState describes whether a container image is present locally.
type ImageState int

const (
	// ImageStateUnknown is returned on timeout, missing CLI, or any
	// non-classifiable error. Never fatal — callers degrade gracefully.
	ImageStateUnknown ImageState = iota
	// ImageStateAbsent — the CLI reported the image was not found.
	ImageStateAbsent
	// ImageStatePresent — the CLI reported the image is available locally.
	ImageStatePresent
)

func (s ImageState) String() string {
	switch s {
	case ImageStatePresent:
		return "present"
	case ImageStateAbsent:
		return "absent"
	default:
		return "unknown"
	}
}

// ImageInspector reports whether a container image is present in the local
// engine cache. Implementations: docker, podman, noop.
type ImageInspector interface {
	HasImage(ctx context.Context, ref string) (ImageState, error)
	Name() string
}

// defaultInspectTimeout caps each HasImage call. 5s mirrors the spec §9
// requirement.
const defaultInspectTimeout = 5 * time.Second

// cliInspector shells out to docker or podman.
type cliInspector struct {
	bin     string
	timeout time.Duration
}

// NewDockerCLIInspector returns an inspector that runs `docker image inspect`.
func NewDockerCLIInspector() ImageInspector {
	return &cliInspector{bin: "docker", timeout: defaultInspectTimeout}
}

// NewPodmanCLIInspector returns an inspector that runs `podman image inspect`.
func NewPodmanCLIInspector() ImageInspector {
	return &cliInspector{bin: "podman", timeout: defaultInspectTimeout}
}

// NewNoopInspector returns an inspector that always reports unknown.
func NewNoopInspector() ImageInspector { return noopInspector{} }

// DetectImageInspector picks docker → podman → noop based on PATH lookup.
// Per spec §9, image-state inspection is best-effort; never panics.
func DetectImageInspector() ImageInspector {
	if _, err := exec.LookPath("docker"); err == nil {
		return NewDockerCLIInspector()
	}
	if _, err := exec.LookPath("podman"); err == nil {
		return NewPodmanCLIInspector()
	}
	return NewNoopInspector()
}

// HasImage runs `<bin> image inspect <ref>` with a deadline.
//
// Classification is exit-code-only (spec §9): localised stderr text (LANG,
// version drift) makes substring matching fragile.
//
//   exit 0                                  → ImageStatePresent
//   exit 1 with empty stdout                → ImageStateAbsent
//   ctx.Err() == context.DeadlineExceeded   → ImageStateUnknown (timeout)
//   *exec.Error (binary not on PATH)        → ImageStateUnknown
//   any other exit code                     → ImageStateUnknown
func (c *cliInspector) HasImage(ctx context.Context, ref string) (ImageState, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, c.bin, "image", "inspect", ref)
	out, err := cmd.Output()

	// Timeout — deadline exceeded.
	if ctx.Err() == context.DeadlineExceeded {
		return ImageStateUnknown, ctx.Err()
	}

	if err == nil {
		return ImageStatePresent, nil
	}

	// Binary not on PATH (exec.Error wraps "not found").
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return ImageStateUnknown, err
	}

	// Classify by exit code.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == 1 && len(out) == 0 {
			return ImageStateAbsent, nil
		}
		return ImageStateUnknown, err
	}

	return ImageStateUnknown, err
}

func (c *cliInspector) Name() string { return c.bin }

type noopInspector struct{}

func (noopInspector) HasImage(ctx context.Context, ref string) (ImageState, error) {
	return ImageStateUnknown, nil
}

func (noopInspector) Name() string { return "noop" }
