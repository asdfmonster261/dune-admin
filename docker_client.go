package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DockerClient is a minimal Docker Engine API client over /var/run/docker.sock.
// Just enough for the operations we need: list containers, stream logs, exec.
type DockerClient struct {
	hc      *http.Client
	project string
}

func NewDockerClient(project string) *DockerClient {
	return &DockerClient{
		project: project,
		hc: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", "/var/run/docker.sock")
				},
			},
		},
	}
}

// streamingClient strips the read timeout for endpoints that intentionally
// hold the connection open (logs?follow=true, exec/start, events).
func (d *DockerClient) streamingClient() *http.Client {
	return &http.Client{Transport: d.hc.Transport}
}

// ContainerInfo is the trimmed view of a docker container we expose.
type ContainerInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`    // first /-prefixed name without the leading slash
	Service string `json:"service"` // com.docker.compose.service label
	State   string `json:"state"`   // running / exited / created / etc.
	Status  string `json:"status"`  // e.g. "Up 2 hours"
}

// ListContainers returns containers in our compose project. If serviceFilter
// is non-empty, only matching services are returned.
func (d *DockerClient) ListContainers(ctx context.Context, serviceFilter string) ([]ContainerInfo, error) {
	filters := map[string][]string{
		"label": {"com.docker.compose.project=" + d.project},
	}
	if serviceFilter != "" {
		filters["label"] = append(filters["label"], "com.docker.compose.service="+serviceFilter)
	}
	fb, _ := json.Marshal(filters)
	q := url.Values{}
	q.Set("all", "true")
	q.Set("filters", string(fb))

	req, _ := http.NewRequestWithContext(ctx, "GET", "http://unix/containers/json?"+q.Encode(), nil)
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker list: %s", resp.Status)
	}
	var raw []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Labels map[string]string `json:"Labels"`
		State  string            `json:"State"`
		Status string            `json:"Status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]ContainerInfo, 0, len(raw))
	for _, c := range raw {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out = append(out, ContainerInfo{
			ID:      c.ID,
			Name:    name,
			Service: c.Labels["com.docker.compose.service"],
			State:   c.State,
			Status:  c.Status,
		})
	}
	return out, nil
}
