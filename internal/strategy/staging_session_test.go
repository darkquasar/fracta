package strategy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStagingSession_PutGet(t *testing.T) {
	s := NewStagingSession()

	s.Put("alerts", "/tmp/staging/alerts.parquet")
	p, ok := s.Get("alerts")
	if !ok {
		t.Fatal("Get returned false for existing table")
	}
	if p != "/tmp/staging/alerts.parquet" {
		t.Errorf("path = %q, want %q", p, "/tmp/staging/alerts.parquet")
	}

	_, ok = s.Get("nonexistent")
	if ok {
		t.Error("Get returned true for nonexistent table")
	}
}

func TestStagingSession_PutOverwrites(t *testing.T) {
	s := NewStagingSession()

	s.Put("alerts", "/old/path.parquet")
	s.Put("alerts", "/new/path.parquet")

	p, _ := s.Get("alerts")
	if p != "/new/path.parquet" {
		t.Errorf("path = %q, want %q", p, "/new/path.parquet")
	}
}

func TestStagingSession_PutChunk(t *testing.T) {
	s := NewStagingSession()

	s.PutChunk("alerts", "/tmp/staging/abc123/alerts_chunk_1.parquet")
	p, ok := s.Get("alerts")
	if !ok {
		t.Fatal("Get returned false after PutChunk")
	}
	expected := filepath.Join("/tmp/staging/abc123", "alerts_chunk_*.parquet")
	if p != expected {
		t.Errorf("path = %q, want %q", p, expected)
	}

	// Second chunk should produce the same glob
	s.PutChunk("alerts", "/tmp/staging/abc123/alerts_chunk_2.parquet")
	p2, _ := s.Get("alerts")
	if p2 != expected {
		t.Errorf("after second chunk: path = %q, want %q", p2, expected)
	}
}

func TestStagingSession_All(t *testing.T) {
	s := NewStagingSession()
	s.Put("alerts", "/a.parquet")
	s.Put("hosts", "/h.parquet")

	all := s.All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
	if all["alerts"] != "/a.parquet" {
		t.Errorf("alerts = %q", all["alerts"])
	}
	if all["hosts"] != "/h.parquet" {
		t.Errorf("hosts = %q", all["hosts"])
	}

	// Verify All() returns a copy (mutation doesn't affect session)
	all["alerts"] = "mutated"
	p, _ := s.Get("alerts")
	if p == "mutated" {
		t.Error("All() returned a reference, not a copy")
	}
}

func TestStagingSession_Tables(t *testing.T) {
	s := NewStagingSession()
	s.Put("hosts", "/h.parquet")
	s.Put("alerts", "/a.parquet")
	s.Put("events", "/e.parquet")

	tables := s.Tables()
	if len(tables) != 3 {
		t.Fatalf("Tables() len = %d, want 3", len(tables))
	}
	// Should be sorted
	if tables[0] != "alerts" || tables[1] != "events" || tables[2] != "hosts" {
		t.Errorf("Tables() = %v, want [alerts events hosts]", tables)
	}
}

func TestStagingSession_IDFormat(t *testing.T) {
	s := NewStagingSession()
	if len(s.ID) != 8 {
		t.Errorf("ID length = %d, want 8", len(s.ID))
	}
	// Should be hex characters only
	for _, c := range s.ID {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("ID contains non-hex char %q", string(c))
		}
	}
}

func TestStagingSession_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		s := NewStagingSession()
		if ids[s.ID] {
			t.Fatalf("duplicate ID %q on iteration %d", s.ID, i)
		}
		ids[s.ID] = true
	}
}

func TestStagingSession_LastUsed(t *testing.T) {
	s := NewStagingSession()
	before := s.LastUsed()

	time.Sleep(5 * time.Millisecond)
	s.Put("alerts", "/a.parquet")
	after := s.LastUsed()

	if !after.After(before) {
		t.Error("LastUsed was not updated after Put")
	}
}

func TestStagingSessionStore_CreateAndGet(t *testing.T) {
	dir := t.TempDir()
	store := NewStagingSessionStore(dir)

	s := store.Create()
	if s == nil {
		t.Fatal("Create returned nil")
	}

	got, ok := store.Get(s.ID)
	if !ok {
		t.Fatal("Get returned false for created session")
	}
	if got.ID != s.ID {
		t.Errorf("ID = %q, want %q", got.ID, s.ID)
	}

	// Verify directory was created on disk
	sessionDir := filepath.Join(dir, s.ID)
	info, err := os.Stat(sessionDir)
	if err != nil {
		t.Fatalf("session dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("session path is not a directory")
	}
}

func TestStagingSessionStore_GetMissing(t *testing.T) {
	store := NewStagingSessionStore(t.TempDir())
	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("Get returned true for nonexistent session")
	}
}

func TestStagingSessionStore_Remove(t *testing.T) {
	dir := t.TempDir()
	store := NewStagingSessionStore(dir)
	s := store.Create()

	// Write a file into the session directory
	sessionDir := filepath.Join(dir, s.ID)
	os.WriteFile(filepath.Join(sessionDir, "test.parquet"), []byte("data"), 0o644)

	store.Remove(s.ID)

	_, ok := store.Get(s.ID)
	if ok {
		t.Error("Get returned true after Remove")
	}

	// Verify directory was removed
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Error("session directory still exists after Remove")
	}
}

func TestStagingSessionStore_Janitor(t *testing.T) {
	dir := t.TempDir()
	store := NewStagingSessionStore(dir)
	store.ttl = 50 * time.Millisecond // Very short TTL for testing

	s := store.Create()
	s.Put("alerts", "/a.parquet")

	// Wait for session to expire
	time.Sleep(100 * time.Millisecond)

	// Start janitor and let it run one cycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Manually trigger reap instead of waiting for ticker
	store.reapExpired(nil) // nil logger is safe — we'll test that separately

	_, ok := store.Get(s.ID)
	if ok {
		t.Error("expired session was not reaped")
	}

	// Verify directory was cleaned
	sessionDir := filepath.Join(dir, s.ID)
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Error("session directory still exists after reap")
	}

	_ = ctx // used by deferred cancel
}

func TestStagingSessionStore_JanitorPreservesActive(t *testing.T) {
	dir := t.TempDir()
	store := NewStagingSessionStore(dir)
	store.ttl = 1 * time.Hour // Very long TTL

	s := store.Create()
	s.Put("alerts", "/a.parquet")

	store.reapExpired(nil)

	_, ok := store.Get(s.ID)
	if ok != true {
		t.Error("active session was incorrectly reaped")
	}
}

func TestStagingSessionStore_JanitorStopsOnCancel(t *testing.T) {
	store := NewStagingSessionStore(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	store.StartJanitor(ctx)
	cancel() // Should not panic or leak goroutines
}

func TestStagingSessionStore_ConcurrentSessions(t *testing.T) {
	dir := t.TempDir()
	store := NewStagingSessionStore(dir)

	// Create two sessions with same table name
	s1 := store.Create()
	s2 := store.Create()

	s1.Put("alerts", filepath.Join(dir, s1.ID, "alerts.parquet"))
	s2.Put("alerts", filepath.Join(dir, s2.ID, "alerts.parquet"))

	p1, _ := s1.Get("alerts")
	p2, _ := s2.Get("alerts")

	if p1 == p2 {
		t.Error("concurrent sessions produced identical paths for same table")
	}

	// Remove one session should not affect the other
	store.Remove(s1.ID)
	_, ok := store.Get(s2.ID)
	if !ok {
		t.Error("removing s1 also removed s2")
	}
}

func TestStagingSessionStore_DefaultTTL(t *testing.T) {
	store := NewStagingSessionStore(t.TempDir())
	if store.ttl != DefaultSessionTTL {
		t.Errorf("ttl = %v, want %v", store.ttl, DefaultSessionTTL)
	}
}

// ---------------------------------------------------------------------------
// S9-partial: ParamsFingerprint
// ---------------------------------------------------------------------------

func TestComputeParamsFingerprint_Deterministic(t *testing.T) {
	params := map[string]any{
		"days_back": 7,
		"ip":        "10.0.0.1",
		"verbose":   true,
	}

	fp1 := ComputeParamsFingerprint(params)
	fp2 := ComputeParamsFingerprint(params)

	if fp1 == "" {
		t.Fatal("fingerprint is empty")
	}
	if len(fp1) != 16 {
		t.Errorf("fingerprint length = %d, want 16", len(fp1))
	}
	if fp1 != fp2 {
		t.Errorf("non-deterministic: %q != %q", fp1, fp2)
	}
}

func TestComputeParamsFingerprint_DifferentParams(t *testing.T) {
	fp1 := ComputeParamsFingerprint(map[string]any{"days_back": 7})
	fp2 := ComputeParamsFingerprint(map[string]any{"days_back": 14})

	if fp1 == fp2 {
		t.Error("different params produced same fingerprint")
	}
}

func TestComputeParamsFingerprint_KeyOrder(t *testing.T) {
	// json.Marshal sorts map keys, so different insertion order should produce
	// the same fingerprint.
	fp1 := ComputeParamsFingerprint(map[string]any{"a": 1, "b": 2, "c": 3})
	fp2 := ComputeParamsFingerprint(map[string]any{"c": 3, "a": 1, "b": 2})

	if fp1 != fp2 {
		t.Errorf("key order affected fingerprint: %q != %q", fp1, fp2)
	}
}

func TestComputeParamsFingerprint_EmptyParams(t *testing.T) {
	if fp := ComputeParamsFingerprint(nil); fp != "" {
		t.Errorf("nil params fingerprint = %q, want empty", fp)
	}
	if fp := ComputeParamsFingerprint(map[string]any{}); fp != "" {
		t.Errorf("empty params fingerprint = %q, want empty", fp)
	}
}

func TestStagingSession_ParamsFingerprint(t *testing.T) {
	s := NewStagingSession()
	s.ParamsFingerprint = ComputeParamsFingerprint(map[string]any{"ip": "10.0.0.1"})

	if s.ParamsFingerprint == "" {
		t.Fatal("ParamsFingerprint not set")
	}
	if len(s.ParamsFingerprint) != 16 {
		t.Errorf("ParamsFingerprint length = %d, want 16", len(s.ParamsFingerprint))
	}
}

// ---------------------------------------------------------------------------
// S5: Age-based orphaned directory cleanup
// ---------------------------------------------------------------------------

func TestReapOrphanedDirs_RemovesOldDirs(t *testing.T) {
	dir := t.TempDir()
	store := NewStagingSessionStore(dir)
	store.ttl = 50 * time.Millisecond

	// Create an orphaned directory (not tracked by any session).
	orphanDir := filepath.Join(dir, "orphan-run-abc123")
	os.MkdirAll(orphanDir, 0o755)
	os.WriteFile(filepath.Join(orphanDir, "data.parquet"), []byte("data"), 0o644)

	// Set its mod time to the past so it's older than TTL.
	past := time.Now().Add(-1 * time.Hour)
	os.Chtimes(orphanDir, past, past)

	store.reapOrphanedDirs(nil)

	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Error("orphaned directory should have been removed")
	}
}

func TestReapOrphanedDirs_PreservesActiveSessions(t *testing.T) {
	dir := t.TempDir()
	store := NewStagingSessionStore(dir)
	store.ttl = 50 * time.Millisecond

	// Create a session — its directory should be preserved even if "old" on disk,
	// because it's tracked by the session store.
	s := store.Create()
	sessionDir := filepath.Join(dir, s.ID)

	// Set mod time to the past.
	past := time.Now().Add(-1 * time.Hour)
	os.Chtimes(sessionDir, past, past)

	store.reapOrphanedDirs(nil)

	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		t.Error("active session directory should NOT be removed by orphan reaper")
	}
}

func TestReapOrphanedDirs_PreservesRecentDirs(t *testing.T) {
	dir := t.TempDir()
	store := NewStagingSessionStore(dir)
	store.ttl = 1 * time.Hour

	// Create a directory that's not tracked but is recent.
	recentDir := filepath.Join(dir, "recent-run-xyz")
	os.MkdirAll(recentDir, 0o755)

	store.reapOrphanedDirs(nil)

	if _, err := os.Stat(recentDir); os.IsNotExist(err) {
		t.Error("recent orphaned directory should NOT be removed (within TTL)")
	}
}

func TestReapOrphanedDirs_IgnoresFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewStagingSessionStore(dir)
	store.ttl = 50 * time.Millisecond

	// Create a file (not a directory) in staging dir.
	filePath := filepath.Join(dir, "stray-file.txt")
	os.WriteFile(filePath, []byte("stray"), 0o644)

	past := time.Now().Add(-1 * time.Hour)
	os.Chtimes(filePath, past, past)

	store.reapOrphanedDirs(nil)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("reaper should not remove files, only directories")
	}
}

func TestReapOrphanedDirs_EmptyStagingDir(t *testing.T) {
	store := NewStagingSessionStore("")
	// Should not panic with empty staging dir.
	store.reapOrphanedDirs(nil)
}

func TestReapOrphanedDirs_NonexistentStagingDir(t *testing.T) {
	store := NewStagingSessionStore("/nonexistent/path/that/does/not/exist")
	// Should not panic.
	store.reapOrphanedDirs(nil)
}
