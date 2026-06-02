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

// HostInfo is the trimmed view of GET /info we expose.
type HostInfo struct {
	Containers        int    `json:"containers"`
	ContainersRunning int    `json:"containers_running"`
	ContainersPaused  int    `json:"containers_paused"`
	ContainersStopped int    `json:"containers_stopped"`
	Images            int    `json:"images"`
	NCPU              int    `json:"ncpu"`
	MemTotal          int64  `json:"mem_total"`
	KernelVersion     string `json:"kernel_version"`
	OperatingSystem   string `json:"operating_system"`
	DockerVersion     string `json:"docker_version"`
	Name              string `json:"name"`
}

// Info hits the docker /info endpoint to learn about the host (memory,
// cpu count, kernel, etc.) without needing /proc mounts in the container.
func (d *DockerClient) Info(ctx context.Context) (*HostInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://unix/info", nil)
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker /info: %s", resp.Status)
	}
	var raw struct {
		Containers        int    `json:"Containers"`
		ContainersRunning int    `json:"ContainersRunning"`
		ContainersPaused  int    `json:"ContainersPaused"`
		ContainersStopped int    `json:"ContainersStopped"`
		Images            int    `json:"Images"`
		NCPU              int    `json:"NCPU"`
		MemTotal          int64  `json:"MemTotal"`
		KernelVersion     string `json:"KernelVersion"`
		OperatingSystem   string `json:"OperatingSystem"`
		ServerVersion     string `json:"ServerVersion"`
		Name              string `json:"Name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return &HostInfo{
		Containers:        raw.Containers,
		ContainersRunning: raw.ContainersRunning,
		ContainersPaused:  raw.ContainersPaused,
		ContainersStopped: raw.ContainersStopped,
		Images:            raw.Images,
		NCPU:              raw.NCPU,
		MemTotal:          raw.MemTotal,
		KernelVersion:     raw.KernelVersion,
		OperatingSystem:   raw.OperatingSystem,
		DockerVersion:     raw.ServerVersion,
		Name:              raw.Name,
	}, nil
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
