"""Drain both SSH streams without losing a remote failure behind a full window."""
import sys
import time

OUTPUT_TAIL_BYTES = 1024 * 1024
READ_BYTES = 64 * 1024
KEEPALIVE_SECONDS = 30


def collect_output(stdout, stderr, timeout):
    channel = stdout.channel
    if stderr.channel is not channel:
        raise ValueError('Publisher streams must share one SSH channel')
    deadline = time.monotonic() + timeout
    output, errors = bytearray(), bytearray()
    while True:
        for ready, receive, tail, stream in (
                (channel.recv_ready, channel.recv, output, sys.stdout),
                (channel.recv_stderr_ready, channel.recv_stderr, errors, sys.stderr)):
            if ready():
                data = receive(READ_BYTES)
                tail.extend(data)
                del tail[:-OUTPUT_TAIL_BYTES]
                stream.write(data.decode('utf-8', errors='replace'))
                stream.flush()
        drained = not channel.recv_ready() and not channel.recv_stderr_ready()
        if drained and (channel.eof_received or channel.closed) and channel.exit_status_ready():
            code = channel.recv_exit_status()
            if code < 0:
                raise RuntimeError('Publisher SSH channel closed without an exit status')
            return output.decode('utf-8', errors='replace'), errors.decode('utf-8', errors='replace'), code
        if time.monotonic() >= deadline:
            raise TimeoutError('Publisher SSH output deadline exceeded; exit status unconfirmed; '
                               f'stderr tail: {errors[-2000:].decode("utf-8", errors="replace")}')
        if drained:
            time.sleep(0.01)
