package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	"github.com/moby/go-archive"
)

const networkName = "st_net"

// Services the UI may build/start/stop (not console-api itself).
var managedServices = []string{"mock-server", "vegeta-client"}

type serviceSpec struct {
	Name          string
	Image         string
	ContainerName string
	Dockerfile    string
	HostPort      string
	ContainerPort string
	Env           []string
	NetworkAlias  string
}

var serviceSpecs = map[string]serviceSpec{
	"mock-server": {
		Name: "mock-server", Image: "st-do-asign/mock-server:local",
		ContainerName: "st-mock-server", Dockerfile: "build/Dockerfile.mock-server",
		HostPort: "8081", ContainerPort: "8081", NetworkAlias: "mock-server",
		Env: []string{"MOCK_SERVER_ADDR=0.0.0.0:8081"},
	},
	"vegeta-client": {
		Name: "vegeta-client", Image: "st-do-asign/vegeta-client:local",
		ContainerName: "st-vegeta-client", Dockerfile: "build/Dockerfile.vegeta-client",
		HostPort: "8090", ContainerPort: "8090", NetworkAlias: "vegeta-client",
		Env: []string{
			"VEGETA_CLIENT_ADDR=0.0.0.0:8090",
			"RATE_LIMITER_URL=http://mock-server:8081",
		},
	},
}

type orchestrator struct {
	projectRoot string
	docker      *client.Client

	mu   sync.Mutex
	jobs map[string]*Job
}

type JobStatus string

const (
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobOK      JobStatus = "succeeded"
	JobFailed  JobStatus = "failed"
)

type Job struct {
	ID        string     `json:"id"`
	Action    string     `json:"action"`
	Target    string     `json:"target"`
	Status    JobStatus  `json:"status"`
	Output    string     `json:"output"`
	Error     string     `json:"error,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

func newOrchestrator() *orchestrator {
	root := envOr("PROJECT_ROOT", findProjectRoot())
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(fmt.Sprintf("docker client: %v", err))
	}
	return &orchestrator{
		projectRoot: root,
		docker:      cli,
		jobs:        map[string]*Job{},
	}
}

func findProjectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if strings.Contains(filepath.ToSlash(wd), "backend/console-api") {
		return filepath.Clean(filepath.Join(wd, "..", ".."))
	}
	if _, err := os.Stat(filepath.Join(wd, "build", "docker-compose.yml")); err == nil {
		return wd
	}
	return wd
}

func (o *orchestrator) listJobs() []Job {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Job, 0, len(o.jobs))
	for _, j := range o.jobs {
		out = append(out, *j)
	}
	return out
}

func (o *orchestrator) getJob(id string) (Job, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	j, ok := o.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

func (o *orchestrator) startJob(action, target string, fn func(ctx context.Context) (string, error)) Job {
	id := uuid.NewString()
	job := &Job{
		ID: id, Action: action, Target: target,
		Status: JobQueued, StartedAt: time.Now().UTC(),
	}
	o.mu.Lock()
	o.jobs[id] = job
	o.mu.Unlock()

	go func() {
		o.patch(id, func(j *Job) { j.Status = JobRunning })
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		out, err := fn(ctx)
		now := time.Now().UTC()
		o.patch(id, func(j *Job) {
			j.Output = trimOut(out)
			j.EndedAt = &now
			if err != nil {
				j.Status = JobFailed
				j.Error = err.Error()
			} else {
				j.Status = JobOK
			}
		})
	}()
	return *job
}

func (o *orchestrator) patch(id string, fn func(*Job)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if j, ok := o.jobs[id]; ok {
		fn(j)
	}
}

func (o *orchestrator) ensureNetwork(ctx context.Context) error {
	_, err := o.docker.NetworkInspect(ctx, networkName, network.InspectOptions{})
	if err == nil {
		return nil
	}
	_, err = o.docker.NetworkCreate(ctx, networkName, network.CreateOptions{
		Driver: "bridge",
	})
	return err
}

func (o *orchestrator) buildImage(ctx context.Context, spec serviceSpec) (string, error) {
	tar, err := archive.TarWithOptions(o.projectRoot, &archive.TarOptions{})
	if err != nil {
		return "", fmt.Errorf("tar context: %w", err)
	}
	defer tar.Close()

	resp, err := o.docker.ImageBuild(ctx, tar, types.ImageBuildOptions{
		Dockerfile: spec.Dockerfile,
		Tags:       []string{spec.Image},
		Remove:     true,
		PullParent: true,
	})
	if err != nil {
		return "", fmt.Errorf("image build %s: %w", spec.Name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	text := string(raw)
	if strings.Contains(text, `"errorDetail"`) || strings.Contains(text, `"error":"`) {
		return text, fmt.Errorf("build failed for %s — see output", spec.Name)
	}
	return fmt.Sprintf("built %s\n%s", spec.Image, trimOut(text)), nil
}

func (o *orchestrator) stopAndRemove(ctx context.Context, containerName string) error {
	timeout := 10
	_ = o.docker.ContainerStop(ctx, containerName, container.StopOptions{Timeout: &timeout})
	_ = o.docker.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})
	return nil
}

func (o *orchestrator) startContainer(ctx context.Context, spec serviceSpec) (string, error) {
	if err := o.ensureNetwork(ctx); err != nil {
		return "", err
	}
	_ = o.stopAndRemove(ctx, spec.ContainerName)

	port, err := nat.NewPort("tcp", spec.ContainerPort)
	if err != nil {
		return "", err
	}
	resp, err := o.docker.ContainerCreate(ctx, &container.Config{
		Image: spec.Image,
		Env:   spec.Env,
		ExposedPorts: nat.PortSet{
			port: struct{}{},
		},
	}, &container.HostConfig{
		PortBindings: nat.PortMap{
			port: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: spec.HostPort}},
		},
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {Aliases: []string{spec.NetworkAlias}},
		},
	}, nil, spec.ContainerName)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", spec.Name, err)
	}
	if err := o.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start %s: %w", spec.Name, err)
	}
	short := resp.ID
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("started %s (%s)", spec.ContainerName, short), nil
}

func (o *orchestrator) pipelineUp(rebuild bool) Job {
	return o.startJob("pipeline.up", "all", func(ctx context.Context) (string, error) {
		var b strings.Builder
		for _, name := range managedServices {
			spec := serviceSpecs[name]
			if rebuild {
				out, err := o.buildImage(ctx, spec)
				b.WriteString(out)
				b.WriteString("\n")
				if err != nil {
					return b.String(), err
				}
			}
			out, err := o.startContainer(ctx, spec)
			b.WriteString(out)
			b.WriteString("\n")
			if err != nil {
				return b.String(), err
			}
		}
		return b.String(), nil
	})
}

func (o *orchestrator) pipelineDown() Job {
	return o.startJob("pipeline.down", "all", func(ctx context.Context) (string, error) {
		var b strings.Builder
		for _, name := range managedServices {
			spec := serviceSpecs[name]
			_ = o.stopAndRemove(ctx, spec.ContainerName)
			b.WriteString("stopped ")
			b.WriteString(spec.ContainerName)
			b.WriteString("\n")
		}
		return b.String(), nil
	})
}

func (o *orchestrator) pipelineBuild() Job {
	return o.startJob("pipeline.build", "all", func(ctx context.Context) (string, error) {
		var b strings.Builder
		for _, name := range managedServices {
			out, err := o.buildImage(ctx, serviceSpecs[name])
			b.WriteString(out)
			b.WriteString("\n")
			if err != nil {
				return b.String(), err
			}
		}
		return b.String(), nil
	})
}

func (o *orchestrator) serviceUp(name string, rebuild bool) (Job, error) {
	spec, err := lookupSpec(name)
	if err != nil {
		return Job{}, err
	}
	return o.startJob("service.up", name, func(ctx context.Context) (string, error) {
		var b strings.Builder
		if rebuild {
			out, err := o.buildImage(ctx, spec)
			b.WriteString(out + "\n")
			if err != nil {
				return b.String(), err
			}
		}
		out, err := o.startContainer(ctx, spec)
		b.WriteString(out + "\n")
		return b.String(), err
	}), nil
}

func (o *orchestrator) serviceDown(name string) (Job, error) {
	spec, err := lookupSpec(name)
	if err != nil {
		return Job{}, err
	}
	return o.startJob("service.down", name, func(ctx context.Context) (string, error) {
		_ = o.stopAndRemove(ctx, spec.ContainerName)
		return "stopped " + spec.ContainerName, nil
	}), nil
}

func (o *orchestrator) serviceBuild(name string) (Job, error) {
	spec, err := lookupSpec(name)
	if err != nil {
		return Job{}, err
	}
	return o.startJob("service.build", name, func(ctx context.Context) (string, error) {
		return o.buildImage(ctx, spec)
	}), nil
}

type composeServiceStatus struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Status string `json:"status"`
	Health string `json:"health,omitempty"`
}

func (o *orchestrator) status(ctx context.Context) ([]composeServiceStatus, string, error) {
	var out []composeServiceStatus
	for _, name := range managedServices {
		spec := serviceSpecs[name]
		c, err := o.docker.ContainerInspect(ctx, spec.ContainerName)
		if err != nil {
			out = append(out, composeServiceStatus{Name: name, State: "absent", Status: "not found"})
			continue
		}
		state := "unknown"
		health := ""
		if c.State != nil {
			state = c.State.Status
			if c.State.Health != nil {
				health = c.State.Health.Status
			}
		}
		out = append(out, composeServiceStatus{
			Name: name, State: state, Status: state, Health: health,
		})
	}
	if c, err := o.docker.ContainerInspect(ctx, "st-console-api"); err == nil && c.State != nil {
		out = append(out, composeServiceStatus{
			Name: "console-api", State: c.State.Status, Status: c.State.Status,
		})
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return out, string(raw), nil
}

func lookupSpec(name string) (serviceSpec, error) {
	spec, ok := serviceSpecs[name]
	if !ok {
		return serviceSpec{}, fmt.Errorf("service %q is not managed (allowed: %s)", name, strings.Join(managedServices, ", "))
	}
	return spec, nil
}

func trimOut(s string) string {
	const max = 48_000
	if len(s) > max {
		return s[len(s)-max:]
	}
	return s
}
