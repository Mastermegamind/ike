# Contributing

Thanks for helping build HostPanel!

## Getting started

1. Fork and clone the repository.
2. Install Go 1.22+ and ShellCheck.
3. Run the checks:
   ```bash
   cd agent && go vet ./... && go test ./...
   shellcheck installer/install.sh
   ```
4. Test real installs on disposable Ubuntu 22.04, 24.04 and 26.04 VPSes (or Multipass/LXD VMs). Never test on a server with real sites.

## Pull requests

- One feature or fix per PR, with tests where possible.
- Follow the design rules in [SECURITY.md](SECURITY.md). PRs that pass user input to a shell will not be merged.
- If you change the installer, say which Ubuntu versions you tested on.
- Use clear commit messages: `agent: add database creation action`, `installer: support arm64`.

## Code style

- Go: `gofmt`, small packages, return errors instead of panicking.
- Bash: `set -euo pipefail`, quote every variable, keep ShellCheck clean.

## Code of conduct

Be respectful and constructive. Harassment of any kind is not tolerated.
