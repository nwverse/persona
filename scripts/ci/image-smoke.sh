#!/bin/bash
set -euo pipefail
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
# Drain producer output under pipefail: an early grep exit can make Docker exec
# return 255 even when the expected pairing link was present.
printf 'Checking pairing and tailcat key\n'
docker compose exec -T hub personad hub pair | grep 'persona://pair?' >/dev/null
docker compose exec -T hub personad hub tailcat-key | grep '^nodekey:' >/dev/null
curl --max-time 10 -fsS "$smoke_url/" | grep -i '<html' >/dev/null
curl --max-time 10 -fsSk "https://127.0.0.1:${CI_IMAGE_TLS_PORT:?}/api/v1/health" >/dev/null
docker compose exec -T --user 0 hub test -s /home/persona/.persona/docker/db-password
docker compose exec -T --user 0 hub grep -q '"first_setup": "code"' /home/persona/.persona/hub.json
docker compose exec -T hub wget -qO- http://127.0.0.1:18660/admin | grep 'Persona' >/dev/null
docker compose exec -T hub wget -qO- http://127.0.0.1:18660/api/v1/admin/state | grep '"password_set":false' >/dev/null
if curl --max-time 10 -fsS "$smoke_url/admin" | grep 'Persona' >/dev/null; then
  printf 'FAIL admin UI exposed through mapped port\n' >&2
  exit 1
fi
[ "$(curl --max-time 10 -s -o /dev/null -w '%{http_code}' "$smoke_url/api/v1/admin/state")" = "$(curl --max-time 10 -s -o /dev/null -w '%{http_code}' "$smoke_url/api/v1/machines")" ]
if docker info --format '{{json .SecurityOptions}}' | grep 'name=rootless' >/dev/null; then
  mapped_uid="$(docker run --rm --entrypoint awk persona-hub:ci -v uid="$(id -u)" '$1 <= uid && uid < $1 + $3 { print $2 + uid - $1 }' /proc/self/uid_map)"
  [ -n "$mapped_uid" ] && [ "$(stat -c %u data)" = "$mapped_uid" ]
else
  [ "$(stat -c %u data/hub.json)" = "$(id -u)" ]
fi
[ "$(docker compose exec -T --user 0 hub stat -c %u /home/persona/.persona/hub.json)" = "$(id -u)" ]
[ "$(docker compose exec -T --user 0 hub stat -c %a /home/persona/.persona/hub.json)" = 600 ]
printf 'PASS hub image: health, TLS, pairing, web, admin isolation and file ownership\n'
