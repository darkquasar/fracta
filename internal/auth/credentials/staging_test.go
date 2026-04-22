package credentials

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryCredentialStager_StageAndFetch(t *testing.T) {
	s := NewInMemoryCredentialStager()
	ctx := context.Background()

	ref, err := s.Stage(ctx, "proxy", []byte("token-abc"), "/var/run/creds", 5*time.Minute)
	require.NoError(t, err)
	assert.NotEmpty(t, ref)
	assert.Equal(t, 1, s.Len())

	cred, err := s.Fetch(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, "proxy", cred.SourceName)
	assert.Equal(t, []byte("token-abc"), cred.Data)
	assert.Equal(t, "/var/run/creds", cred.MountPath)
}

func TestInMemoryCredentialStager_FetchNotFound(t *testing.T) {
	s := NewInMemoryCredentialStager()
	ctx := context.Background()

	_, err := s.Fetch(ctx, "nonexistent-ref")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestInMemoryCredentialStager_FetchExpired(t *testing.T) {
	s := NewInMemoryCredentialStager()
	ctx := context.Background()

	// Stage with 1ms TTL — will expire immediately.
	ref, err := s.Stage(ctx, "proxy", []byte("token"), "/mount", 1*time.Millisecond)
	require.NoError(t, err)

	time.Sleep(5 * time.Millisecond)

	_, err = s.Fetch(ctx, ref)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")

	// Entry should be cleaned up after expired fetch.
	assert.Equal(t, 0, s.Len())
}

func TestInMemoryCredentialStager_StageEmptyData(t *testing.T) {
	s := NewInMemoryCredentialStager()
	ctx := context.Background()

	_, err := s.Stage(ctx, "proxy", []byte{}, "/mount", 5*time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty data")
}

func TestInMemoryCredentialStager_Cleanup(t *testing.T) {
	s := NewInMemoryCredentialStager()
	ctx := context.Background()

	ref, err := s.Stage(ctx, "proxy", []byte("token"), "/mount", 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, s.Len())

	err = s.Cleanup(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, 0, s.Len())

	// Fetch after cleanup should fail.
	_, err = s.Fetch(ctx, ref)
	require.Error(t, err)
}

func TestInMemoryCredentialStager_CleanupNotFound(t *testing.T) {
	s := NewInMemoryCredentialStager()
	ctx := context.Background()

	err := s.Cleanup(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestInMemoryCredentialStager_MultipleSourcesIndependent(t *testing.T) {
	s := NewInMemoryCredentialStager()
	ctx := context.Background()

	ref1, err := s.Stage(ctx, "proxy", []byte("token-1"), "/mount/a", 5*time.Minute)
	require.NoError(t, err)

	ref2, err := s.Stage(ctx, "host_fallback", []byte("token-2"), "/mount/b", 5*time.Minute)
	require.NoError(t, err)

	assert.NotEqual(t, ref1, ref2)
	assert.Equal(t, 2, s.Len())

	cred1, err := s.Fetch(ctx, ref1)
	require.NoError(t, err)
	assert.Equal(t, "proxy", cred1.SourceName)
	assert.Equal(t, []byte("token-1"), cred1.Data)

	cred2, err := s.Fetch(ctx, ref2)
	require.NoError(t, err)
	assert.Equal(t, "host_fallback", cred2.SourceName)
	assert.Equal(t, []byte("token-2"), cred2.Data)
}

func TestInMemoryCredentialStager_FetchReturnsCopy(t *testing.T) {
	s := NewInMemoryCredentialStager()
	ctx := context.Background()

	ref, err := s.Stage(ctx, "proxy", []byte("original"), "/mount", 5*time.Minute)
	require.NoError(t, err)

	cred1, err := s.Fetch(ctx, ref)
	require.NoError(t, err)

	// Mutate the returned data.
	cred1.Data[0] = 'X'

	// Fetch again — should still return original data.
	cred2, err := s.Fetch(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), cred2.Data)
}
