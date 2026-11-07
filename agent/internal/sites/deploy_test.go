package sites

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

const sha1 = "1111111111111111111111111111111111111111"
const sha2 = "2222222222222222222222222222222222222222"

func waitDeploy(t *testing.T, m *Manager, domain, id string) Deployment {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, _, err := m.Deployment(domain, id)
		if err == nil && d.Status != "queued" && d.Status != "building" {
			return *d
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("deployment %s didn't finish", id)
	return Deployment{}
}

func gitResponses(r *runner.DryRun, root, mirror, sha string) {
	if r.Responses == nil {
		r.Responses = map[string]string{}
	}
	base := "git -c credential.helper= -c core.hooksPath=/dev/null -C " + mirror + " "
	r.Responses[base+"rev-parse"] = sha + "\n"
	r.Responses[base+"log -1"] = "Ada Obi\x1fFix the checkout page\n"
	r.Responses[base+"for-each-ref"] = "v2.1.0\nv2.0.0\nlatest\n"
	_ = os.MkdirAll(filepath.Join(root, mirror), 0o700)
	_ = os.WriteFile(filepath.Join(root, mirror, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600)
}

func TestGitDeployPHPWithRollbackAndWebhooks(t *testing.T) {
	drainDelay = 0
	m, r, root := newTestManager(t)
	mustAccount(t, m, "shop")
	if _, err := m.Create(ctx, CreateRequest{Domain: "shop.ng", Account: "shop"}); err != nil {
		t.Fatal(err)
	}
	site, _ := m.Get("shop.ng")
	if _, err := m.Deploy("shop.ng", DeployRequest{}); err == nil {
		t.Error("deployed without a repository")
	}

	setup := DeploySetup{Type: "native", Git: &GitSource{URL: "https://github.com/ada/shop.git", Ref: "main", Auth: "token", AutoDeploy: true},
		PublicDir: "public", SharedPaths: []string{".env", "storage"}, BuildCommand: "composer install --no-dev && php artisan view:cache",
		ReleaseCommand: "php artisan migrate --force", NotifyURL: "https://hooks.example.com/ike"}
	if _, err := m.ConfigureDeploy(ctx, "shop.ng", setup); err == nil {
		t.Error("token auth without a token")
	}
	setup.Token = "ghp_exampleTOKEN123456"
	info, err := m.ConfigureDeploy(ctx, "shop.ng", setup)
	if err != nil {
		t.Fatal(err)
	}
	if info.WebhookSecret == "" || !info.HasToken || info.Deploy.Git.Provider != "github" || len(info.Instructions) == 0 {
		t.Errorf("info %+v", info)
	}
	secret := info.WebhookSecret
	gitResponses(r, root, m.gitMirror(site), sha1)

	d, err := m.Deploy("shop.ng", DeployRequest{Actor: "marvellous"})
	if err != nil {
		t.Fatal(err)
	}
	first := waitDeploy(t, m, "shop.ng", d.ID)
	if first.Status != "live" || first.Commit != sha1 || first.Author != "Ada Obi" || first.Release == "" {
		t.Fatalf("first deploy %+v", first)
	}
	hist := strings.Join(r.History(), "\n")
	has(t, "commands", hist,
		"git -c credential.helper= -c core.hooksPath=/dev/null -C /var/lib/ike/git/shop-"+siteID("shop.ng")+".git fetch --prune --force origin",
		"archive --format=tar -o /var/lib/ike/deploy-tmp/",
		"runuser -u shop -- tar -xf /var/lib/ike/deploy-tmp/",
		"runuser -u shop -- ln -sfn /home/shop/web/shop.ng/.env /home/shop/web/shop.ng/releases/"+first.Release+"/.env",
		"runuser -u shop -- mkdir -p /home/shop/web/shop.ng/shared/storage",
		"systemd-run --quiet --wait --pipe --collect --uid=shop --gid=shop --working-directory=/home/shop/web/shop.ng/releases/"+first.Release,
		"--slice=ike-acct-shop.slice", "composer install --no-dev && php artisan view:cache", "php artisan migrate --force",
		"runuser -u shop -- ln -sfn releases/"+first.Release, "mv -Tf", "systemctl reload php8.3-fpm")
	// The token never appears in arguments.
	if strings.Contains(hist, "ghp_exampleTOKEN") {
		t.Error("token leaked into command arguments")
	}
	found := false
	for _, env := range r.Envs {
		if strings.Contains(strings.Join(env, " "), "IKE_GIT_TOKEN=ghp_exampleTOKEN123456") {
			found = true
		}
	}
	if !found {
		t.Error("token not passed in the environment")
	}
	vh := read(t, root, "etc/nginx/ike/sites/shop.ng.conf")
	has(t, "root", vh, "root /home/shop/web/shop.ng/current/public;", "$realpath_root$fastcgi_script_name")

	// Second deploy, then roll back to the first.
	gitResponses(r, root, m.gitMirror(site), sha2)
	time.Sleep(1100 * time.Millisecond) // release ids are per second
	d2, _ := m.Deploy("shop.ng", DeployRequest{})
	second := waitDeploy(t, m, "shop.ng", d2.ID)
	if second.Status != "live" || second.Release == first.Release {
		t.Fatalf("second %+v", second)
	}
	list, _ := m.Deployments("shop.ng")
	if list[1].Status != "replaced" {
		t.Errorf("first deployment should be replaced, got %s", list[1].Status)
	}
	// Release folders don't exist in a dry run; create the first so rollback can find it.
	_ = os.MkdirAll(filepath.Join(root, "home/shop/web/shop.ng/releases", first.Release), 0o750)
	rb, err := m.Rollback("shop.ng", first.ID, "marvellous")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitDeploy(t, m, "shop.ng", rb.ID); got.Status != "live" || got.Release != first.Release {
		t.Fatalf("rollback %+v", got)
	}
	if s, _ := m.Get("shop.ng"); s.Deploy.Current != first.Release {
		t.Errorf("current %s", s.Deploy.Current)
	}

	// GitHub push webhook.
	body := []byte(`{"ref":"refs/heads/main","after":"` + sha2 + `"}`)
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	h.Set("X-Hub-Signature-256", sign(secret, body))
	res, err := m.HandleGitWebhook(ctx, "shop.ng", h, body)
	if err != nil || res.Action != "deployed" {
		t.Fatalf("push: %v %+v", err, res)
	}
	waitDeploy(t, m, "shop.ng", res.Deployment.ID)
	h.Set("X-Hub-Signature-256", sign("wrong", body))
	if _, err := m.HandleGitWebhook(ctx, "shop.ng", h, body); err != ErrUnauthorized {
		t.Errorf("bad signature: %v", err)
	}
	other := []byte(`{"ref":"refs/heads/feature","after":"` + sha2 + `"}`)
	h.Set("X-Hub-Signature-256", sign(secret, other))
	if res, _ := m.HandleGitWebhook(ctx, "shop.ng", h, other); res.Action != "ignored" {
		t.Errorf("other branch: %+v", res)
	}
	// GitLab and Bitbucket formats.
	gl := []byte(`{"ref":"refs/heads/main","checkout_sha":"` + sha1 + `"}`)
	h = http.Header{}
	h.Set("X-Gitlab-Event", "Push Hook")
	h.Set("X-Gitlab-Token", secret)
	if res, err := m.HandleGitWebhook(ctx, "shop.ng", h, gl); err != nil || res.Action != "deployed" {
		t.Errorf("gitlab: %v %+v", err, res)
	} else {
		waitDeploy(t, m, "shop.ng", res.Deployment.ID)
	}
	bb := []byte(`{"push":{"changes":[{"new":{"type":"branch","name":"main","target":{"hash":"` + sha1 + `"}}}]}}`)
	h = http.Header{}
	h.Set("X-Event-Key", "repo:push")
	h.Set("X-Hub-Signature", sign(secret, bb))
	if res, err := m.HandleGitWebhook(ctx, "shop.ng", h, bb); err != nil || res.Action != "deployed" {
		t.Errorf("bitbucket: %v %+v", err, res)
	} else {
		waitDeploy(t, m, "shop.ng", res.Deployment.ID)
	}
}

func TestPreviewDeployments(t *testing.T) {
	drainDelay = 0
	m, r, root := newTestManager(t)
	mustAccount(t, m, "team")
	if _, err := m.Create(ctx, CreateRequest{Domain: "app.team.ng", Account: "team", Runtime: "static"}); err != nil {
		t.Fatal(err)
	}
	info, err := m.ConfigureDeploy(ctx, "app.team.ng", DeploySetup{Type: "native",
		Git:      &GitSource{URL: "git@github.com:team/app.git", Auth: "deploy_key"},
		Previews: &PreviewSettings{Enabled: true, BaseDomain: "preview.team.ng"}})
	if err != nil {
		t.Fatal(err)
	}
	has(t, "keygen", strings.Join(r.History(), "\n"), "ssh-keygen -q -t ed25519 -N  -C ike-deploy@app.team.ng")
	pr := func(action, headRepo string) *WebhookResult {
		body := []byte(`{"action":"` + action + `","number":3,"pull_request":{"head":{"sha":"` + sha2 + `","ref":"feature/cart","repo":{"full_name":"` + headRepo + `"}},"base":{"repo":{"full_name":"team/app"}}}}`)
		h := http.Header{}
		h.Set("X-GitHub-Event", "pull_request")
		h.Set("X-Hub-Signature-256", sign(info.WebhookSecret, body))
		res, err := m.HandleGitWebhook(ctx, "app.team.ng", h, body)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := pr("opened", "stranger/app"); res.Action != "ignored" {
		t.Errorf("fork PR built: %+v", res)
	}
	prev, _ := m.Get("app.team.ng")
	_ = prev
	// The preview has its own mirror; give it canned git output once it exists.
	res := pr("opened", "team/app")
	if res.Action != "preview" || res.Reason != "pr-3.preview.team.ng" {
		t.Fatalf("preview: %+v", res)
	}
	waitDeploy(t, m, "pr-3.preview.team.ng", res.Deployment.ID) // fails in dry run (no canned commit) but the site exists
	ps, err := m.Get("pr-3.preview.team.ng")
	if err != nil || ps.Deploy.PreviewOf != "app.team.ng" || !ps.Staging || ps.Deploy.Git.AutoDeploy {
		t.Fatalf("preview site %+v %v", ps, err)
	}
	if _, err := os.Stat(filepath.Join(root, m.gitSecrets(ps), "id_ed25519.pub")); err != nil {
		// ssh-keygen is a dry run, so there's no key to copy; the folder must exist.
		if _, err := os.Stat(filepath.Join(root, m.gitSecrets(ps))); err != nil {
			t.Error("preview secrets folder missing")
		}
	}
	if res := pr("closed", "team/app"); res.Action != "preview-removed" {
		t.Errorf("close: %+v", res)
	}
	if _, err := m.Get("pr-3.preview.team.ng"); err == nil {
		t.Error("preview not removed")
	}
}

func TestBlueGreenAppAndContainerDeploys(t *testing.T) {
	drainDelay = 0
	m, r, root := newTestManager(t)
	addRuntime(t, root, "node", "22", "node")
	mustAccount(t, m, "apps")
	if _, err := m.Create(ctx, CreateRequest{Domain: "api.apps.ng", Account: "apps", Runtime: "node"}); err != nil {
		t.Fatal(err)
	}
	site, _ := m.Get("api.apps.ng")
	if _, err := m.ConfigureDeploy(ctx, "api.apps.ng", DeploySetup{Type: "native", Strategy: "blue-green", HealthPath: "/health",
		Git: &GitSource{URL: "https://gitlab.com/apps/api.git", Ref: "production"}}); err != nil {
		t.Fatal(err)
	}
	site, _ = m.Get("api.apps.ng")
	if site.Deploy.AltPort == 0 || site.Deploy.AltPort == site.Port {
		t.Fatalf("alt port %d", site.Deploy.AltPort)
	}
	gitResponses(r, root, m.gitMirror(site), sha1)
	var probed []int
	m.healthy = func(_ context.Context, port int, host, path string) error { probed = append(probed, port); return nil }
	d, _ := m.Deploy("api.apps.ng", DeployRequest{})
	if got := waitDeploy(t, m, "api.apps.ng", d.ID); got.Status != "live" {
		t.Fatalf("%+v", got)
	}
	site, _ = m.Get("api.apps.ng")
	if site.Deploy.Color != "b" || len(probed) != 1 || probed[0] != site.Deploy.AltPort {
		t.Errorf("color %s probed %v", site.Deploy.Color, probed)
	}
	key := "apps-" + siteID("api.apps.ng")
	has(t, "b unit", read(t, root, "etc/systemd/system/ike-app-"+key+"-b.service"), "ExecStart=/bin/bash /etc/ike/apps/"+key+"-b.sh")
	has(t, "b script", read(t, root, "etc/ike/apps/"+key+"-b.sh"), "export PORT="+strconv.Itoa(site.Deploy.AltPort),
		"cd \"/home/apps/web/api.apps.ng/releases/"+site.Deploy.Current+"\"")
	has(t, "nginx", read(t, root, "etc/nginx/ike/sites/api.apps.ng.conf"), "proxy_pass http://127.0.0.1:"+strconv.Itoa(site.Deploy.AltPort)+";")
	has(t, "cmds", strings.Join(r.History(), "\n"), "systemctl restart ike-app-"+key+"-b", "systemctl disable --now ike-app-"+key)

	// A failing health check keeps traffic on the old instance.
	m.healthy = func(context.Context, int, string, string) error { return os.ErrDeadlineExceeded }
	time.Sleep(1100 * time.Millisecond)
	d, _ = m.Deploy("api.apps.ng", DeployRequest{})
	if got := waitDeploy(t, m, "api.apps.ng", d.ID); got.Status != "failed" || !strings.Contains(got.Error, "/health") {
		t.Errorf("unhealthy deploy %+v", got)
	}
	if s, _ := m.Get("api.apps.ng"); s.Deploy.Color != "b" {
		t.Error("traffic moved to an unhealthy release")
	}

	// Container website from a registry image.
	m.healthy = func(context.Context, int, string, string) error { return nil }
	_ = os.MkdirAll(filepath.Join(root, "var/run"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "var/run/docker.sock"), nil, 0o600)
	if _, err := m.Create(ctx, CreateRequest{Domain: "box.apps.ng", Account: "apps", Runtime: "docker"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []DeploySetup{{Type: "image", Image: "Nginx:Latest"}, {Type: "image", Image: "nginx; rm -rf /"}, {Type: "native"}} {
		if _, err := m.ConfigureDeploy(ctx, "box.apps.ng", bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if _, err := m.ConfigureDeploy(ctx, "box.apps.ng", DeploySetup{Type: "image", Image: "ghcr.io/apps/box:1.4", ContainerPort: 3000}); err != nil {
		t.Fatal(err)
	}
	d, _ = m.Deploy("box.apps.ng", DeployRequest{})
	got := waitDeploy(t, m, "box.apps.ng", d.ID)
	if got.Status != "live" {
		t.Fatalf("container deploy %+v", got)
	}
	box, _ := m.Get("box.apps.ng")
	ck := "apps-" + siteID("box.apps.ng")
	has(t, "docker", strings.Join(r.History(), "\n"),
		"docker pull ghcr.io/apps/box:1.4", "docker tag ghcr.io/apps/box:1.4 ike/"+ck+":"+got.Release,
		"docker run -d --name ike-"+ck+"-a --restart unless-stopped -p 127.0.0.1:"+strconv.Itoa(box.Port)+":3000",
		"--cgroup-parent ike-acct-apps.slice", "--security-opt no-new-privileges")
	has(t, "nginx", read(t, root, "etc/nginx/ike/sites/box.apps.ng.conf"), "proxy_pass http://127.0.0.1:"+strconv.Itoa(box.Port)+";")
}

func TestDeploySetupValidation(t *testing.T) {
	m, _, _ := newTestManager(t)
	mustAccount(t, m, "val")
	if _, err := m.Create(ctx, CreateRequest{Domain: "val.ng", Account: "val"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []DeploySetup{
		{Type: "native", Git: &GitSource{URL: "file:///etc"}},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git --upload-pack=evil"}},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git", Ref: "-x"}},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git", Ref: "a..b"}},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git", RefType: "commit", Ref: "main"}},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git", Auth: "deploy_key"}},
		{Type: "native", Git: &GitSource{URL: "git@github.com:a/b.git", Auth: "token"}, Token: "abcdefghij"},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git", Subdir: "../etc"}},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git"}, PublicDir: "/abs"},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git"}, SharedPaths: []string{"../../x"}},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git"}, BuildCommand: "make\nrm -rf ~"},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git"}, NotifyURL: "http://127.0.0.1:2087/"},
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git"}, Strategy: "blue-green"}, // PHP switches instantly
		{Type: "dockerfile", Git: &GitSource{URL: "https://github.com/a/b.git"}},                     // not a docker site
		{Type: "native", Git: &GitSource{URL: "https://github.com/a/b.git"}, Previews: &PreviewSettings{Enabled: true, BaseDomain: "val.ng"}},
	} {
		if _, err := m.ConfigureDeploy(ctx, "val.ng", bad); err == nil {
			t.Errorf("accepted %+v %+v", bad, bad.Git)
		}
	}
	if _, err := m.SetDeploySettings(DeploySettings{MaxConcurrent: 20}); err == nil {
		t.Error("accepted 20 concurrent builds")
	}
	if s, err := m.SetDeploySettings(DeploySettings{MaxConcurrent: 2}); err != nil || m.DeployQueue().Settings.MaxConcurrent != 2 {
		t.Errorf("%v %+v", err, s)
	}
	if !tagMatches("v*", "v2.1.0") || tagMatches("v*", "latest") {
		t.Error("tag patterns")
	}
}
