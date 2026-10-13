# The one-line installer

Anyone can set up Ike on a fresh Ubuntu 22.04, 24.04 or 26.04 server with:

```bash
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash
```

With no options, the guided wizard asks which web server, PHP versions, Node.js, Python and database to install. To skip it, pass options after `bash -s --`:

```bash
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash -s -- \
  --web hybrid --php all --default-php 8.3 --node "20 22" --python "3.12" --phpmyadmin 6
```

Every option is listed by `install.sh --help`.

## What it does

`installer.sh` (in `installer/`) is a short bootstrap script. It:

1. Checks it's running as root on a supported Ubuntu release and CPU (x86_64 or arm64).
2. Downloads `install.sh` and `install.sh.sha256` from the GitHub release, over HTTPS only.
3. Refuses to continue if the checksum doesn't match.
4. Runs `install.sh`, giving it the terminal so the wizard works even when piped from curl.

The whole script sits inside a function that runs on its last line, so a download cut off halfway does nothing.

| Variable | Default | Use |
|---|---|---|
| `IKE_REPO` | `YOUR-ORG/ike` | Install from a fork |
| `IKE_VERSION` | `latest` | Pin a release, e.g. `v0.3.0` |

```bash
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo IKE_VERSION=v0.3.0 bash
```

## Publishing it on ike.megamindtechnologies.com

Each tagged release (`git tag v0.3.0 && git push --tags`) attaches `installer.sh`, `install.sh` and their `.sha256` files to the GitHub release. The domain then only has to point at them.

1. **DNS.** Add an `A` record for `ike` in the `megamindtechnologies.com` zone, pointing at the server that will answer for it. Any small server works, including one running Ike.
2. **Web server.** Copy `deploy/ike.megamindtechnologies.com.nginx.conf` to `/etc/nginx/conf.d/` and run `nginx -t && systemctl reload nginx`. It redirects `/installer.sh` to the latest release's copy, so you never have to re-upload after a release.
3. **HTTPS.** Run `certbot --nginx -d ike.megamindtechnologies.com`. The one-liner uses `https://`, and curl refuses an invalid certificate.
4. **Check it.**
   ```bash
   curl -fsSLI https://ike.megamindtechnologies.com/installer.sh   # 302, then 200 from GitHub
   curl -fsSL https://ike.megamindtechnologies.com/installer.sh | head -3
   ```

If you'd rather serve the file directly, use option B in the nginx file and upload `installer/installer.sh` to `/var/www/ike/installer.sh`. Upload it again after each release. Serve it as `text/plain` so browsers show it rather than download it.

On a server already running Ike, you can instead create a static website for `ike.megamindtechnologies.com` in the panel and upload `installer.sh` into its `public_html`.

## Before the first public release

- Replace `YOUR-ORG` with the GitHub owner in `installer/installer.sh`, `installer/install.sh`, `deploy/*.conf` and the Go module path (`agent/go.mod`).
- Push a tag so a release exists. Until then `releases/latest/download/...` returns 404 and the installer stops with a clear message.
