"""Bound retries to the original deadline and an already selected node transaction."""
import time
import urllib.error

RECONNECT_ATTEMPTS = 4
RECONNECT_DELAY = 2
TRANSIENT_HTTP = {502, 503, 504}


def transient(error):
    if isinstance(error, urllib.error.HTTPError):
        return error.code in TRANSIENT_HTTP
    return isinstance(error, (OSError, TimeoutError))


def request(relay, root, target, action, deadline, params=None, retry=False):
    attempts = RECONNECT_ATTEMPTS if retry else 1
    for attempt in range(attempts):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError('Node update deadline exceeded')
        try:
            return relay(root, target, action, params, timeout=min(50, remaining))
        except (OSError, TimeoutError) as error:
            if not transient(error) or attempt + 1 == attempts:
                raise
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise
            time.sleep(min(RECONNECT_DELAY, remaining))
