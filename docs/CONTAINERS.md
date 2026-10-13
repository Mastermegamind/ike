# Containers, Compose and Kubernetes

Install Docker with `install.sh --docker`. Add `--docker-for-accounts` to let account owners (not only admins) run containers, and `--buildpacks` for Git deploys without a Dockerfile.

## What accounts can and can't do

Account containers and Compose stacks run as the account:

- named `ike-<account>-…` and labelled with the account, in the account's own network;
- inside the account's CPU and memory slice (`--cgroup-parent`), with `no-new-privileges` and a process limit;
- with published ports bound to **127.0.0.1**, so they're reached through a website with the *Forward to a local port* runtime (which adds the domain and HTTPS);
- mounting only folders in the account's home, never symlinks; containers that mount the home run as the account's user.

Compose files are read by `docker compose config` running **as the account user**, so they can't pull in files the account can't read. The checked result must not use `privileged`, `cap_add`, devices, host networking, host PID/IPC, unconfined security options, the Docker socket, or other accounts' networks and volumes. Ike adds its labels and limits and starts the checked copy.

Server admins can publish container ports publicly. Docker manages its own firewall rules for published ports, so a public container port is reachable even if ufw doesn't list it.

## Image updates

Once a day Ike compares each running image tag with its registry (`docker buildx imagetools inspect`) and alerts you when a newer image exists. Update with **Update images** on a stack, or redeploy a container website.

## Kubernetes

`install.sh --k3s` installs single-node k3s with its own ingress and load balancer turned off: Nginx keeps ports 80 and 443. The Kubernetes page (admins) shows nodes, workloads, pods and services, applies manifests, scales and restarts workloads. Publish a NodePort service with a *Forward to a local port* website.
