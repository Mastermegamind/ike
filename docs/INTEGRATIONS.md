# Billing integrations: WHMCS and Blesta

Ike includes provisioning modules for WHMCS and Blesta, in [`integrations/`](../integrations). When a customer orders hosting, the billing system creates the Ike account, its sign-in and first website on the right package. It also suspends, unsuspends, changes the package or password, terminates, and signs the customer in with one click. Each night it also brings disk and bandwidth use back to the billing system.

Both modules use the provisioning API (`/api/v1/provision/…`, see [API.md](API.md)). Any other billing system can use it the same way.

## 1. Make a token for the billing system

In Ike, sign in as an admin (or as the reseller whose customers these are) and open **Settings → API tokens**:

- **Limited**, with `accounts` write, `billing` write and `sites` write
- **Allowed addresses:** the billing server's IP
- No expiry, or a long one with a reminder to replace it

Keep the token; it's shown once.

## 2. Give the panel a real certificate

The modules check the panel's certificate, and the panel starts with a self-signed one. To replace it, as root on the server:

1. Point a name such as `panel.example.com` at the server. In Ike, add it as a **static** website and click **Get certificate**.
2. Copy the certificate to the panel now and after every renewal:

   ```sh
   cat >/etc/letsencrypt/renewal-hooks/deploy/ike-panel <<'HOOK'
   #!/bin/sh
   # Give ike-panel the certificate for its own address.
   [ "$RENEWED_LINEAGE" = /etc/letsencrypt/live/panel.example.com ] || exit 0
   install -m 0640 -o ike -g ike "$RENEWED_LINEAGE/fullchain.pem" /etc/ike/tls/panel.crt
   install -m 0600 -o ike -g ike "$RENEWED_LINEAGE/privkey.pem" /etc/ike/tls/panel.key
   systemctl restart ike-panel
   HOOK
   chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/ike-panel
   RENEWED_LINEAGE=/etc/letsencrypt/live/panel.example.com /etc/letsencrypt/renewal-hooks/deploy/ike-panel
   ```

3. Use `panel.example.com` as the host name in the billing system.

Until then, you can set `IKE_ALLOW_SELF_SIGNED=1` in the billing server's PHP environment. This turns off the certificate check for Ike only, so use it just for testing: anyone between the two servers could read the token.

## WHMCS

1. Copy `integrations/whmcs/modules/servers/ike` to `<whmcs>/modules/servers/ike`.
2. **System Settings → Servers → Add New Server**:
   - Module: **Ike**
   - Hostname: `panel.example.com`
   - Port: `2087` for an admin token or `2086` for a reseller token (tick *Override with Custom Port*)
   - Secure: on
   - Access Hash: the token
3. Click **Test Connection**.
4. On each product's **Module Settings** tab, choose the server group, enter the **Ike package** name or id (from **Business → Packages** in Ike), and choose what the first website runs.

Single sign-on works from the client area (**Log in to Ike**) and from the admin area (**Log in to Ike as the customer**). The links last 60 seconds and work once.

## Blesta

1. Copy `integrations/blesta/components/modules/ike` to `<blesta>/components/modules/ike`.
2. **Settings → Company → Modules → Available**, install **Ike**, then **Add Server** with the host name, port, token and a name.
3. On each package, choose the **Ike** module and enter the Ike package.

## What each action does in Ike

| Billing action | In Ike |
|---|---|
| Create | Account named from the username, a panel sign-in for the customer, the first website, on the package |
| Suspend / Unsuspend | Websites show the suspended page; apps, cron jobs and SSH/SFTP stop; everything comes back on unsuspend |
| Change package | New limits apply at once; refused if the account already uses more than the new package allows |
| Change password | The customer's panel password |
| Terminate | The account, its websites and its panel sign-in are deleted, and its subscription is cancelled |
| Usage update | Disk and bandwidth per account |
