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
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	"github.com/moby/go-archive"
)

const networkName = "st_net"

// Order matters for pipeline.up: infra first, then scratch apps.
var managedServices = []string{"mongo", "redis", "mock-server", "rate-limiter-a", "rate-limiter-b", "rate-limiter", "vegeta-client", "control-plane"}

type volumeBind struct {
	Name   string // named docker volume
	Target string
}

type serviceSpec struct {
	Name          string
	Image         string
	ContainerName string
	Dockerfile    string // empty ⇒ pull-only infra image (no build)
	HostPort      string
	ContainerPort string
	Env           []string
	Cmd           []string
	NetworkAlias  string
	NamedVolumes  []volumeBind
	BindMounts    []string // "hostPath:containerPath:ro" relative host paths resolved against project root
	HealthTest    []string // docker HEALTHCHECK CMD
}

func (s serviceSpec) isInfra() bool { return s.Dockerfile == "" }

var serviceSpecs = map[string]serviceSpec{
	"mongo": {
		Name: "mongo", Image: "mongo:7.0",
		ContainerName: "st-mongo", NetworkAlias: "mongo",
		HostPort: "27017", ContainerPort: "27017",
		Env: []string{
			"MONGO_INITDB_DATABASE=rate_limiter",
			"MONGO_INITDB_ROOT_USERNAME=root",
			"MONGO_INITDB_ROOT_PASSWORD=stroot",
		},
		NamedVolumes: []volumeBind{{Name: "st-do-asign_mongo_data", Target: "/data/db"}},
		BindMounts:   []string{"build/mongo:/docker-entrypoint-initdb.d:ro"},
		HealthTest: []string{
			"CMD", "mongosh", "--quiet",
			"-u", "root", "-p", "stroot",
			"--authenticationDatabase", "admin",
			"--eval", "db.adminCommand('ping').ok",
		},
	},
	"redis": {
		Name: "redis", Image: "redis:7.4-alpine",
		ContainerName: "st-redis", NetworkAlias: "redis",
		HostPort: "6379", ContainerPort: "6379",
		Cmd:          []string{"redis-server", "--appendonly", "yes", "--save", "60", "1000"},
		NamedVolumes: []volumeBind{{Name: "st-do-asign_redis_data", Target: "/data"}},
		HealthTest:   []string{"CMD", "redis-cli", "ping"},
	},
	"mock-server": {
		Name: "mock-server", Image: "st-do-asign/mock-server:local",
		ContainerName: "st-mock-server", Dockerfile: "build/Dockerfile.mock-server",
		HostPort: "8081", ContainerPort: "8081", NetworkAlias: "mock-server",
		Env: []string{"MOCK_SERVER_ADDR=0.0.0.0:8081"},
	},
	"rate-limiter-a": {
		Name: "rate-limiter-a", Image: "st-do-asign/rate-limiter:local",
		ContainerName: "st-rate-limiter-a", Dockerfile: "build/Dockerfile.rate-limiter",
		HostPort: "18080", ContainerPort: "8080", NetworkAlias: "rate-limiter-a",
		Env: []string{
			"RATE_LIMITER_ADDR=0.0.0.0:8080",
			"INSTANCE_ID=a",
			"MONGO_URI=mongodb://root:stroot@mongo:27017/?authSource=admin",
			"MONGO_DB=rate_limiter",
			"REDIS_ADDR=redis:6379",
			"POLICY_POLL_INTERVAL=2s",
		},
	},
	"rate-limiter-b": {
		Name: "rate-limiter-b", Image: "st-do-asign/rate-limiter:local",
		ContainerName: "st-rate-limiter-b", Dockerfile: "build/Dockerfile.rate-limiter",
		HostPort: "18081", ContainerPort: "8080", NetworkAlias: "rate-limiter-b",
		Env: []string{
			"RATE_LIMITER_ADDR=0.0.0.0:8080",
			"INSTANCE_ID=b",
			"MONGO_URI=mongodb://root:stroot@mongo:27017/?authSource=admin",
			"MONGO_DB=rate_limiter",
			"REDIS_ADDR=redis:6379",
			"POLICY_POLL_INTERVAL=2s",
		},
	},
	"rate-limiter": {
		Name: "rate-limiter", Image: "st-do-asign/rate-limiter-lb:local",
		ContainerName: "st-rate-limiter", Dockerfile: "build/Dockerfile.rate-limiter-lb",
		HostPort: "8080", ContainerPort: "8080", NetworkAlias: "rate-limiter",
	},
	"vegeta-client": {
		Name: "vegeta-client", Image: "st-do-asign/vegeta-client:local",
		ContainerName: "st-vegeta-client", Dockerfile: "build/Dockerfile.vegeta-client",
		HostPort: "8090", ContainerPort: "8090", NetworkAlias: "vegeta-client",
		Env: []string{
			"VEGETA_CLIENT_ADDR=0.0.0.0:8090",
			"RATE_LIMITER_URL=http://rate-limiter:8080",
		},
	},
	"control-plane": {
		Name: "control-plane", Image: "st-do-asign/control-plane:local",
		ContainerName: "st-control-plane", Dockerfile: "build/Dockerfile.control-plane",
		HostPort: "8091", ContainerPort: "8091", NetworkAlias: "control-plane",
		Env: []string{
			"CONTROL_PLANE_ADDR=0.0.0.0:8091",
			"MONGO_URI=mongodb://root:stroot@mongo:27017/?authSource=admin",
			"MONGO_DB=rate_limiter",
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
	if spec.isInfra() {
		return o.pullImage(ctx, spec)
	}
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

func (o *orchestrator) pullImage(ctx context.Context, spec serviceSpec) (string, error) {
	reader, err := o.docker.ImagePull(ctx, spec.Image, image.PullOptions{})
	if err != nil {
		return "", fmt.Errorf("pull %s: %w", spec.Image, err)
	}
	defer reader.Close()
	raw, _ := io.ReadAll(reader)
	return fmt.Sprintf("pulled %s\n%s", spec.Image, trimOut(string(raw))), nil
}

func (o *orchestrator) stopAndRemove(ctx context.Context, containerName string) error {
	timeout := 10
	_ = o.docker.ContainerStop(ctx, containerName, container.StopOptions{Timeout: &timeout})
	_ = o.docker.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})
	return nil
}

func (o *orchestrator) stopContainer(ctx context.Context, spec serviceSpec) error {
	timeout := 15
	if spec.isInfra() {
		// Keep container + named volumes so Start is fast and MySQL data survives.
		return o.docker.ContainerStop(ctx, spec.ContainerName, container.StopOptions{Timeout: &timeout})
	}
	return o.stopAndRemove(ctx, spec.ContainerName)
}

func (o *orchestrator) ensureVolumes(ctx context.Context, spec serviceSpec) error {
	for _, v := range spec.NamedVolumes {
		_, err := o.docker.VolumeInspect(ctx, v.Name)
		if err == nil {
			continue
		}
		if _, err := o.docker.VolumeCreate(ctx, volume.CreateOptions{Name: v.Name}); err != nil {
			return fmt.Errorf("volume %s: %w", v.Name, err)
		}
	}
	return nil
}

func (o *orchestrator) startContainer(ctx context.Context, spec serviceSpec) (string, error) {
	if err := o.ensureNetwork(ctx); err != nil {
		return "", err
	}
	if spec.isInfra() {
		return o.startInfraContainer(ctx, spec)
	}
	_ = o.stopAndRemove(ctx, spec.ContainerName)
	return o.createAndStart(ctx, spec)
}

func (o *orchestrator) startInfraContainer(ctx context.Context, spec serviceSpec) (string, error) {
	c, err := o.docker.ContainerInspect(ctx, spec.ContainerName)
	if err == nil {
		if c.State != nil && c.State.Running {
			timeout := 10
			_ = o.docker.ContainerRestart(ctx, spec.ContainerName, container.StopOptions{Timeout: &timeout})
			return fmt.Sprintf("restarted %s", spec.ContainerName), nil
		}
		if err := o.docker.ContainerStart(ctx, spec.ContainerName, container.StartOptions{}); err != nil {
			return "", fmt.Errorf("start %s: %w", spec.Name, err)
		}
		return fmt.Sprintf("started %s", spec.ContainerName), nil
	}
	if _, err := o.pullImage(ctx, spec); err != nil {
		return "", err
	}
	return o.createAndStart(ctx, spec)
}

func (o *orchestrator) createAndStart(ctx context.Context, spec serviceSpec) (string, error) {
	if err := o.ensureVolumes(ctx, spec); err != nil {
		return "", err
	}
	port, err := nat.NewPort("tcp", spec.ContainerPort)
	if err != nil {
		return "", err
	}

	var mounts []mount.Mount
	for _, v := range spec.NamedVolumes {
		mounts = append(mounts, mount.Mount{
			Type: mount.TypeVolume, Source: v.Name, Target: v.Target,
		})
	}
	for _, b := range spec.BindMounts {
		parts := strings.Split(b, ":")
		if len(parts) < 2 {
			continue
		}
		src := parts[0]
		if !filepath.IsAbs(src) {
			src = filepath.Join(o.projectRoot, src)
		}
		m := mount.Mount{Type: mount.TypeBind, Source: src, Target: parts[1]}
		if len(parts) > 2 && parts[2] == "ro" {
			m.ReadOnly = true
		}
		mounts = append(mounts, m)
	}

	cfg := &container.Config{
		Image: spec.Image,
		Env:   spec.Env,
		Cmd:   spec.Cmd,
		ExposedPorts: nat.PortSet{
			port: struct{}{},
		},
	}
	if len(spec.HealthTest) > 0 {
		cfg.Healthcheck = &container.HealthConfig{
			Test:     spec.HealthTest,
			Interval: 5 * time.Second,
			Timeout:  5 * time.Second,
			Retries:  20,
		}
	}

	resp, err := o.docker.ContainerCreate(ctx, cfg, &container.HostConfig{
		PortBindings: nat.PortMap{
			port: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: spec.HostPort}},
		},
		Mounts:        mounts,
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
		// Stop apps first, then infra.
		for i := len(managedServices) - 1; i >= 0; i-- {
			spec := serviceSpecs[managedServices[i]]
			_ = o.stopContainer(ctx, spec)
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
		if err := o.stopContainer(ctx, spec); err != nil {
			// Absent container is fine for Stop.
			if !strings.Contains(err.Error(), "No such container") {
				return "", err
			}
		}
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

func (o *orchestrator) inspectNamed(ctx context.Context, name, containerName string) composeServiceStatus {
	c, err := o.docker.ContainerInspect(ctx, containerName)
	if err != nil {
		return composeServiceStatus{Name: name, State: "absent", Status: "not found"}
	}
	state := "unknown"
	health := ""
	if c.State != nil {
		state = c.State.Status
		if c.State.Health != nil {
			health = c.State.Health.Status
		}
	}
	return composeServiceStatus{Name: name, State: state, Status: state, Health: health}
}

func (o *orchestrator) status(ctx context.Context) ([]composeServiceStatus, string, error) {
	var out []composeServiceStatus
	for _, name := range managedServices {
		spec := serviceSpecs[name]
		out = append(out, o.inspectNamed(ctx, name, spec.ContainerName))
	}
	out = append(out, o.inspectNamed(ctx, "console-api", "st-console-api"))
	raw, _ := json.MarshalIndent(out, "", "  ")
	return out, string(raw), nil
}

// infraHealth maps service id → healthy|down for UI probes (MySQL/Redis have no HTTP /healthz).
func (o *orchestrator) infraHealth(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, name := range managedServices {
		spec := serviceSpecs[name]
		if !spec.isInfra() {
			continue
		}
		st := o.inspectNamed(ctx, name, spec.ContainerName)
		if st.State == "running" && (st.Health == "" || st.Health == "healthy") {
			out[name] = "healthy"
		} else if st.State == "running" && st.Health == "starting" {
			out[name] = "checking"
		} else {
			out[name] = "down"
		}
	}
	return out
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
