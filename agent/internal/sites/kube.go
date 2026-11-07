package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// Optional Kubernetes through k3s, for server admins. Ike reads cluster state
// and applies manifests with kubectl; websites reach services through the
// proxy runtime (NodePort on 127.0.0.1).

// KubeStatus summarizes the cluster.
type KubeStatus struct {
	Installed  bool       `json:"installed"`
	Nodes      []KubeNode `json:"nodes"`
	Namespaces []string   `json:"namespaces"`
	Error      string     `json:"error,omitempty"`
}

// KubeNode is one node.
type KubeNode struct {
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Version string `json:"version"`
	Roles   string `json:"roles"`
}

// KubeWorkload is a deployment, stateful set or daemon set.
type KubeWorkload struct {
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Ready     int      `json:"ready"`
	Desired   int      `json:"desired"`
	Images    []string `json:"images"`
}

// KubePod is one pod.
type KubePod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Restarts  int    `json:"restarts"`
	Node      string `json:"node"`
}

// KubeService is one service with its node ports.
type KubeService struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Ports     []string `json:"ports"`
}

var kubeNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
var kubeKinds = map[string]bool{"deployment": true, "statefulset": true, "daemonset": true, "service": true, "pod": true,
	"ingress": true, "configmap": true, "secret": true, "job": true, "cronjob": true, "persistentvolumeclaim": true}

func (m *Manager) kubectl(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	if _, err := os.Stat(m.p(m.cfg.K3sPath)); err != nil {
		return nil, fmt.Errorf("Kubernetes (k3s) isn't installed. Install it with: install.sh --k3s")
	}
	return m.run.Exec(ctx, runner.Command{Name: m.cfg.K3sPath, Args: append([]string{"kubectl"}, args...), Stdin: stdin,
		Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/root", "KUBECONFIG=/etc/rancher/k3s/k3s.yaml"}, Timeout: 2 * time.Minute})
}

type kubeList struct {
	Items []json.RawMessage `json:"items"`
}

func (m *Manager) kubeItems(ctx context.Context, args ...string) ([]json.RawMessage, error) {
	out, err := m.kubectl(ctx, "", append(args, "-o", "json")...)
	if err != nil {
		return nil, err
	}
	var l kubeList
	if err := json.Unmarshal(out, &l); err != nil {
		return nil, fmt.Errorf("unexpected kubectl output")
	}
	return l.Items, nil
}

// KubeOverview returns nodes and namespaces.
func (m *Manager) KubeOverview(ctx context.Context) KubeStatus {
	st := KubeStatus{Nodes: []KubeNode{}, Namespaces: []string{}}
	if _, err := os.Stat(m.p(m.cfg.K3sPath)); err != nil {
		return st
	}
	st.Installed = true
	nodes, err := m.kubeItems(ctx, "get", "nodes")
	if err != nil {
		st.Error = err.Error()
		return st
	}
	for _, raw := range nodes {
		var n struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct{ Type, Status string } `json:"conditions"`
				NodeInfo   struct {
					KubeletVersion string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		}
		if json.Unmarshal(raw, &n) != nil {
			continue
		}
		node := KubeNode{Name: n.Metadata.Name, Version: n.Status.NodeInfo.KubeletVersion}
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" {
				node.Ready = c.Status == "True"
			}
		}
		roles := []string{}
		for k := range n.Metadata.Labels {
			if r, ok := strings.CutPrefix(k, "node-role.kubernetes.io/"); ok {
				roles = append(roles, r)
			}
		}
		node.Roles = strings.Join(roles, ",")
		st.Nodes = append(st.Nodes, node)
	}
	if ns, err := m.kubeItems(ctx, "get", "namespaces"); err == nil {
		for _, raw := range ns {
			var n struct {
				Metadata struct{ Name string } `json:"metadata"`
			}
			if json.Unmarshal(raw, &n) == nil {
				st.Namespaces = append(st.Namespaces, n.Metadata.Name)
			}
		}
	}
	return st
}

// KubeWorkloads lists workloads, pods and services in every namespace.
func (m *Manager) KubeWorkloads(ctx context.Context) (map[string]any, error) {
	work := []KubeWorkload{}
	items, err := m.kubeItems(ctx, "get", "deployments,statefulsets,daemonsets", "-A")
	if err != nil {
		return nil, err
	}
	for _, raw := range items {
		var w struct {
			Kind     string `json:"kind"`
			Metadata struct{ Name, Namespace string }
			Spec     struct {
				Replicas *int `json:"replicas"`
				Template struct {
					Spec struct {
						Containers []struct{ Image string } `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas          int `json:"readyReplicas"`
				NumberReady            int `json:"numberReady"`
				DesiredNumberScheduled int `json:"desiredNumberScheduled"`
			} `json:"status"`
		}
		if json.Unmarshal(raw, &w) != nil {
			continue
		}
		k := KubeWorkload{Kind: w.Kind, Namespace: w.Metadata.Namespace, Name: w.Metadata.Name, Ready: w.Status.ReadyReplicas, Images: []string{}}
		if w.Spec.Replicas != nil {
			k.Desired = *w.Spec.Replicas
		}
		if w.Kind == "DaemonSet" {
			k.Ready, k.Desired = w.Status.NumberReady, w.Status.DesiredNumberScheduled
		}
		for _, c := range w.Spec.Template.Spec.Containers {
			k.Images = append(k.Images, c.Image)
		}
		work = append(work, k)
	}
	pods := []KubePod{}
	if items, err := m.kubeItems(ctx, "get", "pods", "-A"); err == nil {
		for _, raw := range items {
			var p struct {
				Metadata struct{ Name, Namespace string }
				Spec     struct {
					NodeName string `json:"nodeName"`
				} `json:"spec"`
				Status struct {
					Phase             string `json:"phase"`
					ContainerStatuses []struct {
						RestartCount int `json:"restartCount"`
					} `json:"containerStatuses"`
				} `json:"status"`
			}
			if json.Unmarshal(raw, &p) != nil {
				continue
			}
			kp := KubePod{Namespace: p.Metadata.Namespace, Name: p.Metadata.Name, Phase: p.Status.Phase, Node: p.Spec.NodeName}
			for _, c := range p.Status.ContainerStatuses {
				kp.Restarts += c.RestartCount
			}
			pods = append(pods, kp)
		}
	}
	svcs := []KubeService{}
	if items, err := m.kubeItems(ctx, "get", "services", "-A"); err == nil {
		for _, raw := range items {
			var s struct {
				Metadata struct{ Name, Namespace string }
				Spec     struct {
					Type  string `json:"type"`
					Ports []struct {
						Port     int    `json:"port"`
						NodePort int    `json:"nodePort"`
						Protocol string `json:"protocol"`
					} `json:"ports"`
				} `json:"spec"`
			}
			if json.Unmarshal(raw, &s) != nil {
				continue
			}
			ks := KubeService{Namespace: s.Metadata.Namespace, Name: s.Metadata.Name, Type: s.Spec.Type, Ports: []string{}}
			for _, p := range s.Spec.Ports {
				d := strconv.Itoa(p.Port) + "/" + p.Protocol
				if p.NodePort != 0 {
					d += " → node " + strconv.Itoa(p.NodePort)
				}
				ks.Ports = append(ks.Ports, d)
			}
			svcs = append(svcs, ks)
		}
	}
	return map[string]any{"workloads": work, "pods": pods, "services": svcs}, nil
}

// KubeApply applies a YAML or JSON manifest.
func (m *Manager) KubeApply(ctx context.Context, manifest string) (string, error) {
	if strings.TrimSpace(manifest) == "" || len(manifest) > 512<<10 {
		return "", fmt.Errorf("paste a manifest (up to 512 KB)")
	}
	out, err := m.kubectl(ctx, manifest, "apply", "-f", "-")
	return strings.TrimSpace(string(out)), err
}

func checkKube(kind, ns, name string) error {
	if !kubeKinds[kind] {
		return fmt.Errorf("unsupported kind %q", kind)
	}
	if !kubeNameRe.MatchString(ns) || !kubeNameRe.MatchString(name) {
		return fmt.Errorf("invalid namespace or name")
	}
	return nil
}

// KubeDelete deletes one resource.
func (m *Manager) KubeDelete(ctx context.Context, kind, ns, name string) error {
	if err := checkKube(kind, ns, name); err != nil {
		return err
	}
	_, err := m.kubectl(ctx, "", "delete", kind, name, "-n", ns, "--wait=false")
	return err
}

// KubeScale changes a deployment's or stateful set's replica count.
func (m *Manager) KubeScale(ctx context.Context, kind, ns, name string, replicas int) error {
	if err := checkKube(kind, ns, name); err != nil {
		return err
	}
	if kind != "deployment" && kind != "statefulset" {
		return fmt.Errorf("only deployments and stateful sets can be scaled")
	}
	if replicas < 0 || replicas > 100 {
		return fmt.Errorf("replicas must be 0-100")
	}
	_, err := m.kubectl(ctx, "", "scale", kind+"/"+name, "-n", ns, "--replicas="+strconv.Itoa(replicas))
	return err
}

// KubeRestart rolls a workload's pods.
func (m *Manager) KubeRestart(ctx context.Context, kind, ns, name string) error {
	if err := checkKube(kind, ns, name); err != nil {
		return err
	}
	_, err := m.kubectl(ctx, "", "rollout", "restart", kind+"/"+name, "-n", ns)
	return err
}

// KubeLogs returns a pod's recent output.
func (m *Manager) KubeLogs(ctx context.Context, ns, pod string, lines int) (string, error) {
	if !kubeNameRe.MatchString(ns) || !kubeNameRe.MatchString(pod) {
		return "", fmt.Errorf("invalid namespace or pod")
	}
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	out, err := m.kubectl(ctx, "", "logs", pod, "-n", ns, "--tail="+strconv.Itoa(lines), "--all-containers=true", "--timestamps")
	return string(out), err
}
