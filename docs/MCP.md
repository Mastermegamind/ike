# AI agents (MCP)

Ike ships `ike-mcp`, a [Model Context Protocol](https://modelcontextprotocol.io) server. It lets AI clients such as Claude, Cursor or VS Code manage a server through the same checked API the panel uses. There is no shell tool: an AI can only do what the tools below allow, and only what its access allows.

## Two ways to connect

| | Over SSH, as root | Through the panel, with an API token |
|---|---|---|
| Command | `ssh root@server ike-mcp` | `ike-mcp --panel https://server:2087` with `IKE_TOKEN` set |
| Access | Every account (the agent socket) | Exactly what the token allows: its areas, accounts, addresses, read-only flag and expiry |
| Good for | You, managing your own server | Giving an AI, a teammate or a reseller part of a server |
| Panel tools (packages, billing, servers, clusters, notifications) | Not available | Available, if the token allows them |

Either way, **changes need `--allow-write`**, and `--tools` can switch off whole areas.

### Over SSH

Nothing extra to open on the firewall. The AI client runs `ike-mcp` on the server through SSH using your key:

```json
{
  "mcpServers": {
    "my-vps": { "command": "ssh", "args": ["root@203.0.113.10", "ike-mcp", "--tools", "sites,dns,mail"] }
  }
}
```

Give the AI its own SSH key and pin it to the MCP server in `/root/.ssh/authorized_keys`, so that key can never open a shell:

```
command="/usr/local/bin/ike-mcp --allow-write --tools sites,dns",restrict,from="203.0.113.0/24" ssh-ed25519 AAAA… ai-agent
```

### Through the panel, with scoped access

1. In the panel, open **Settings → API tokens** and create a token. Choose **Limited** and set each area to *none*, *read* or *write*. Optionally pick accounts, allowed addresses and an expiry date. Or from a terminal:

   ```sh
   ike tokens create "Claude for Adaeze" --username marvellous \
       --scope sites=write --scope dns=write --scope mail=read --account adaeze --days 30
   ```

2. Install `ike-mcp` where the AI client runs (the release has Linux, macOS and Windows builds) and add it to the client's config:

   ```json
   {
     "mcpServers": {
       "ike": {
         "command": "ike-mcp",
         "args": ["--panel", "https://server.example.com:2087", "--allow-write"],
         "env": { "IKE_TOKEN": "ike_…" }
       }
     }
   }
   ```

   Put the token in `env`, not in `args`: arguments can be seen by other users on the computer.

When it starts, `ike-mcp` asks the panel what the token may do and hides every tool outside that. With the token above, the AI sees website and DNS tools, read-only email tools, and nothing else. The panel still checks every call, so a hidden tool couldn't work even if a client called it.

`ike-mcp --panel … --list-tools` prints exactly what a token would get. Add `--insecure` only for a panel that still has its self-signed certificate.

## Tools

208 tools. Tools marked * need the panel connection.

| Area (`--tools` name) | Read | Change (need `--allow-write`) |
|---|---|---|
| Websites (`sites`) | `list_sites`, `get_site`, `list_cron_jobs`, `get_app_logs`, `get_site_log`, `diagnose_site`, `list_deployments`, `get_deployment`, `list_certificates`, `get_site_protection`, `get_account_realtime` | `install_wordpress`, `fix_permissions`, `create_staging`, `deploy_site`, `rollback_deployment`, `issue_certificate`, `set_redirects`, `set_domains`, `run_composer`, `create_site`, `update_site`, `set_site_status`, `app_action`, `add_cron_job`, `delete_cron_job`, `delete_site`, `set_protection_user`, `set_protected_paths`, `delete_protection_user`, `check_domain_expiry`, `set_laravel` |
| Accounts (`accounts`) | `list_accounts`, `list_ssh_keys`, `list_developers`* | `set_limits`, `create_account`, `set_ssh_access`, `add_ssh_key`, `remove_ssh_key`, `suspend_account`, `unsuspend_account`, `delete_account`, `invite_developer`*, `remove_developer`* |
| DNS (`dns`) | `list_dns_zones`, `get_dns_zone`, `check_dns`, `export_dns_zone`, `get_reverse_dns`, `check_domain_availability` | `create_dns_zone`, `add_dns_record`, `update_dns_record`, `delete_dns_record`, `apply_dns_template`, `import_dns_zone`, `delete_dns_zone`, `register_domain` |
| Email (`mail`) | `list_mail_domains`, `get_mail_domain`, `check_mail_deliverability`, `get_mail_dns_records`, `get_mail_queue`, `search_mail_log`, `get_mail_settings` | `enable_mail_domain`, `add_mail_dns_records`, `issue_mail_certificate`, `create_mailbox`, `update_mailbox`, `set_auto_reply`, `set_mail_forwarders`, `mail_queue_action`, `delete_mailbox` |
| Databases (`databases`) | `list_databases`, `list_all_databases`, `diagnose_database`, `list_db_processes`, `list_slow_queries`, `get_redis`, `get_replication`, `get_search` | `create_database`, `create_engine_database`, `reset_database_password`, `add_database_user`, `set_redis`, `kill_db_query`, `delete_engine_database`, `set_search` |
| Files (`files`) | `list_sftp_users`, `list_files`, `read_file`, `search_files`, `folder_sizes`, `list_file_snapshots`, `list_share_links`, `get_s3_storage` | `create_sftp_user`, `update_sftp_user`, `delete_sftp_user`, `write_file`, `make_folder`, `move_files`, `copy_files`, `set_file_mode`, `compress_files`, `extract_archive`, `delete_files`, `create_file_snapshot`, `restore_file_snapshot`, `create_share_link`, `delete_share_link`, `create_s3_bucket` |
| Backups (`backups`) | `list_backup_plans`, `list_backup_snapshots`, `browse_backup`, `list_storage` | `run_backup`, `check_backup`, `restore_backup` |
| Containers (`containers`) | `list_containers`, `list_stacks`, `kubernetes_status` | `container_action`, `stack_action` |
| Security (`security`) | `security_scan`, `get_firewall`, `list_bans`, `list_malware_scans`, `get_malware_overview`, `list_quarantine`, `get_waf_events`, `get_secrets_status` | `open_port`, `close_port`, `block_address`, `unblock_address`, `unban_ip`, `scan_for_malware`, `quarantine_findings`, `restore_quarantined_file`, `set_waf` |
| Packages and billing (`billing`) | `get_disk_quotas`, `list_expiry`*, `list_packages`*, `list_account_packages`*, `get_bandwidth`*, `list_subscriptions`*, `list_invoices`* | `set_hosting_expiry`*, `create_package`*, `assign_package`*, `create_subscription`*, `cancel_subscription`* |
| Server (`server`) | `get_system_info`, `get_service_status`, `get_audit_log`, `check_updates`, `get_usage`, `detect_drift`, `list_php_extensions`, `get_server_metrics`, `get_inventory`, `get_events`, `list_maintenance`, `list_webhooks`, `export_spec`, `plan_spec`, `list_cpanel_imports`, `inspect_cpanel_backup`, `get_realtime`, `get_server_expiry`, `get_data_saver`, `list_servers`*, `list_clusters`*, `get_ike_versions`, `get_package_update_history`, `check_ubuntu_release`, `list_package_tools`, `list_my_passkeys`*, `list_notifications`* | `install_updates`, `install_php_extension`, `reconcile_drift`, `apply_spec`, `schedule_maintenance`, `restore_cpanel_backup`, `set_turn`, `make_turn_login`, `rotate_turn_secret`, `set_livekit`, `create_livekit_key`, `delete_livekit_key`, `make_livekit_token`, `set_jitsi`, `set_data_saver`, `set_domain_renewal`, `promote_replica`*, `update_ike`, `rollback_ike`, `rollback_package_update`, `install_pecl_extension`, `install_pear_package` |
| Always available (always) | `list_runtimes`, `get_job`, `list_apps` |  |

## Safety

- **Read-only by default.** Change tools are hidden and refused unless `--allow-write` is given, and always for a read-only token.
- **Scoped by the token.** Through the panel, the token's areas, accounts, addresses and expiry apply to every call. Limited tokens can never make or see tokens.
- **Confirmation for destructive tools.** Deleting a site, account, zone, mailbox or database, restoring a backup, deleting files, failing over a cluster and buying a domain all need the name (or count) repeated exactly.
- **No lock-out.** SSH and web ports can't be closed, and loopback or overly broad ranges can't be blocked.
- **Secrets stay out of logs.** Passwords and keys are returned once; arguments such as `password`, `content`, `zone_file` and `public_key` are redacted in the audit log.
- **Strict input checks.** Unknown arguments are rejected, and the agent checks every value again.
- **Audit log.** Every call, whether allowed, denied or failed, is appended to `/var/log/ike/mcp-audit.log` as JSON lines. Through the panel, calls are also in the panel's activity log under the token's owner.

## Options

| Option | Meaning |
|---|---|
| `--allow-write` | Offer tools that change things |
| `--tools AREAS` | Only these areas, comma separated: `sites,dns,mail,databases,files,backups,containers,security,billing,accounts,server` |
| `--panel URL` | Connect through the panel with `IKE_TOKEN` (or `--token`) instead of the local socket |
| `--insecure` | Accept the panel's self-signed certificate |
| `--list-tools` | Print the tools this connection would offer, then exit |
| `--audit-log PATH` | Where to log calls (empty turns it off) |
| `--socket PATH` | The agent socket (default `/run/ike/agent.sock`) |
