# Upgrading to the split hub and manager

The hub and the built-in manager now run as two programs. A new install gets them as two services (`personad hub setup`) or as two processes in the hub's container (Docker). An existing install keeps working as it is: the hub still starts the built-in manager as its child (**child mode**) until you switch it to **service mode**, where the built-in manager runs on its own and survives hub restarts. Nothing switches by itself.

This guide takes an existing install to the split layout. A person or an agent can follow it step by step: every step has a check, and a way back. The details are in [managers.md](../assistant/managers.md) ("The built-in manager as its own service"), [docker.md](docker.md) ("The hub and the built-in manager in one container"), [server.md](../server/server.md) ("Programs") and [hub-update.md](hub-update.md).

## Which section is yours

| Your hub | How to tell | Section |
| --- | --- | --- |
| Installed by `personad hub setup` on Linux, with a systemd user instance | `systemctl --user status personad` shows the hub's service | [Native Linux and macOS](#native-linux-and-macos) |
| Installed by `personad hub setup` on a Mac | `launchctl print gui/$(id -u)/com.persona.hub` shows the hub's agent | [Native Linux and macOS](#native-linux-and-macos) |
| Docker (`deploy/docker/compose.yaml`, a NAS) | `docker compose ps` in the compose folder lists `hub` and `postgres` | [Docker](#docker) |
| Run by hand or by your own process manager: `hub setup --no-service`, Linux without a systemd user instance (WSL without systemd, some containers), only `install.sh` | none of the above | [No service manager](#no-service-manager) |
| Windows | The hub doesn't run natively on Windows: it runs in Docker Desktop or in WSL 2 | [Docker](#docker), or [No service manager](#no-service-manager) / [Native](#native-linux-and-macos) for WSL without / with systemd |

An install made under the product's former name keeps the former names of its service and data directory; see [rolling-upgrade-compatibility.md](../protocol/rolling-upgrade-compatibility.md). Use those names where this guide says `personad` (the service) or `~/.persona`.

## Before you start

**The values the checks use.** On the hub's machine (in Docker, on the host, in the compose folder; `data/hub.json` belongs to the container's user, so prefix `jq` with `sudo` if it isn't yours). The checks need `curl` and `jq`.

```sh
DATA=${PERSONA_HOME:-$HOME/.persona}                     # Docker: DATA=./data
PORT=$(jq -r '.listen' "$DATA/hub.json" | sed 's/.*://')  # Docker: not needed
TOKEN=$(jq -r '.token' "$DATA/hub.json")
```

**The check this guide uses most:** the built-in manager in the hub's roster (the hub's own `GET /api/v1/managers`, what **Settings › Managers** shows in the apps). The hub takes its own token over plain HTTP from its machine only, so ask it on loopback:

```sh
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT/api/v1/managers" \
  | jq '.managers[] | select(.id == "assistant") | {online, deploy, version}'
```

In Docker, ask from inside the hub's container (from the host, the published plaintext port answers `403 tls_required`):

```sh
docker compose exec hub wget -qO- --header "Authorization: Bearer $TOKEN" http://127.0.0.1:18660/api/v1/managers \
  | jq '.managers[] | select(.id == "assistant") | {online, deploy, version}'
```

`online` must be `true`. `deploy` is `child` in child mode and `service` in service mode; a build from before the split may leave it empty.

**Where you are now:**

```sh
"$DATA/bin/personad" version                            # Docker: docker compose exec hub personad version
jq -r '.assistant.mode // "child"' "$DATA/hub.json"     # child, unless someone switched already
```

## Backups

Take them before the first step. The ways back below don't restore them: they are for a step that went wrong in a way this guide doesn't cover.

**Two databases.** The built-in manager keeps its data in a database of its own next to the hub's, named after it with `_manager` ([managers.md](../assistant/managers.md) ("The built-in manager's database")). Back up both and restore both together.

Native, with the Postgres container `hub setup` made:

```sh
docker exec persona-postgres sh -c 'pg_dumpall -U "$POSTGRES_USER"' > ~/persona-db-$(date +%Y%m%d%H%M).sql
```

Native, with another Postgres: one `pg_dump` of each database, using `database_url` from `hub.json`:

```sh
url=$(jq -r '.database_url' "$DATA/hub.json")
db=${url##*/}; db=${db%%\?*}
pg_dump "$url" > ~/persona-hub-db.sql
pg_dump "${url%/*}/${db}_manager${url#*/"$db"}" > ~/persona-manager-db.sql
```

Docker, in the compose folder:

```sh
docker compose exec -T postgres sh -c 'pg_dumpall -U "$POSTGRES_USER"' > persona-db-$(date +%Y%m%d%H%M).sql
```

**Check:** `grep -c '^CREATE DATABASE' persona-db-*.sql` is at least 2, one line for the hub's database and one for its `_manager`. With `pg_dump`, both files are non-empty and have `CREATE TABLE` lines.

**The data directory.** It has `hub.json`, the binaries you would roll back to (`bin/`), the built-in manager's binding and keys (`managers/assistant/`) and the knowledge base:

```sh
tar czf ~/persona-data-$(date +%Y%m%d%H%M).tgz -C "$(dirname "$DATA")" "$(basename "$DATA")"   # Docker: sudo tar czf persona-data-….tgz data
```

Docker: also copy `.env` and `compose.yaml`, and note the image tag you run (`docker compose images hub`).

**Restoring** (only when a way back below doesn't bring the hub back): stop the hub and the built-in manager's service if there is one, put the data directory back, restore both databases together (a `pg_dumpall` file into an empty server with `psql -f <file> postgres`; each `pg_dump` file into its emptied database with `psql -d <database> -f <file>`), then start the hub's service.

## Native Linux and macOS

Restarting the hub's service, used below:

```sh
systemctl --user restart personad                          # Linux
launchctl kickstart -k gui/$(id -u)/com.persona.hub          # macOS
```

`"$DATA/bin/personad" hub switch` also works: the hub restarts behind its edge, without a gap. The commands below use `personad` from the data directory: the service runs it, and an install made with `install.sh` doesn't have `persona-hub` and `persona-manager` until its next update. `persona-hub …` and `persona-manager …` are the same commands.

### Step 1: update to a build with the split

Update as usual: the automatic update, **Update now** in the apps, the install command again with `--hub`, or a direct deploy. The hub's self-update swaps `personad`, `persona-hub` and `persona-manager` together, and rolls them back by itself when the new version doesn't start ([hub-update.md](hub-update.md)). The install command runs `hub setup` again, which keeps an existing install's mode and says how to switch.

**Check:**

```sh
"$DATA/bin/personad" hub manager-mode                       # prints: child
"$DATA/bin/personad" manager install --builtin --check && echo ready
```

- `manager-mode` must print `child`. An older build doesn't know the command: the update didn't happen.
- `install --builtin --check` must succeed. It changes nothing; it fails on a build from before the split (`flag provided but not defined: -builtin`), and on Linux without a systemd user instance (then follow [No service manager](#no-service-manager)).
- The roster says `online: true`, `deploy: "child"`. `hub.json` is unchanged: same `token`, no `assistant.mode`.

**Way back:** the update's own rollback is automatic. By hand: put `bin/` back from the backup and restart the hub's service.

### Step 2: switch with setup

```sh
"$DATA/bin/personad" hub setup --manager-service
```

It checks first that the service can be installed (nothing changes when it can't), sets `assistant.mode` to `service`, restarts the hub's service, binds the built-in manager with a one-time code (passed in the environment, never on a command line), installs its service (`persona-manager-assistant.service` on Linux, the launchd agent `com.persona.manager.assistant` on a Mac), and waits up to 90 seconds for it on the hub's bus. Add `--json` for one JSON line per step: the line with `"step": "manager"` must say `"ok": true`.

When a step fails, setup undoes the switch itself (mode back, service removed, hub restarted into child mode) and says so. When the service only fails to come online, setup leaves it and says how to go back.

**Check:**

```sh
jq -r '.assistant.mode' "$DATA/hub.json"                    # service
systemctl --user is-active persona-manager-assistant        # active (macOS: launchctl print gui/$(id -u)/com.persona.manager.assistant)
"$DATA/bin/personad" manager status --builtin               # Binding: hub http://127.0.0.1:<port>, Service: … active
pgrep -af 'bin/personad assistant'                          # exactly one line
```

- The roster says `online: true`, `deploy: "service"`.
- After `systemctl --user restart personad`, the built-in manager keeps its process (the same pid in `pgrep`) and is back online within a few seconds.
- Logs: `journalctl --user -u persona-manager-assistant -n 100` (macOS: `tail -n 100 "$DATA/managers/assistant/manager.log"`); the hub's: `journalctl --user -u personad -n 100`.

**Way back to child mode** (same build):

```sh
"$DATA/bin/personad" hub manager-mode child
systemctl --user restart personad                            # macOS: launchctl kickstart -k gui/$(id -u)/com.persona.hub
"$DATA/bin/personad" manager uninstall --builtin             # the binding and its data stay
```

Check: the roster says `deploy: "child"` and `online: true`, `systemctl --user is-active persona-manager-assistant` no longer says `active`, and `pgrep` shows one line. A service left installed doesn't run next to the hub's child: the hub refuses its token and it waits. To go forward again, run setup with `--manager-service` again.

### Step 2, by hand

Instead of setup, with the same result:

1. `"$DATA/bin/personad" hub manager-mode service`, then restart the hub's service. **Check:** the hub no longer starts its child: `pgrep -af 'bin/personad assistant'` prints nothing, and the roster says `online: false` until the next step.
2. Bind and install with a one-time code (good for 10 minutes, once), kept in the environment:

   ```sh
   PERSONA_MANAGER_CODE=$("$DATA/bin/personad" hub manager-code | head -n 1) \
     "$DATA/bin/personad" manager join --builtin "http://127.0.0.1:$PORT" --install
   ```

   **Check:** as after setup.

**Way back:** as after setup.

### Going back to a build from before the split

First go back to child mode (above): a build from before the split doesn't start a service-mode built-in manager, and nothing else would. Then put `bin/` back from the backup and restart the hub's service. **Check:** the hub answers `GET /api/v1/health` and the roster says `online: true`. Restore the databases only if that build doesn't start.

## Docker

The image's command runs `personad supervise`, which runs the hub and the built-in manager side by side in service mode, and the hub alone (which starts its child) otherwise. Your compose file can stay as it is.

### Step 1: update the image

```sh
docker compose pull && docker compose up -d                  # with a version pinned in .env: change PERSONA_IMAGE first
```

**Check:**

- `docker compose ps` shows `hub` healthy.
- `docker compose logs hub | grep 'supervise:'` shows `assistant.mode is not service: the hub starts the built-in manager itself, as before`.
- The roster says `online: true`, `deploy: "child"`. `data/hub.json` is unchanged.

**Way back:** the previous tag in `.env`, then `docker compose up -d`.

### Step 2: service mode

```sh
docker compose exec hub personad hub manager-mode service
docker compose restart hub
```

**Check:**

- `docker compose logs hub | grep 'supervise:'` shows `bound the built-in manager to the hub at http://127.0.0.1:18660`.
- `docker compose exec hub ps -o pid,ppid,args` shows `personad supervise` with two children, `… hub run` and `… assistant`.
- The roster says `online: true`, `deploy: "service"`.
- When either program stops, the supervisor starts it again and leaves the other running (`supervise: … stopped (…): starting it again in 1s; the other runs on`).

**Way back:**

```sh
docker compose exec hub personad hub manager-mode child
docker compose restart hub
```

Check: the log says `assistant.mode is not service` again, and the roster says `deploy: "child"`.

### Optional: the built-in manager in a container of its own

Take the current `compose.yaml`. Uncomment its `manager` service and `PERSONA_BUILTIN_MANAGER: separate` on the `hub` service, then `docker compose up -d`. It needs service mode (step 2).

**Check:** `docker compose ps` lists `manager`; `docker compose logs manager | grep 'supervise:'` shows `bound the built-in manager to the hub at http://hub:18660`, or nothing new when the binding from step 2 still works; `docker compose logs hub` shows `PERSONA_BUILTIN_MANAGER=separate`; `docker compose exec hub ps -o args` shows no `assistant`; the roster says `online: true`, `deploy: "service"`.

**Way back:** comment both out again, then `docker compose up -d --remove-orphans`. Check: the hub's container runs both programs again (step 2's check).

### Going back to an image from before the split

First go back to child mode (step 2's way back): an older image doesn't run the built-in manager in service mode. Then the previous tag in `.env` (and the previous `compose.yaml` if you replaced it), and `docker compose up -d`. **Check:** `hub` healthy, and the roster says `online: true`.

## No service manager

A hub run by hand or by a process manager of your own has nothing to install the built-in manager's service into. It stays in child mode, which is fully supported:

- **Update** the binaries as you do now, and start the hub again. **Check:** the roster says `online: true`, `deploy: "child"`, and `"$DATA/bin/personad" hub manager-mode` prints `child`.
- `hub setup --manager-service` can't go with `--no-service`, and on Linux without a systemd user instance `manager install --builtin --check` fails: nothing changes either way.

**Optional: run the built-in manager yourself.** Under your own process manager, so that it is restarted when it stops:

1. `"$DATA/bin/personad" hub manager-mode service`, then start the hub again. **Check:** the roster says `online: false` (nothing runs the built-in manager yet).
2. Bind it, without installing a service:

   ```sh
   PERSONA_MANAGER_CODE=$("$DATA/bin/personad" hub manager-code | head -n 1) \
     "$DATA/bin/personad" manager join --builtin "http://127.0.0.1:$PORT"
   ```

3. Run `PERSONA_ASSISTANT_DEPLOY=service PERSONA_HOME="$DATA" "$DATA/bin/personad" assistant`. Restart it on any exit except 2: exit 2 means it isn't bound, or another one already runs on this data directory, and restarting won't help.

**Check:** the roster says `online: true`, `deploy: "service"`; a hub restart leaves its process alone; a second one exits 2 (Linux and macOS: the lock is in `managers/assistant/`).

**Way back:** `"$DATA/bin/personad" hub manager-mode child`, start the hub again, then stop the one you run. Check: `deploy: "child"`, `online: true`.

## Verify after

Whatever the type, the upgrade is done when all of these hold:

- `GET /api/v1/health` answers 200, and the apps connect.
- The roster says `online: true` with the `deploy` you chose, and **Settings › Managers** shows the built-in manager online.
- Exactly one built-in manager runs per data directory (`pgrep -af 'personad assistant'`, or `ps` in the hub's container).
- Its log and the hub's show no restart loop. The supervisor in Docker, and the service's own lines, say when a program stops and why.
- The main chat answers a message.

## Known limits

- **Nothing switches by itself.** Existing installs stay in child mode until switched, and child mode stays supported.
- **The services still run `personad`** (`personad hub run`, `personad assistant`). A later release moves them to `persona-hub` and `persona-manager`.
- **The first update that brings `persona-hub` and `persona-manager`:** if it rolls back, only `personad` goes back (the old watchdog doesn't know the two). The two programs left behind don't match the hub's version and aren't started until the next update. `install.sh` installs `personad` alone; the next update brings the other two.
- **A service left installed after going back to child mode** waits, refused by the hub, instead of running next to its child. Short overlaps remain while a refused service shuts down and a child starts. Remove it with `manager uninstall --builtin`.
- **One built-in manager per data directory.** A second one exits 2, held off by a lock on Linux and macOS. In Docker, a separate manager container while the hub's container still runs its own (no `PERSONA_BUILTIN_MANAGER: separate`) leaves one of them exiting 2 and trying again.
- **A built-in manager that runs elsewhere** (`assistant.external`) is not switched: `--manager-service` refuses it and changes nothing.
- **New binaries:** the built-in manager's service picks up a new `personad` by itself within a minute, once it is idle (at most 30 minutes; an extension's live view left open holds it that long). Managers made in the app run their old build until their service restarts.
- **Docker:** the programs get the hub's shutdown budget and a second more to stop (9 seconds by default). A longer `shutdown_timeout_seconds` needs a longer `stop_grace_period` in compose, and the supervisor reads it when the container starts. With Docker Compose 2.39, `compose.yaml`'s `image:` line also needs the image variable under its former name set in `.env`, not only `PERSONA_IMAGE`.
- **No service manager, and Windows:** the built-in manager's service needs a systemd user instance (Linux) or launchd (macOS). Elsewhere the hub keeps its child, or you run the built-in manager yourself (above). Windows computers (Persona Link and their nodes) are not affected by the split.
