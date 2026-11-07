# Deploying from Git

Any website can deploy from a Git repository (or, for container websites, a registry image). Open the website, then **Deploy**.

## How a deployment runs

1. **Fetch.** Ike keeps a bare mirror of the repository in `/var/lib/ike/git` (root only) and fetches the branch, tag or commit. Credentials travel in environment variables, never in command arguments. Repository hooks never run.
2. **Export.** The commit's files are unpacked, as the account user, into `~/web/<domain>/releases/<time>-<commit>/`.
3. **Shared paths.** Paths listed as shared (`.env`, `storage`, `public/uploads` …) are links into `~/web/<domain>/shared/`, so they survive releases. `.env` is the file the **Env vars** tab edits.
4. **Build.** The build command (or the detected one) runs as the account user in a throwaway systemd unit: inside the account's CPU and memory slice, the rest of the system read-only, 2 GB memory and a 30-minute limit.

   | Runtime | Detected build |
   |---|---|
   | PHP | `composer install --no-dev --optimize-autoloader`; `phive install` when `.phive/phars.xml` exists (trusted key ids go in `.phive/trusted-keys`); the JavaScript build below when package.json has a build script |
   | Static, Node.js | npm (`npm ci`), pnpm, Yarn classic or Berry, or Bun, picked by lockfile or package.json's `packageManager` and run at the version it pins; then the `build` script |
   | Python | a virtualenv in the release, then `uv sync` (uv.lock), `poetry install` (poetry.lock), `pipenv sync` (Pipfile.lock), `pip install -r requirements.txt` or `pip install .` |
   | Go | `go mod download`, `go build -o app .` |
   | Java | `./gradlew build -x test`, `./mvnw -DskipTests package`, `mvn -DskipTests package` or `gradle build -x test` |
   | Dart | `dart pub get` |
   | Container | `docker build`, or Cloud Native Buildpacks (`pack build`), or `docker pull` for images |

   `ike tools` (or **Server software** in the panel) shows which of these the server has.

5. **Release command** (optional) runs in the new release, e.g. `php artisan migrate --force`.
6. **Switch.**
   - PHP and static websites: the `current` link moves to the new release in one rename. Nginx serves `current/<public folder>`; PHP-FPM sees the real path, so no request mixes two releases.
   - Apps with **Restart**: the app restarts on the new release. If it doesn't answer on the health path, Ike goes back to the previous release.
   - Apps and containers with **Zero downtime** (blue-green): the new release starts on a second port. Traffic moves only after it answers the health path; the old instance stops ten seconds later.
7. **Clean up.** The newest releases (5 by default) are kept for rollbacks.

## JavaScript frameworks

Ike reads package.json and sets up the website for the framework, unless you set the public folder or start command yourself:

| Framework | Static website | Node.js website |
|---|---|---|
| Next.js | `out/` with `output: 'export'` | `npm start` (or `next start`) |
| Nuxt | `.output/public` with `nuxt generate` | `node .output/server/index.mjs` |
| SvelteKit | `build/` with adapter-static | `node build` (adapter-node) |
| Astro | `dist/` | `node ./dist/server/entry.mjs` with @astrojs/node |
| Remix / React Router | | `npm start` (or `remix-serve`) |
| NestJS, Express, Fastify, Koa, Hono | | `npm start`, or `node dist/main` for NestJS |
| React, Vue, Svelte, Solid, Preact on Vite | `dist/`, unknown paths get `index.html` | |
| Create React App | `build/`, unknown paths get `index.html` | |
| Angular | `dist/<project>/browser`, unknown paths get `index.html` | |
| Gatsby | `public/` | |

Apps get their port in `PORT`, which every framework above listens on. A website that isn't deployed from Git can turn on **Single-page app** (Overview tab) so client-side routes load `index.html`. The deploy log says what was detected, and warns when a framework with its own server is deployed to a static website.

PostgreSQL, MariaDB, MongoDB and Redis databases come from the **Databases** page; the connection details go in the **Env vars** tab (`DATABASE_URL` and the like), which every build and app reads.

Any failure leaves the live website untouched.

## Push to deploy

Add the webhook URL and secret shown on the Deploy tab to your Git host. Ike checks every call:

| Host | Signature |
|---|---|
| GitHub | `X-Hub-Signature-256` (HMAC-SHA256 of the body) |
| GitLab | `X-Gitlab-Token` (the secret) |
| Bitbucket Cloud | `X-Hub-Signature` (HMAC-SHA256) |
| Anything else | `X-Ike-Signature: sha256=<HMAC-SHA256 of the body>`, body `{"ref":"refs/heads/main","commit":"<sha>"}` |

A burst of pushes builds only the newest commit.

## Previews

With previews on, each pull/merge request from the same repository gets its own website at `pr-<number>.<preview domain>` (point a wildcard DNS record at the server). It is rebuilt on every push to the pull request and deleted when it closes. Previews don't receive the live website's `.env`, and pull requests from forks are never built.

## Notifications

Set a notification URL to receive `POST {"event":"deployment.finished","deployment":{…}}` after every deployment, signed with the webhook secret in `X-Ike-Signature`. Only public https addresses are allowed.

## API and AI agents

`POST /api/v1/sites/<domain>/deployments` with an optional `{"ref_type":"tag","ref":"v2.1.0"}` queues a deployment; the MCP tools `deploy_site`, `list_deployments`, `get_deployment` and `rollback_deployment` do the same for AI agents.
