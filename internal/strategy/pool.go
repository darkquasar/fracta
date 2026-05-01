package strategy

import (
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
)

// SidecarPool manages N sidecar subprocesses and delegates Runner methods
// via round-robin with health awareness. Create methods broadcast to all
// sidecars so each runner discovers the new strategy.
type SidecarPool struct {
	sidecars []*Sidecar
	next     atomic.Uint64
}

// Compile-time assertion: *SidecarPool satisfies Runner.
var _ Runner = (*SidecarPool)(nil)

// NewSidecarPool creates n Sidecar instances, each with its own Unix socket.
// Socket paths are /tmp/fracta-strategy-{i}.sock. All sidecars share the same
// options (graph addr, strategy dir, etc.).
func NewSidecarPool(n int, pythonBin, runnerPath, strategyDir string, opts ...SidecarOption) (*SidecarPool, error) {
	if n < 1 {
		n = 1
	}
	pool := &SidecarPool{
		sidecars: make([]*Sidecar, 0, n),
	}
	for i := 0; i < n; i++ {
		sockPath := fmt.Sprintf("/tmp/fracta-strategy-%d.sock", i)
		perSidecarOpts := append(slices.Clone(opts), WithSocketPath(sockPath))
		sc, err := NewSidecar(pythonBin, runnerPath, strategyDir, perSidecarOpts...)
		if err != nil {
			// Clean up already-started sidecars on failure.
			pool.Close()
			return nil, fmt.Errorf("sidecar %d: %w", i, err)
		}
		pool.sidecars = append(pool.sidecars, sc)
	}
	return pool, nil
}

// pick returns the next healthy sidecar via round-robin with health awareness.
// If all sidecars are unhealthy, picks the one with the oldest lastFailure
// (most likely to have self-healed via restart).
func (p *SidecarPool) pick() *Sidecar {
	n := uint64(len(p.sidecars))
	start := p.next.Add(1) - 1

	// Try up to N sidecars starting from the round-robin position.
	for i := uint64(0); i < n; i++ {
		sc := p.sidecars[(start+i)%n]
		if sc.Healthy() {
			return sc
		}
	}

	// All unhealthy — pick the one with the oldest lastFailure.
	var oldest *Sidecar
	for _, sc := range p.sidecars {
		if oldest == nil || sc.LastFailure().Before(oldest.LastFailure()) {
			oldest = sc
		}
	}
	return oldest
}

// broadcast runs fn against every sidecar. For sidecars that fail with a
// transport error, it attempts restart + retry. Returns the first
// non-recoverable error.
func (p *SidecarPool) broadcast(fn func(*Sidecar) error) error {
	for _, sc := range p.sidecars {
		if !sc.Healthy() {
			// Try to restart unhealthy sidecars before broadcasting.
			if err := sc.restart(); err != nil {
				continue // skip this sidecar, don't fail the whole broadcast
			}
		}
		if err := fn(sc); err != nil {
			var transportErr *SidecarTransportError
			var restartedErr *SidecarRestartedError
			if errors.As(err, &transportErr) {
				// Transport error — restart and retry once.
				if restartErr := sc.restart(); restartErr != nil {
					return err // restart failed, propagate original error
				}
				if retryErr := fn(sc); retryErr != nil {
					return retryErr // retry also failed
				}
				continue // retry succeeded
			}
			if errors.As(err, &restartedErr) {
				// Sidecar already restarted (from withRetry) but didn't retry
				// the write op. Safe to retry now that the sidecar is healthy.
				if retryErr := fn(sc); retryErr != nil {
					return retryErr
				}
				continue
			}
			return err // non-transport error
		}
	}
	return nil
}

func (p *SidecarPool) List(tags ...string) ([]StrategyInfo, error) {
	return p.pick().List(tags...)
}

func (p *SidecarPool) Describe(name string) (*StrategyInfo, error) {
	return p.pick().Describe(name)
}

func (p *SidecarPool) Run(name string, params map[string]any, manifest StagingManifest) (*RunResult, error) {
	return p.pick().Run(name, params, manifest)
}

func (p *SidecarPool) Create(name, code, metadata string, force bool) error {
	return p.broadcast(func(sc *Sidecar) error {
		return sc.Create(name, code, metadata, force)
	})
}

func (p *SidecarPool) CreateWithContract(name, code, contractYAML string, force bool) error {
	return p.broadcast(func(sc *Sidecar) error {
		return sc.CreateWithContract(name, code, contractYAML, force)
	})
}

func (p *SidecarPool) StagingDir() string {
	return p.sidecars[0].StagingDir()
}

func (p *SidecarPool) StrategyDir() string {
	return p.sidecars[0].StrategyDir()
}

func (p *SidecarPool) Close() error {
	var firstErr error
	for _, sc := range p.sidecars {
		if err := sc.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
