# Security Policy

A hosting panel runs as root and controls every site on a server, so security bugs here are serious.

## Reporting a vulnerability

**Do not open a public GitHub issue for security problems.**

Report privately through GitHub's "Report a vulnerability" button (Security tab → Advisories), or email the maintainers at security@YOUR-DOMAIN.

Please include:
- The affected version or commit
- Steps to reproduce
- The impact (e.g. user → root escalation, cross-account file access)

We aim to acknowledge reports within 72 hours and to release a fix for critical issues within 14 days.

## Design rules contributors must follow

1. **The agent never executes free-form commands.** Every action is a named, typed operation with validated inputs.
2. **Never build shell strings.** Use `exec.Command(name, args...)` with separate arguments, never `sh -c`.
3. **Validate every input** (domains, usernames, paths) against strict allow-lists before use.
4. **Never follow user-controlled symlinks** when running as root. Resolve paths and confirm they stay inside the expected directory.
5. **Each hosting account is a separate Linux user** with its own PHP-FPM pool; one account must never be able to read another's files.
6. **Secrets** (DB passwords, API tokens) are generated with `crypto/rand` and stored with `0600` permissions.
