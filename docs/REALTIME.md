# Video and calls: TURN and LiveKit

Ike can run the two servers that video and voice apps need:

- **TURN** (coturn) relays a call when two people can't reach each other directly: strict office firewalls, mobile networks, carrier-grade NAT. Any WebRTC app can use it, including ones you didn't build with LiveKit.
- **LiveKit** is a WebRTC media server for rooms: video calls, online classes, webinars and live streams. Apps connect to `wss://your-domain` and sign join tokens with an API key.

Both are optional. Install them with the installer, then switch them on in the panel under **Server → Real-time media** (or with `ike realtime`).

```sh
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash -s -- --turn --livekit
```

On a server that already runs Ike, add them without touching anything else:

```sh
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash -s -- --add turn,livekit
```

## TURN

1. Point a name at the server, e.g. `turn.example.com` (an A record to the server's IPv4 address).
2. For TURN over TLS (port 5349, which gets through firewalls that only allow HTTPS), that name needs HTTPS on this server: add a website for it (a static one is enough) and get it a certificate, or use a certificate that already lists the name. Ike copies it where coturn can read it and copies it again when Let's Encrypt renews it.
3. Switch TURN on with that domain. Ike writes `/etc/turnserver.conf`, starts coturn and opens 3478 (UDP and TCP), 5349 and the relay ports 40000–49999/udp.

What Ike sets for you:

- **Short-lived logins only.** There are no fixed TURN passwords. Logins are signed with a shared secret (the "TURN REST API" scheme: username `expiry:name`, password an HMAC of it), last 12 hours by default and at most 7 days. **New secret** invalidates every login at once.
- **No relaying into private networks.** coturn refuses to relay to loopback, private (10/8, 172.16/12, 192.168/16), carrier-grade NAT, link-local and multicast addresses, so the TURN server can't be used to reach things behind your firewall.
- Optional limits: logins per user, logins in all, and bandwidth per session.

Getting a login for an app (it should ask for a fresh one rather than keep one):

```sh
curl -X POST https://panel.example.com:2087/api/v1/realtime/turn/credentials \
     -H "Authorization: Bearer $IKE_TOKEN" -d '{"user":"web","ttl_hours":12}'
```

```js
const { uris, username, credential } = await (await fetch("/my-backend/turn")).json();
const pc = new RTCPeerConnection({ iceServers: [{ urls: uris, username, credential }] });
```

When **Account owners can make TURN logins** is on, account owners get them from `POST /accounts/{account}/realtime/turn-credentials`, and their account name is part of the username so you can tell whose traffic is whose.

## LiveKit

1. Point a name at the server, e.g. `rtc.example.com`.
2. Switch LiveKit on with that domain and the account that should own its website. Ike:
   - writes `/etc/livekit/livekit.yaml` and starts `livekit.service` (its own user, hardened systemd unit),
   - makes the domain a website that forwards to LiveKit on `127.0.0.1:7880` (WebSockets included) and gets it a Let's Encrypt certificate (if the name doesn't point here yet, the page says so; get the certificate later from the website's **Domains & HTTPS** tab),
   - opens 7881/tcp and 50000–60000/udp for media,
   - makes the first API key and shows its secret once.
3. Optional: **Give LiveKit's callers this server's TURN server**. Ike writes a TURN login into LiveKit's config and renews it on its own.
4. Optional: a webhook address for room and participant events, signed with the first API key.

### API keys

Make one key per app, so you can remove one without breaking the others. Secrets are sealed with Ike's master key (and re-sealed when it is rotated) and shown only when the key is made. Removing a key restarts LiveKit, so people in rooms reconnect.

A key can belong to an account. Account owners see their keys (never the secrets) under **Video & calls**, and with **Account owners can make their own API keys** on they can add and remove their own.

### Trying a room

**Try a room** on the API keys panel (or `ike realtime token ROOM NAME`) makes a join token and opens LiveKit Meet with it, to check camera, microphone and TURN from a phone on mobile data. Apps make their own tokens with a LiveKit server SDK:

```js
import { AccessToken } from "livekit-server-sdk";
const at = new AccessToken(process.env.LIVEKIT_API_KEY, process.env.LIVEKIT_API_SECRET, { identity: "ada" });
at.addGrant({ room: "class-1", roomJoin: true });
const token = await at.toJwt(); // the browser connects to wss://rtc.example.com with it
```

## Jitsi Meet

For video meetings in the browser with nothing to install, switch on **Jitsi Meet** under Real-time media (it needs Docker: `install.sh --add` doesn't install Docker, so use the installer's `--docker` first). Ike runs the official Jitsi images as one Compose project, serves the meeting site on a domain of yours with HTTPS (the container itself only listens on `127.0.0.1:8000`), opens 10000/udp for media, and, when you tick it, gives Jitsi this server's TURN server. **Lobby** makes everyone wait until a moderator lets them in. The first start downloads the images, so it takes a few minutes.

## Sizing

LiveKit forwards video rather than mixing it, so CPU is modest; bandwidth is what runs out. A rough guide: each person receiving one 720p video uses about 1.5 Mbit/s down from the server, so a class of 30 watching one teacher needs about 45 Mbit/s out. On a VPS with a 200 Mbit/s port, that's a few such classes at once. TURN relays both directions of every relayed call, so set **Kbit/s per session** if the server is shared with websites.

## The command line

```sh
ike realtime                                          # state, addresses, keys
ike realtime turn on --domain turn.example.com --tls yes --accounts yes
ike realtime turn login --user test --hours 2
ike realtime livekit on --domain rtc.example.com --account media --turn yes --webhook https://app.example.com/livekit
ike realtime keys add "School app" --account school
ike realtime token class-1 ada
```

The same is in the [API](API.md#real-time-media) and the [MCP server](MCP.md) (`get_realtime`, `set_turn`, `set_livekit`, `make_livekit_token`, ...).

## Status

This is new in 0.8. The configuration files follow coturn's and LiveKit's documentation and are covered by tests, but they haven't been run on a live server yet; please report anything coturn or LiveKit refuses.
