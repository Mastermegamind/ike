package sites

import (
	"context"
	"crypto/hmac"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnauthorized means a webhook's signature didn't match.
var ErrUnauthorized = errors.New("signature doesn't match")

// WebhookResult says what a Git webhook caused.
type WebhookResult struct {
	Action     string      `json:"action"` // deployed | preview | preview-removed | ignored | pong
	Reason     string      `json:"reason,omitempty"`
	Deployment *Deployment `json:"deployment,omitempty"`
}

const maxPreviews = 10

func (m *Manager) webhookSecret(s *Site) string {
	b, _ := os.ReadFile(m.p(filepath.Join(m.gitSecrets(s), "webhook_secret")))
	return strings.TrimSpace(string(b))
}

func verifySig(secret, header string, body []byte) bool {
	return header != "" && hmac.Equal([]byte(header), []byte(sign(secret, body)))
}

// HandleGitWebhook verifies and acts on a push or pull request event from
// GitHub, GitLab, Bitbucket or a generic sender.
func (m *Manager) HandleGitWebhook(ctx context.Context, domain string, h http.Header, body []byte) (*WebhookResult, error) {
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.Deploy == nil || site.Deploy.Git == nil {
		return nil, fmt.Errorf("%s isn't connected to a repository", domain)
	}
	secret := m.webhookSecret(site)
	if secret == "" {
		return nil, ErrUnauthorized
	}
	var provider string
	switch {
	case h.Get("X-Hub-Signature-256") != "":
		provider = "github"
		if !verifySig(secret, h.Get("X-Hub-Signature-256"), body) {
			return nil, ErrUnauthorized
		}
	case h.Get("X-Gitlab-Token") != "":
		provider = "gitlab"
		if subtle.ConstantTimeCompare([]byte(h.Get("X-Gitlab-Token")), []byte(secret)) != 1 {
			return nil, ErrUnauthorized
		}
	case h.Get("X-Hub-Signature") != "" && h.Get("X-Event-Key") != "":
		provider = "bitbucket"
		if !verifySig(secret, h.Get("X-Hub-Signature"), body) {
			return nil, ErrUnauthorized
		}
	case h.Get("X-Ike-Signature") != "":
		provider = "generic"
		if !verifySig(secret, h.Get("X-Ike-Signature"), body) {
			return nil, ErrUnauthorized
		}
	default:
		return nil, ErrUnauthorized
	}
	ev, err := parseEvent(provider, h, body)
	if err != nil {
		return nil, err
	}
	switch ev.kind {
	case "ping":
		return &WebhookResult{Action: "pong"}, nil
	case "push":
		return m.onPush(site, ev)
	case "pr":
		return m.onPullRequest(ctx, site, ev)
	}
	return &WebhookResult{Action: "ignored", Reason: "event type isn't used"}, nil
}

type gitEvent struct {
	kind   string // ping | push | pr | other
	ref    string // refs/heads/main, refs/tags/v1
	commit string
	// pull/merge requests
	number  int
	action  string // open | update | close
	branch  string
	forkish bool // comes from another repository
	deleted bool
}

func parseEvent(provider string, h http.Header, body []byte) (gitEvent, error) {
	ev := gitEvent{kind: "other"}
	bad := func(err error) (gitEvent, error) { return ev, fmt.Errorf("unreadable webhook body: %w", err) }
	switch provider {
	case "github":
		switch h.Get("X-GitHub-Event") {
		case "ping":
			ev.kind = "ping"
		case "push":
			var p struct {
				Ref     string `json:"ref"`
				After   string `json:"after"`
				Deleted bool   `json:"deleted"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return bad(err)
			}
			ev.kind, ev.ref, ev.commit, ev.deleted = "push", p.Ref, p.After, p.Deleted
		case "pull_request":
			var p struct {
				Action      string `json:"action"`
				Number      int    `json:"number"`
				PullRequest struct {
					Head struct {
						Sha  string `json:"sha"`
						Ref  string `json:"ref"`
						Repo struct {
							FullName string `json:"full_name"`
						} `json:"repo"`
					} `json:"head"`
					Base struct {
						Repo struct {
							FullName string `json:"full_name"`
						} `json:"repo"`
					} `json:"base"`
				} `json:"pull_request"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return bad(err)
			}
			ev.kind, ev.number, ev.commit, ev.branch = "pr", p.Number, p.PullRequest.Head.Sha, p.PullRequest.Head.Ref
			ev.forkish = p.PullRequest.Head.Repo.FullName != p.PullRequest.Base.Repo.FullName
			ev.action = map[string]string{"opened": "open", "reopened": "open", "synchronize": "update", "closed": "close"}[p.Action]
		}
	case "gitlab":
		switch h.Get("X-Gitlab-Event") {
		case "Push Hook", "Tag Push Hook":
			var p struct {
				Ref         string `json:"ref"`
				CheckoutSha string `json:"checkout_sha"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return bad(err)
			}
			ev.kind, ev.ref, ev.commit, ev.deleted = "push", p.Ref, p.CheckoutSha, p.CheckoutSha == ""
		case "Merge Request Hook":
			var p struct {
				Attrs struct {
					IID          int    `json:"iid"`
					Action       string `json:"action"`
					SourceBranch string `json:"source_branch"`
					SourceID     int    `json:"source_project_id"`
					TargetID     int    `json:"target_project_id"`
					LastCommit   struct {
						ID string `json:"id"`
					} `json:"last_commit"`
				} `json:"object_attributes"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return bad(err)
			}
			a := p.Attrs
			ev.kind, ev.number, ev.commit, ev.branch, ev.forkish = "pr", a.IID, a.LastCommit.ID, a.SourceBranch, a.SourceID != a.TargetID
			ev.action = map[string]string{"open": "open", "reopen": "open", "update": "update", "close": "close", "merge": "close"}[a.Action]
		}
	case "bitbucket":
		key := h.Get("X-Event-Key")
		switch {
		case key == "repo:push":
			var p struct {
				Push struct {
					Changes []struct {
						New *struct {
							Type   string `json:"type"`
							Name   string `json:"name"`
							Target struct {
								Hash string `json:"hash"`
							} `json:"target"`
						} `json:"new"`
					} `json:"changes"`
				} `json:"push"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return bad(err)
			}
			ev.kind, ev.deleted = "push", true
			for _, c := range p.Push.Changes {
				if c.New != nil {
					prefix := "refs/heads/"
					if c.New.Type == "tag" {
						prefix = "refs/tags/"
					}
					ev.ref, ev.commit, ev.deleted = prefix+c.New.Name, c.New.Target.Hash, false
				}
			}
		case strings.HasPrefix(key, "pullrequest:"):
			var p struct {
				PR struct {
					ID     int `json:"id"`
					Source struct {
						Branch struct {
							Name string `json:"name"`
						} `json:"branch"`
						Commit struct {
							Hash string `json:"hash"`
						} `json:"commit"`
						Repository struct {
							FullName string `json:"full_name"`
						} `json:"repository"`
					} `json:"source"`
					Destination struct {
						Repository struct {
							FullName string `json:"full_name"`
						} `json:"repository"`
					} `json:"destination"`
				} `json:"pullrequest"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return bad(err)
			}
			ev.kind, ev.number, ev.commit, ev.branch = "pr", p.PR.ID, p.PR.Source.Commit.Hash, p.PR.Source.Branch.Name
			ev.forkish = p.PR.Source.Repository.FullName != p.PR.Destination.Repository.FullName
			ev.action = map[string]string{"pullrequest:created": "open", "pullrequest:updated": "update",
				"pullrequest:fulfilled": "close", "pullrequest:rejected": "close"}[key]
		}
	default:
		var p struct {
			Ref    string `json:"ref"`
			Commit string `json:"commit"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return bad(err)
		}
		ev.kind, ev.ref, ev.commit = "push", p.Ref, p.Commit
	}
	return ev, nil
}

func (m *Manager) onPush(site *Site, ev gitEvent) (*WebhookResult, error) {
	g := site.Deploy.Git
	if ev.deleted {
		return &WebhookResult{Action: "ignored", Reason: "a branch or tag was deleted"}, nil
	}
	matches := false
	switch g.RefType {
	case "branch":
		matches = ev.ref == "refs/heads/"+g.Ref
	case "tag":
		t, ok := strings.CutPrefix(ev.ref, "refs/tags/")
		matches = ok && tagMatches(g.Ref, t)
	}
	if !matches {
		return &WebhookResult{Action: "ignored", Reason: ev.ref + " isn't the deployed " + g.RefType}, nil
	}
	if !g.AutoDeploy {
		return &WebhookResult{Action: "ignored", Reason: "automatic deploys are off for this website"}, nil
	}
	req := DeployRequest{Trigger: "push", Actor: "git"}
	if commitRe.MatchString(ev.commit) {
		req.RefType, req.Ref = "commit", ev.commit
	}
	d, err := m.Deploy(site.Domain, req)
	if err != nil {
		return nil, err
	}
	return &WebhookResult{Action: "deployed", Deployment: d}, nil
}

func previewDomain(n int, base string) string { return fmt.Sprintf("pr-%d.%s", n, base) }

func (m *Manager) onPullRequest(ctx context.Context, site *Site, ev gitEvent) (*WebhookResult, error) {
	p := site.Deploy.Previews
	if p == nil || !p.Enabled {
		return &WebhookResult{Action: "ignored", Reason: "previews are off"}, nil
	}
	if ev.forkish {
		// Forks are other people's code; never build them on this server.
		return &WebhookResult{Action: "ignored", Reason: "previews aren't built for pull requests from forks"}, nil
	}
	if ev.number <= 0 || ev.action == "" {
		return &WebhookResult{Action: "ignored", Reason: "pull request event isn't used"}, nil
	}
	domain := previewDomain(ev.number, p.BaseDomain)
	if ev.action == "close" {
		if _, err := m.Get(domain); err != nil {
			return &WebhookResult{Action: "ignored", Reason: "no preview to remove"}, nil
		}
		if err := m.Delete(ctx, domain, true); err != nil {
			return nil, err
		}
		return &WebhookResult{Action: "preview-removed", Reason: domain}, nil
	}
	if !commitRe.MatchString(ev.commit) {
		return nil, fmt.Errorf("pull request event has no commit")
	}
	if _, err := m.Get(domain); err != nil {
		if err := m.createPreview(ctx, site, domain, ev.number); err != nil {
			return nil, err
		}
	}
	d, err := m.Deploy(domain, DeployRequest{Trigger: "preview", Actor: "git", RefType: "commit", Ref: ev.commit})
	if err != nil {
		return nil, err
	}
	return &WebhookResult{Action: "preview", Reason: domain, Deployment: d}, nil
}

func (m *Manager) createPreview(ctx context.Context, parent *Site, domain string, number int) error {
	sites, _ := m.List()
	n := 0
	for _, s := range sites {
		if s.Deploy != nil && s.Deploy.PreviewOf == parent.Domain {
			n++
		}
	}
	if n >= maxPreviews {
		return fmt.Errorf("%s already has %d previews; close some pull requests first", parent.Domain, maxPreviews)
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: domain, Account: parent.Account, Runtime: parent.Runtime,
		Version: parent.Version, WebServer: parent.WebServer, StartCommand: parent.StartCommand}); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return err
	}
	d := *parent.Deploy
	g := *parent.Deploy.Git
	g.AutoDeploy = false
	d.Git, d.Previews, d.NotifyURL, d.PreviewOf, d.PreviewNumber = &g, nil, "", parent.Domain, number
	d.Current, d.Color, d.Colors, d.AltPort = "", "", nil, 0
	site.Deploy, site.Staging = &d, true
	if d.Strategy == "blue-green" {
		if d.AltPort = m.nextPort(); d.AltPort == 0 {
			return fmt.Errorf("no free local ports left")
		}
	}
	// The preview fetches with the same credentials, but never gets the live
	// website's .env: pull requests shouldn't see production secrets.
	from, to := m.p(m.gitSecrets(parent)), m.p(m.gitSecrets(site))
	if err := os.MkdirAll(to, 0o700); err != nil {
		return err
	}
	for _, f := range []string{"id_ed25519", "id_ed25519.pub", "token"} {
		if b, err := os.ReadFile(filepath.Join(from, f)); err == nil {
			if err := writeFile(filepath.Join(to, f), b, 0o600); err != nil {
				return err
			}
		}
	}
	return m.writeJSON(m.layout(site).StateFile, site)
}
