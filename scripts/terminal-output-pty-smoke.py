#!/usr/bin/env python3
"""Check stderr line endings while a harmless child disables terminal OPOST.

go test -c -o .tmp/keyboard-tests ./internal/tui
python3 scripts/terminal-output-pty-smoke.py .tmp/keyboard-tests
"""
import argparse
import fcntl
import os
import pty
import select
import signal
import struct
import termios
import time
from pathlib import Path


def check(binary, traced):
    pid, fd = pty.fork()
    if pid == 0:
        fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
        os.environ.update(SYS_BOZO_OUTPUT_PTY="1", SYS_BOZO_OUTPUT_TRACE=str(int(traced)),
                          TERM="xterm-256color", NO_COLOR="1")
        os.execv(binary, [binary, "-test.run=^TestPTYInteractiveOutput$", "-test.timeout=15s", "-test.v"])
    output = b""
    checked = reaped = False
    try:
        deadline = time.monotonic() + 12
        while time.monotonic() < deadline:
            ready, _, _ = select.select([fd], [], [], 0.1)
            if not ready:
                continue
            chunk = os.read(fd, 65536)
            if not chunk:
                break
            output += chunk
            for query, response in ((b"\x1b]11;?\x1b\\", b"\x1b]11;rgb:1717/1c1c/2424\x1b\\"),
                                    (b"\x1b[6n", b"\x1b[1;1R")):
                if query in output:
                    os.write(fd, response)
                    output = output.replace(query, b"")
            if not checked and (b"OUTPUT_SECOND\n" in output or b"OUTPUT_SECOND\r\n" in output):
                if b"OUTPUT_FIRST\r\nOUTPUT_SECOND\r\n" not in output:
                    raise AssertionError(f"stderr stair-steps in raw output mode: {output[-300:]!r}")
                checked = True
                os.write(fd, b"\n")
            if b"OUTPUT_RESTORED_OK" in output:
                _, status = os.waitpid(pid, 0)
                reaped = True
                if not checked or os.waitstatus_to_exitcode(status) != 0:
                    raise AssertionError(f"fixture failed: {output[-500:]!r}")
                print(f"PASS: aligned stderr, captured errors, terminal restoration (traced={traced})")
                return
        raise AssertionError(f"fixture timed out: {output[-500:]!r}")
    finally:
        if not reaped:
            os.kill(pid, signal.SIGKILL)
            os.waitpid(pid, 0)
        os.close(fd)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    for traced in (False, True):
        check(str(args.binary.resolve()), traced)
