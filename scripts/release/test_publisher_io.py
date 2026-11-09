"""SSH channel failures retain both streams without a sequential-read deadlock."""
import contextlib
import io
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import publisher_io


class Channel:
    def __init__(self, output=b'{"phase":"confirmed"}\n', errors=b'failure\n', code=0):
        self.output, self.errors, self.code = output, errors, code
        self.closed = False

    @property
    def eof_received(self):
        return not self.output and not self.errors

    def recv_ready(self):
        # Simulate stdout waiting behind stderr's SSH window.
        return not self.errors and bool(self.output)

    def recv(self, size):
        result, self.output = self.output[:size], self.output[size:]
        return result

    def recv_stderr_ready(self):
        return bool(self.errors)

    def recv_stderr(self, size):
        result, self.errors = self.errors[:size], self.errors[size:]
        return result

    def exit_status_ready(self):
        return self.eof_received or self.closed

    def recv_exit_status(self):
        return self.code


class PublisherIOTests(unittest.TestCase):
    def collect(self, channel, timeout=10):
        stream = SimpleNamespace(channel=channel)
        return publisher_io.collect_output(stream, stream, timeout)

    def test_stderr_larger_than_window_does_not_block_stdout_or_exit(self):
        errors = b'e' * (3 * 1024 * 1024)
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            output, tail, code = self.collect(Channel(errors=errors, code=7))
        self.assertEqual(output, '{"phase":"confirmed"}\n')
        self.assertEqual(code, 7)
        self.assertEqual(tail, 'e' * publisher_io.OUTPUT_TAIL_BYTES)
        self.assertEqual(err.getvalue(), errors.decode())
        self.assertEqual(out.getvalue(), output)

    def test_missing_exit_status_fails_instead_of_accepting_a_receipt(self):
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaisesRegex(RuntimeError, 'without an exit status'):
                self.collect(Channel(code=-1))

    def test_lost_channel_deadline_keeps_already_received_error(self):
        channel = Channel(output=b'', errors=b'original traceback\n')
        channel.exit_status_ready = lambda: False
        out, err = io.StringIO(), io.StringIO()
        with patch.object(publisher_io.time, 'monotonic', side_effect=[0, 0, 3]), \
                patch.object(publisher_io.time, 'sleep'), \
                contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            with self.assertRaisesRegex(TimeoutError, 'original traceback'):
                self.collect(channel, timeout=2)
        self.assertEqual(err.getvalue(), 'original traceback\n')

    def test_different_channel_streams_rejected(self):
        with self.assertRaises(ValueError):
            publisher_io.collect_output(SimpleNamespace(channel=Channel()),
                                        SimpleNamespace(channel=Channel()), 10)


if __name__ == '__main__':
    unittest.main()
