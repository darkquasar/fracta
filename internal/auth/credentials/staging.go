// Package credentials implements the credential pipeline for agent authentication.
// It handles staging (cross-boundary transport), rehydration, and source management.
package credentials

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/google/uuid"
)

// CredentialStager stores and retrieves credential blobs for cross-boundary transport.
// Each source produces a single credential blob. The K8s Secret wrapping happens
// inside the K8s stager implementation, not at the interface boundary.
type CredentialStager interface {
	// Stage stores a single credential blob for a named source.
	// Returns an opaque ref for later retrieval.
	Stage(ctx context.Context, sourceName string, data []byte, mountPath string, ttl time.Duration) (ref string, err error)

	// Fetch retrieves a previously staged credential blob by ref.
	Fetch(ctx context.Context, ref string) (*StagedCredential, error)

	// Cleanup removes a staged credential.
	Cleanup(ctx context.Context, ref string) error
}

// StagedCredential is a credential blob retrieved from staging.
type StagedCredential struct {
	SourceName string // the source this was staged for
	Data       []byte // single credential blob
	MountPath  string // pod-side mount path
}

// inMemoryEntry holds a staged credential with expiry metadata.
type inMemoryEntry struct {
	cred      StagedCredential
	expiresAt time.Time
}

// InMemoryCredentialStager is a CredentialStager backed by an in-process map.
// Used in tests and local-mode operation where no K8s Secrets are available.
type InMemoryCredentialStager struct {
	mu      sync.Mutex
	entries map[string]*inMemoryEntry
}

// NewInMemoryCredentialStager creates a new in-memory stager.
func NewInMemoryCredentialStager() *InMemoryCredentialStager {
	return &InMemoryCredentialStager{
		entries: make(map[string]*inMemoryEntry),
	}
}

func (s *InMemoryCredentialStager) Stage(ctx context.Context, sourceName string, data []byte, mountPath string, ttl time.Duration) (string, error) {
	log := fractalog.Component("credentials")

	if len(data) == 0 {
		return "", fmt.Errorf("credentials: stage %q: empty data", sourceName)
	}

	ref := fmt.Sprintf("fracta-cred-staged-%s", uuid.New().String()[:8])

	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[ref] = &inMemoryEntry{
		cred: StagedCredential{
			SourceName: sourceName,
			Data:       append([]byte(nil), data...), // defensive copy
			MountPath:  mountPath,
		},
		expiresAt: time.Now().Add(ttl),
	}

	log.Info("credentials.stage.success",
		"source_name", sourceName,
		"staged_ref", ref,
		"ttl_seconds", int(ttl.Seconds()),
		"bytes_count", len(data),
	)

	return ref, nil
}

func (s *InMemoryCredentialStager) Fetch(ctx context.Context, ref string) (*StagedCredential, error) {
	log := fractalog.Component("credentials")

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[ref]
	if !ok {
		log.Warn("credentials.stage.fetch_miss", "staged_ref", ref)
		return nil, fmt.Errorf("credentials: staged ref %q not found", ref)
	}
	if time.Now().After(entry.expiresAt) {
		delete(s.entries, ref)
		log.Warn("credentials.stage.fetch_expired", "staged_ref", ref, "source_name", entry.cred.SourceName)
		return nil, fmt.Errorf("credentials: staged ref %q expired", ref)
	}

	log.Debug("credentials.stage.fetch_ok", "staged_ref", ref, "source_name", entry.cred.SourceName)

	// Return a copy so callers can't mutate internal state.
	result := &StagedCredential{
		SourceName: entry.cred.SourceName,
		Data:       append([]byte(nil), entry.cred.Data...),
		MountPath:  entry.cred.MountPath,
	}
	return result, nil
}

func (s *InMemoryCredentialStager) Cleanup(ctx context.Context, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entries[ref]; !ok {
		return fmt.Errorf("credentials: staged ref %q not found", ref)
	}
	delete(s.entries, ref)
	return nil
}

// Len returns the number of entries (for testing).
func (s *InMemoryCredentialStager) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
