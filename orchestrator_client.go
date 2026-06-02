package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OrchestratorClient talks to dune-orchestrator's emulated Kubernetes API
// over HTTPS. The orchestrator serves BattleGroup, ServerSetScale,
// ServerStats, and BattleGroupDirectorStats CRs at
// /apis/igw.funcom.com/v1/...
type OrchestratorClient struct {
	hc      *http.Client
	baseURL string
}

func NewOrchestratorClient(baseURL string, insecureSkipVerify bool) *OrchestratorClient {
	return &OrchestratorClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		hc: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: insecureSkipVerify,
					MinVersion:         tls.VersionTLS12,
				},
			},
		},
	}
}

// Get fetches a resource and decodes the JSON response into v.
func (o *OrchestratorClient) Get(ctx context.Context, path string, v any) error {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, "GET", o.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return fmt.Errorf("orchestrator %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("orchestrator %s: %s — %s", path, resp.Status, body)
	}
	if v == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// BattleGroupCR is the trimmed view we read for status display.
type BattleGroupCR struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec   map[string]any `json:"spec"`
	Status map[string]any `json:"status"`
}

func (o *OrchestratorClient) GetBattleGroup(ctx context.Context, ns, name string) (*BattleGroupCR, error) {
	var bg BattleGroupCR
	if err := o.Get(ctx, fmt.Sprintf("/apis/igw.funcom.com/v1/namespaces/%s/battlegroups/%s", ns, name), &bg); err != nil {
		return nil, err
	}
	return &bg, nil
}

// ServerStatsList is what the game-servers PATCH into the orchestrator.
type ServerStatsCR struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec   map[string]any `json:"spec"`
	Status map[string]any `json:"status"`
}

type ServerStatsList struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Items      []ServerStatsCR `json:"items"`
}

func (o *OrchestratorClient) ListServerStats(ctx context.Context, ns string) (*ServerStatsList, error) {
	var list ServerStatsList
	if err := o.Get(ctx, fmt.Sprintf("/apis/igw.funcom.com/v1/namespaces/%s/serverstats", ns), &list); err != nil {
		return nil, err
	}
	return &list, nil
}
