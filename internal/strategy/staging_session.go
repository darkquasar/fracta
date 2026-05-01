package strategy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// StagingSession tracks Parquet file paths for a single strategy run.
// Each session is isolated by a unique ID to prevent table-name collisions
// between concurrent runs.
type StagingSession struct {
	ID                string
	ParamsFingerprint string // SHA-256 of sorted JSON params, set on creation (S9)
	createdAt         time.Time
	lastUsed          time.Time
	paths             map[string]string // table -> parquet path (or glob for chunked)
	mu                sync.Mutex
}

// ComputeParamsFingerprint computes a deterministic fingerprint of strategy
// parameters by sorting map keys, JSON-encoding, and hashing with SHA-256.
// Returns a 16-char hex string. Used to detect parameter mismatches on
// session re-use (S9).
func ComputeParamsFingerprint(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	// json.Marshal produces deterministic output for maps (sorted keys).
	b, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8]) // 16-char hex
}

// NewStagingSession creates a session with a random 8-char hex ID.
func NewStagingSession() *StagingSession {
	b := make([]byte, 4)
	rand.Read(b)
	return &StagingSession{
		ID:        hex.EncodeToString(b),
		paths:     make(map[string]string),
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}
}

// Put registers a Parquet path for a table, replacing any previous entry.
func (s *StagingSession) Put(table, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths[table] = path
	s.lastUsed = time.Now()
}

// PutChunk registers a chunk file for a table. The session stores a glob
// pattern so all chunks are loaded together at run time.
// Example: staging_dir/sessionID/alerts_chunk_*.parquet
func (s *StagingSession) PutChunk(table, chunkPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(chunkPath)
	glob := filepath.Join(dir, table+"_chunk_*.parquet")
	s.paths[table] = glob
	s.lastUsed = time.Now()
}

// Get returns the path (or glob) for a table, and whether it exists.
func (s *StagingSession) Get(table string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.paths[table]
	return p, ok
}

// All returns a copy of the table->path map.
func (s *StagingSession) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]string, len(s.paths))
	for k, v := range s.paths {
		cp[k] = v
	}
	return cp
}

// Tables returns sorted table names in this session.
func (s *StagingSession) Tables() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.paths))
	for k := range s.paths {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// LastUsed returns the time of the last Put/PutChunk/Get operation.
func (s *StagingSession) LastUsed() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed
}

// CreatedAt returns the session creation time.
func (s *StagingSession) CreatedAt() time.Time {
	return s.createdAt
}

// DefaultSessionTTL is the default time-to-live for abandoned staging sessions.
const DefaultSessionTTL = 30 * time.Minute

// janitorInterval is how often the janitor scans for expired sessions.
const janitorInterval = 5 * time.Minute

// StagingSessionStore manages staging sessions with automatic cleanup.
type StagingSessionStore struct {
	sessions   sync.Map // sessionID -> *StagingSession
	stagingDir string
	ttl        time.Duration
}

// NewStagingSessionStore creates a store that writes session data under stagingDir.
func NewStagingSessionStore(stagingDir string) *StagingSessionStore {
	return &StagingSessionStore{
		stagingDir: stagingDir,
		ttl:        DefaultSessionTTL,
	}
}

// Create allocates a new session, registers it in the store, and creates its
// disk directory under stagingDir/sessionID.
func (ss *StagingSessionStore) Create() *StagingSession {
	s := NewStagingSession()
	ss.sessions.Store(s.ID, s)
	os.MkdirAll(filepath.Join(ss.stagingDir, s.ID), 0o700)
	return s
}

// Get retrieves a session by ID.
func (ss *StagingSessionStore) Get(id string) (*StagingSession, bool) {
	v, ok := ss.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*StagingSession), true
}

// Remove deletes a session from the store and removes its disk directory.
func (ss *StagingSessionStore) Remove(id string) {
	ss.sessions.Delete(id)
	dir := filepath.Join(ss.stagingDir, id)
	os.RemoveAll(dir)
}

// StartJanitor runs a background goroutine that reaps sessions older than TTL
// and removes orphaned staging directories. It stops when ctx is canceled.
func (ss *StagingSessionStore) StartJanitor(ctx context.Context) {
	log := fractalog.Component("staging-janitor")
	go func() {
		ticker := time.NewTicker(janitorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ss.reapExpired(log)
				ss.reapOrphanedDirs(log)
			}
		}
	}()
}

// reapExpired removes all sessions whose lastUsed exceeds the TTL.
func (ss *StagingSessionStore) reapExpired(log *slog.Logger) {
	now := time.Now()
	ss.sessions.Range(func(key, value any) bool {
		s := value.(*StagingSession)
		if now.Sub(s.LastUsed()) > ss.ttl {
			id := key.(string)
			if log != nil {
				log.Info("reaping expired session",
					"session_id", id,
					"age", fmt.Sprintf("%.0fm", now.Sub(s.CreatedAt()).Minutes()),
				)
			}
			ss.Remove(id)
		}
		return true
	})
}

// reapOrphanedDirs scans the staging directory for subdirectories older than
// the TTL and removes them. This catches:
// - Auto-resolve run directories from crashed sidecar runs (not tracked by sessions)
// - Abandoned session directories from workflows that never cleaned up
// - Any other orphaned directories from unexpected failures
func (ss *StagingSessionStore) reapOrphanedDirs(log *slog.Logger) {
	if ss.stagingDir == "" {
		return
	}

	entries, err := os.ReadDir(ss.stagingDir)
	if err != nil {
		return // directory may not exist yet
	}

	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip directories that belong to active sessions.
		if _, ok := ss.sessions.Load(entry.Name()); ok {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		age := now.Sub(info.ModTime())
		if age > ss.ttl {
			dirPath := filepath.Join(ss.stagingDir, entry.Name())
			if log != nil {
				log.Info("removing orphaned staging directory",
					"dir", entry.Name(),
					"age", fmt.Sprintf("%.0fm", age.Minutes()),
				)
			}
			os.RemoveAll(dirPath)
		}
	}
}
