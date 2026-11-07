package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

const composeJSON = `{
  "name": "ike-dock-shop",
  "services": {
    "web": {
      "image": "nginx:1.27",
      "ports": [{"mode":"ingress","target":80,"published":"8080","protocol":"tcp"}],
      "volumes": [{"type":"bind","source":"/home/dock/stacks/shop/html","target":"/usr/share/nginx/html","read_only":true},
                  {"type":"volume","source":"data","target":"/data"}]
    },
    "worker": {"build": {"context": "/home/dock/stacks/shop/app", "dockerfile": "Dockerfile"}}
  },
  "volumes": {"data": {"name": "ike-dock-shop_data"}}
}`

func dockerManager(t *testing.T, forAccounts bool) (*Manager, string) {
	m, _, root := newTestManager(t, func(c *config.Config) { c.DockerForAccounts = forAccounts })
	_ = os.MkdirAll(filepath.Join(root, "var/run"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "var/run/docker.sock"), nil, 0o600)
	mustAccount(t, m, "dock")
	return m, root
}

func TestComposePolicy(t *testing.T) {
	m, _ := dockerManager(t, true)
	out, err := m.checkCompose("dock", []byte(composeJSON), false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	has(t, "injected", s, `"host_ip": "127.0.0.1"`, `"cgroup_parent": "ike-acct-dock.slice"`, `"ike.account": "dock"`,
		`"no-new-privileges:true"`, `"pids_limit": 1024`)

	bad := map[string]string{
		"privileged":   `{"services":{"a":{"image":"x","privileged":true}}}`,
		"cap_add":      `{"services":{"a":{"image":"x","cap_add":["SYS_ADMIN"]}}}`,
		"devices":      `{"services":{"a":{"image":"x","devices":["/dev/sda:/dev/sda"]}}}`,
		"host network": `{"services":{"a":{"image":"x","network_mode":"host"}}}`,
		"host pid":     `{"services":{"a":{"image":"x","pid":"host"}}}`,
		"other's ctr":  `{"services":{"a":{"image":"x","network_mode":"container:ike-other-c-db"}}}`,
		"bind /etc":    `{"services":{"a":{"image":"x","volumes":[{"type":"bind","source":"/etc","target":"/e"}]}}}`,
		"bind socket":  `{"services":{"a":{"image":"x","volumes":[{"type":"bind","source":"/var/run/docker.sock","target":"/s"}]}}}`,
		"other home":   `{"services":{"a":{"image":"x","volumes":[{"type":"bind","source":"/home/other","target":"/o"}]}}}`,
		"sneaky home":  `{"services":{"a":{"image":"x","volumes":[{"type":"bind","source":"/home/dockx/data","target":"/o"}]}}}`,
		"unconfined":   `{"services":{"a":{"image":"x","security_opt":["seccomp=unconfined"]}}}`,
		"low port":     `{"services":{"a":{"image":"x","ports":[{"target":80,"published":"80"}]}}}`,
		"public ip":    `{"services":{"a":{"image":"x","ports":[{"host_ip":"203.0.113.5","target":80,"published":"8080"}]}}}`,
		"build ctx":    `{"services":{"a":{"build":{"context":"/root"}}}}`,
		"ext network":  `{"services":{"a":{"image":"x"}},"networks":{"n":{"external":true,"name":"ike-other"}}}`,
		"ext volume":   `{"services":{"a":{"image":"x"}},"volumes":{"v":{"external":true,"name":"pgdata"}}}`,
		"host driver":  `{"services":{"a":{"image":"x"}},"networks":{"n":{"driver":"macvlan"}}}`,
		"empty":        `{"services":{}}`,
	}
	for name, doc := range bad {
		if _, err := m.checkCompose("dock", []byte(doc), false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Admins may publish publicly; the rest of the policy still applies.
	if _, err := m.checkCompose("dock", []byte(`{"services":{"a":{"image":"x","ports":[{"target":80,"published":"8080"}]}}}`), true); err != nil {
		t.Errorf("admin public port: %v", err)
	}
	if _, err := m.checkCompose("dock", []byte(bad["privileged"]), true); err == nil {
		t.Error("admin privileged accepted")
	}
}

func TestAccountContainers(t *testing.T) {
	m, root := dockerManager(t, false)
	r := m.run.(interface{ History() []string })
	spec := ContainerSpec{Name: "redis", Image: "redis:7.4", Ports: []PortMap{{ContainerPort: 6379, HostPort: 16379}},
		Volumes: []VolumeMount{{Volume: "redis-data", Target: "/data"}}, MemoryMB: 256, CPUs: 0.5,
		Env: []EnvVar{{Key: "REDIS_ARGS", Value: "--save 60 1"}}, Health: &ContainerHealth{Command: "redis-cli ping"}}
	if _, err := m.CreateContainer(ctx, "dock", spec, false); err == nil {
		t.Error("accounts can't run containers unless the admin allows it")
	}
	m.cfg.DockerForAccounts = true
	// "container inspect" must fail for a new name; the dry run succeeds, so make it fail.
	dr := m.run.(*dryRunner)
	dr.FailOn = "docker container inspect ike-dock-c-redis"
	if _, err := m.CreateContainer(ctx, "dock", spec, false); err != nil {
		t.Fatal(err)
	}
	hist := strings.Join(r.History(), "\n")
	has(t, "run", hist, "docker network inspect ike-dock",
		"docker run -d --name ike-dock-c-redis --restart unless-stopped --network ike-dock --label ike.account=dock",
		"--cgroup-parent ike-acct-dock.slice --security-opt no-new-privileges", "--cpus 0.50 --memory 256m",
		"-p 127.0.0.1:16379:6379/tcp", "--mount type=volume,source=ike-dock-redis-data,target=/data",
		"--health-cmd redis-cli ping", "--env-file /var/lib/ike/deploy-tmp/", "redis:7.4")
	if strings.Contains(hist, "--save 60 1") {
		t.Error("environment values belong in the env file, not arguments")
	}

	for name, bad := range map[string]ContainerSpec{
		"public port": {Name: "p", Image: "nginx", Ports: []PortMap{{ContainerPort: 80, HostPort: 8080, Public: true}}},
		"low port":    {Name: "p", Image: "nginx", Ports: []PortMap{{ContainerPort: 80, HostPort: 80}}},
		"bad image":   {Name: "p", Image: "nginx;id"},
		"bad name":    {Name: "../x", Image: "nginx"},
		"root mount":  {Name: "p", Image: "nginx", Volumes: []VolumeMount{{HostPath: "../../etc", Target: "/e"}}},
		"no target":   {Name: "p", Image: "nginx", Volumes: []VolumeMount{{Volume: "v", Target: "/"}}},
		"both":        {Name: "p", Image: "nginx", Volumes: []VolumeMount{{Volume: "v", HostPath: "x", Target: "/x"}}},
		"newline env": {Name: "p", Image: "nginx", Env: []EnvVar{{Key: "A", Value: "1\nB=2"}}},
		"restart":     {Name: "p", Image: "nginx", Restart: "sometimes"},
	} {
		dr.FailOn = "docker container inspect ike-dock-c-p"
		if _, err := m.CreateContainer(ctx, "dock", bad, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A home folder mount must exist and not be a link.
	home := filepath.Join(root, "home/dock")
	_ = os.MkdirAll(filepath.Join(home, "site"), 0o750)
	_ = os.Symlink("/etc", filepath.Join(home, "sneaky"))
	dr.FailOn = "docker container inspect ike-dock-c-web"
	if _, err := m.CreateContainer(ctx, "dock", ContainerSpec{Name: "web", Image: "nginx:1.27",
		Volumes: []VolumeMount{{HostPath: "sneaky", Target: "/x"}}}, false); err == nil {
		t.Error("mounted a symlink")
	}
	if _, err := m.CreateContainer(ctx, "dock", ContainerSpec{Name: "web", Image: "nginx:1.27",
		Volumes: []VolumeMount{{HostPath: "site", Target: "/usr/share/nginx/html", ReadOnly: true}}}, false); err != nil {
		t.Fatal(err)
	}
	has(t, "bind", strings.Join(r.History(), "\n"), "--mount type=bind,source=/home/dock/site,target=/usr/share/nginx/html,readonly", "--user 0:0")
	if err := m.ContainerAction(ctx, "dock", "redis", "explode"); err == nil {
		t.Error("unknown action accepted")
	}
}

func TestKubeAdmin(t *testing.T) {
	m, _, root := newTestManager(t)
	if st := m.KubeOverview(ctx); st.Installed {
		t.Error("k3s reported without the binary")
	}
	_ = os.MkdirAll(filepath.Join(root, "usr/local/bin"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "usr/local/bin/k3s"), nil, 0o755)
	dr := m.run.(*dryRunner)
	dr.Responses = map[string]string{
		"/usr/local/bin/k3s kubectl get nodes":      `{"items":[{"metadata":{"name":"vps-lagos-1","labels":{"node-role.kubernetes.io/control-plane":"true"}},"status":{"conditions":[{"type":"Ready","status":"True"}],"nodeInfo":{"kubeletVersion":"v1.33.4+k3s1"}}}]}`,
		"/usr/local/bin/k3s kubectl get namespaces": `{"items":[{"metadata":{"name":"default"}},{"metadata":{"name":"kube-system"}}]}`,
	}
	st := m.KubeOverview(ctx)
	if !st.Installed || len(st.Nodes) != 1 || !st.Nodes[0].Ready || st.Nodes[0].Roles != "control-plane" || len(st.Namespaces) != 2 {
		t.Errorf("%+v", st)
	}
	if err := m.KubeScale(ctx, "deployment", "default", "web", 3); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][3]string{{"node", "default", "x"}, {"deployment", "Default", "x"}, {"deployment", "default", "--all"}} {
		if err := m.KubeDelete(ctx, bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := m.KubeApply(ctx, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n"); err != nil {
		t.Fatal(err)
	}
	has(t, "kubectl", strings.Join(dr.History(), "\n"), "k3s kubectl scale deployment/web -n default --replicas=3", "k3s kubectl apply -f - <stdin>")
}

type dryRunner = runner.DryRun
