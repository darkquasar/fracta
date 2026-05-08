package cpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/darkquasar/fracta/internal/auth/credentials"
)

// RemoteCredentialStager implements credentials.CredentialStager by calling the
// control-plane API's staging endpoint. Used by the host-side thin client
// (StagingSpawnClient) when the control plane runs in-cluster.
type RemoteCredentialStager struct {
	baseURL    string
	httpClient *http.Client
}

// NewRemoteCredentialStager creates a stager that calls the CP API.
func NewRemoteCredentialStager(baseURL string) *RemoteCredentialStager {
	return &RemoteCredentialStager{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *RemoteCredentialStager) Stage(ctx context.Context, sourceName string, data []byte, mountPath string, ttl time.Duration) (string, error) {
	req := StageCredentialRequest{
		SourceName: sourceName,
		Data:       base64.StdEncoding.EncodeToString(data),
		MountPath:  mountPath,
		TTLSeconds: int(ttl.Seconds()),
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal stage request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/v1/credentials/stage", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create stage request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("stage credential: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("stage credential: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var stageResp StageCredentialResponse
	if err := json.NewDecoder(resp.Body).Decode(&stageResp); err != nil {
		return "", fmt.Errorf("decode stage response: %w", err)
	}

	return stageResp.Ref, nil
}

func (s *RemoteCredentialStager) Fetch(ctx context.Context, ref string) (*credentials.StagedCredential, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/api/v1/credentials/stage/"+url.PathEscape(ref), nil)
	if err != nil {
		return nil, fmt.Errorf("create fetch request: %w", err)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fetch staged credential: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch staged credential: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var fetchResp struct {
		SourceName string `json:"source_name"`
		Data       string `json:"data"`      // base64-encoded
		MountPath  string `json:"mount_path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fetchResp); err != nil {
		return nil, fmt.Errorf("decode fetch response: %w", err)
	}

	data, err := base64.StdEncoding.DecodeString(fetchResp.Data)
	if err != nil {
		return nil, fmt.Errorf("decode staged credential data: %w", err)
	}

	return &credentials.StagedCredential{
		SourceName: fetchResp.SourceName,
		Data:       data,
		MountPath:  fetchResp.MountPath,
	}, nil
}

func (s *RemoteCredentialStager) Cleanup(ctx context.Context, ref string) error {
	// Cleanup is handled server-side by TTL or reaper. No client-side action needed.
	return nil
}

// Compile-time check.
var _ credentials.CredentialStager = (*RemoteCredentialStager)(nil)
