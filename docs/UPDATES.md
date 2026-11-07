# Updates

Ike keeps two things up to date: **Ike itself** and **Ubuntu's packages**. Nothing installs without an admin's approval unless you choose otherwise, and every update has a way back.

## Ike

Every server checks GitHub for a new Ike release every six hours. A new version is announced once: in the panel's notification centre and on the admins' alert channels (Telegram, WhatsApp, email). The **Updates** page shows its release notes.

| Setting | What happens |
|---|---|
| **Ask me** (default) | Nothing installs until an admin presses **Update** (or runs `ike update install`) |
| **Install by itself** | New versions install within a few hours of release |
| **Don't check** | Ike doesn't look; **Check now** still works |

**Skip this version** hides one release; newer ones are offered again.

### What an update does

1. Downloads `ike-agent`, `ike-panel`, `ike-mcp` and `ike` for this server's processor and checks each against its published SHA-256. A bad checksum stops everything.
2. Runs the new agent and panel once to make sure they start on this server and report the right version.
3. Keeps a copy of the version now installed in `/var/lib/ike/releases/<version>/` (the three newest are kept).
4. Swaps the new programs in and restarts Ike. Websites, mail and databases keep running; the panel reconnects in a few seconds.
5. Ninety seconds later, **the previous version** checks the new one: both services running and the agent answering with the new version number. If not, it puts itself back and restarts, and admins are told why. An update can't leave a server without its panel.

### Going back

**Go back to 0.6.0** on the Updates page (or `ike update rollback 0.6.0`) reinstalls a kept version the same way, with the same check. Ike's data files only ever gain fields and older versions ignore fields they don't know, so going back needs no data changes.

### Many servers

On the panel that manages other servers, **Ike on your servers** lists each server's version. Each updates itself with its own check and way back. Update one first, then the rest.

## Ubuntu packages

Ike refreshes the package lists twice a day and announces waiting updates (security fixes separately). Ubuntu's own daily security updates can stay on under **Automatic updates**; everything else waits for an admin.

Every run you approve is recorded with each package's version before and after. **Roll back** on a run reinstalls the earlier versions that apt can still get (from the Ubuntu archive or its download cache) and says which it couldn't. Rolling back undoes that run's security fixes too, so use it when an update broke something, then update again once it's fixed.

## A new Ubuntu release

When Ubuntu offers a newer LTS (24.04 to 26.04, say) and Ike supports it, admins are told. Moving needs every check to pass: Ike supports the release, 5 GB free, no restart waiting, all package updates installed. It also needs you to confirm you have a snapshot at your provider, because a release upgrade can't be rolled back any other way. Ike then runs Ubuntu's upgrader without questions (your config files are kept), turns its PHP, Nginx and other package sources back on for the new release, and asks you to restart.

For a big jump, moving to a freshly installed server is often calmer: install the new release on a new VPS, join it to the panel, and restore the accounts there from backups.

## From the command line and AI agents

```sh
ike update                      # version, newest release, kept versions, history
ike update install --wait
ike update rollback 0.6.0 --wait
ike update mode auto            # or notify, off
ike os                          # waiting package updates
ike os install --security --wait
ike os history
ike os rollback RUN_ID --wait
ike os release                  # a newer Ubuntu, and whether you're ready
```

MCP tools: `get_ike_versions`, `update_ike`, `rollback_ike`, `check_updates`, `install_updates`, `get_package_update_history`, `rollback_package_update`, `check_ubuntu_release`. Moving to a new Ubuntu release is deliberately not an AI tool.
