# Website: home page, hosting, Hong Kong entry point, future split routing

`<site-domain>` is the domain in configured `publish.site_url` (actual value in build configuration and `CLAUDE.local.md`). `<old-domain>` was hardcoded in early builds; since 2026-11-05 (#2389),
it no longer connects to Worker and redirects the whole site with 301 to the same path on `<site-domain>` ("Old domain" below).

The website (`site/`) is Cloudflare Worker `persona-site`, hosting static pages, public personad downloads (`/dl/`, archives from R2 bucket `persona-dl`; see `docs/operations/release.md`, "Publishing personad"), installer (`/install.sh`),
and push-relay entry (`/relay/*`, `docs/operations/push-relay.md`). Deploy only through `scripts/release/publish.sh` (`docs/operations/release.md`).

**The Worker's secrets belong to its name.** `RELAY_ORIGIN`, `RELAY_PROXY_SECRET` and `EDGE_SECRET` stay with the old Worker when `name` in
`site/wrangler.jsonc` changes: the deploy creates a new Worker without them, it takes over `origin.<site-domain>`, and `/relay/*` answers 503
"push relay not configured" (2026-11-07, #1263). Before renaming, put each secret on the new name (`npx wrangler secret put <NAME> --name <new name>`;
a secret can't be read back, so take the values from the password manager). `publish.sh` reads `/relay/v1/health` before and after every deploy
and fails when a relay that was healthy before the deploy no longer is.

## Home page

`site/public/index.html` is the whole home page (#3842): Chinese, CSS and JS inline, no build step. It leads with the
SuperAgent, the same story as the README, then the demo-mode recording, shared context, linked computers, every
client, extensions, privacy, getting started, an FAQ and a closing line. The privacy, terms and 404 pages keep their
own `site/public/style.css`.

- **The hero demo is scripted, with made-up data** (the task, PR numbers, test counts). What it acts out is real: main
  chat splits a request into Agent tasks that wait for **Start**, answers an Agent's technical question itself and
  passes a goal decision to the phone (`docs/assistant/assistant.md`). Change the story only along with the product.
- **Facts follow the README.** When the README's runtimes, clients, extensions or install steps change, change the
  page in the same PR. No implementation details on the page (paths, process names, databases, engine names): it is
  for people deciding whether to try Persona; the docs are for the rest.
- **State colors keep their app meaning**: red needs you, amber working, green done, also for the packets moving on
  the diagrams.
- **Assets** come from `scripts/assets/make_site_assets.py`: `site/public/img/recording.webp` (a copy of
  `docs/assets/readme/hero-dark.zh-CN.webp`), `site/public/img/paper.webp` (the mascot cut from the Apple Vision Pro
  icon's front layer) and `site/public/fonts/persona-serif.woff`, Noto Serif CJK SC Bold cut down to the characters of
  `h1`, `h2` and `.hello`. **After editing any of that text, run `python3 scripts/assets/make_site_assets.py serif`**,
  or the new characters fall back to the visitor's own serif. The source font is not in the repository; the script
  finds a system copy or takes `--serif-font`.
- **Motion** only starts with JavaScript; without it every section is visible and static, and with reduced motion
  the page shows its finished state (the switches on, the underline drawn, no moving packets).
- **The version in the top bar** is `/dl/VERSION`, written by `scripts/release/publish.sh`; the badge stays hidden
  when the file is missing.
- **Light only.** The previous page also had a dark variant; this one follows the app icon's paper in both schemes.

## Current routing (since 2026-11-04, #1134)

```
Anyone ──► <site-domain> (A record, DNS only) ──► nginx on Alibaba Cloud Hong Kong ECS ──► origin.<site-domain> (Worker custom domain) ──► Worker
Old builds / deployed hubs ──► <old-domain> ──► 301 to same path on <site-domain>
```

- **Why**: Cloudflare free tier has no mainland China nodes, routing domestic users to European / Japanese datacenters, about 300ms round trips and noticeable packet loss; pages often stall over ten seconds.
  Alibaba Cloud Hong Kong has good mainland routing (Globalping 20 domestic probes: median ping 38ms, 0% loss). Median homepage load fell from about 0.9 to 0.23 seconds, with no timeouts.
  Overseas requests detour through Hong Kong, adding 0.5–1.5 seconds; current users are domestic, so this is the interim choice.
- **Hong Kong entry**: smallest ECS in Alibaba Cloud `cn-hongkong` (instance, IP, account in `CLAUDE.local.md`), Debian + nginx, security group opens only 80 / 443 / ICMP,
  no SSH; manage with Alibaba Cloud Assistant (`ecs RunCommand`). nginx proxies all requests to `origin.<site-domain>`, resolving dynamically with 60-second caching,
  so Cloudflare IP changes need no restart. Cache pages / static files for 60 seconds, serving stale if Worker fails; `/relay/`, `/dl/`, `/install.sh` stream without cache.
- **Client addresses**: after forwarding, `cf-connecting-ip` is the entry's address. Entry adds `x-persona-edge-secret` (Worker secret `EDGE_SECRET`)
  and `X-Forwarded-For: <client>`. `site/worker.mjs` trusts it only with the matching secret, using it for push-relay rate limits; absent / incorrect secret preserves original behavior.
  Secret in 1Password; entry copy `/etc/nginx/persona/edge-secret.conf` (0600).
- **Certificates**: entry's own Let's Encrypt certificate via certbot HTTP validation and systemd renewal timer; Cloudflare manages Worker custom-domain certificate (only `origin.<site-domain>` remains).
- **`site/wrangler.jsonc` excludes `<site-domain>`**: deployments remove unlisted custom domains and reclaim listed ones;
  `<site-domain>` points to the entry in DNS, so listing it would reclaim it for Worker. Keep `origin.<site-domain>` listed (entry origin).
- **Fallback**: if entry fails, restore `<site-domain>` as Worker custom domain (`PUT /accounts/<id>/workers/domains`, hostname `<site-domain>`,
  service persona-site); recovery takes minutes.
- **Old domain**: absent from `site/wrangler.jsonc` (deployment detaches it), with a proxied placeholder DNS record (AAAA `100::`)
  and Single Redirect rule in its zone, 301ing the whole site to `<site-domain>` with path / query intact. Rule / record stay in Cloudflare, not repository.
  User accepted the cost: clients do not replay POST unchanged after 301, breaking push relay for old App / hub builds still using the old domain. Update checks / downloads use GET and work for redirect-following clients,
  but not guaranteed for every old version. Only upgrading to a new-domain build restores operation.

## Why other approaches were rejected (all tested)

| Approach | Result |
| --- | --- |
| Alibaba Cloud CDN "Global (excluding mainland China)" | Domestic users routed to Hong Kong, but mainland path unoptimized: connection 200–390ms, median slower than Cloudflare; overseas origin fetch 10× slower |
| Alibaba Cloud Function Compute (Hong Kong) entry | Good routing (37ms domestic), but custom domain requires CNAME; root domains cannot CNAME, so binding fails |
| Cloudflare country-based Load Balancing (DNS only) | Routing works with `location_strategy.mode: resolver_ip` (default `pop` classifies by query-receiving datacenter and never identifies China). Overseas route to Worker gets `error 1000`, requiring Cloudflare for SaaS. With domain DNS on Cloudflare (O2O), custom hostname must CNAME to the SaaS zone, conflicting with Load Balancing record |
| Simple Application Server | All Hong Kong tiers out of stock (2026-11-04) |

## Future: mainland through Hong Kong, overseas through Cloudflare (one domain)

Common industry approach: put DNS with a provider supporting split routing (DNSPod / Alibaba Cloud DNS), domestic → Hong Kong entry, overseas / default → CNAME to
Cloudflare for SaaS (enabled on `<saas-domain>`, fallback origin `saas-fallback.<saas-domain>`). `<site-domain>` then no longer uses Cloudflare DNS,
so the custom hostname is not O2O and TXT prevalidation works.

Current blocker: `<site-domain>` is registered at Cloudflare Registrar, **which requires Cloudflare DNS**. ICANN prohibits transfer for the first 60 days
(registered 2026-11-03, eligible after 2027-01-02). After transferring:

1. Move DNS to DNSPod or Alibaba Cloud DNS, copying every existing record (MX / SPF / DKIM mail forwarding, verification TXT).
2. Register `<site-domain>` as a custom hostname in `<saas-domain>` SaaS (TXT validation), add Worker route `*/*` → persona-site on `<saas-domain>`,
   with Worker-excluded routes `<saas-domain>/*`, `*.<saas-domain>/*` (exclusions before wildcard, preserving that domain's own subdomains).
3. Split routes: domestic → Hong Kong entry A record; overseas / default → CNAME `saas-fallback.<saas-domain>`.
