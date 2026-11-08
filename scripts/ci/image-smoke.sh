#!/bin/bash
set -euo pipefail
# Most checks are quiet pipes: name the one that failed, or the log ends with only an exit code.
trap 'status=$?; printf "FAIL image smoke line %s (exit %s): %s\n" "$LINENO" "$status" "$BASH_COMMAND" >&2' ERR
# A check's grep reads all of its input: grep -q stops at the first match, and the writer (docker compose exec, curl)
# then fails on the closed pipe, which pipefail reports as the check failing (`hub pair` writes a QR code after its link).
holds() { grep "$@" >/dev/null; }
cd "${CI_IMAGE_FIXTURE:?}"
smoke_url="http://127.0.0.1:${CI_IMAGE_PORT:?}"
docker run --rm persona-hub:ci version | grep -Fx "${CI_IMAGE_VERSION:?}"
docker compose up -d
ok=
for attempt in $(seq 1 60); do
  if curl --max-time 3 -fsS "$smoke_url/api/v1/health"; then ok=1; break; fi
  sleep 2
done
[ -n "$ok" ] || { printf 'TIMEOUT hub health endpoint\n' >&2; exit 1; }
docker compose exec -T hub personad hub pair | holds 'persona://pair?'
docker compose exec -T hub personad hub tailcat-key | holds '^nodekey:'
curl --max-time 10 -fsS "$smoke_url/" | holds -i '<html'
curl --max-time 10 -fsSk "https://127.0.0.1:${CI_IMAGE_TLS_PORT:?}/api/v1/health" >/dev/null
docker compose exec -T --user 0 hub test -s /home/persona/.persona/docker/db-password
docker compose exec -T --user 0 hub grep -q '"first_setup": "code"' /home/persona/.persona/hub.json
# The admin page is the one that loads admin/assets/ (web/vite.config.ts); outside admin.access /admin is the web
# client, whose title is Persona too.
docker compose exec -T hub wget -qO- http://127.0.0.1:18660/admin | holds 'admin/assets/'
docker compose exec -T hub wget -qO- http://127.0.0.1:18660/api/v1/admin/state | holds '"password_set":false'
if curl --max-time 10 -fsS "$smoke_url/admin" | holds 'admin/assets/'; then
  printf 'FAIL admin UI exposed through mapped port\n' >&2
  exit 1
fi
[ "$(curl --max-time 10 -s -o /dev/null -w '%{http_code}' "$smoke_url/api/v1/admin/state")" = "$(curl --max-time 10 -s -o /dev/null -w '%{http_code}' "$smoke_url/api/v1/machines")" ]
if docker info --format '{{json .SecurityOptions}}' | holds 'name=rootless'; then
  mapped_uid="$(docker run --rm --entrypoint awk persona-hub:ci -v uid="$(id -u)" '$1 <= uid && uid < $1 + $3 { print $2 + uid - $1 }' /proc/self/uid_map)"
  [ -n "$mapped_uid" ] && [ "$(stat -c %u data)" = "$mapped_uid" ]
else
  [ "$(stat -c %u data/hub.json)" = "$(id -u)" ]
fi
[ "$(docker compose exec -T --user 0 hub stat -c %u /home/persona/.persona/hub.json)" = "$(id -u)" ]
[ "$(docker compose exec -T --user 0 hub stat -c %a /home/persona/.persona/hub.json)" = 600 ]
printf 'PASS hub image: health, TLS, pairing, web, admin isolation and file ownership\n'
