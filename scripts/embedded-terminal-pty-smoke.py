#!/usr/bin/env python3
"""Exercise the embedded PTY without maintenance commands or real credentials."""
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


def check(binary, color, cancel):
    pid, fd = pty.fork()
    if pid == 0:
        fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
        os.environ.update(SYS_BOZO_EMBEDDED_PTY="1", TERM="xterm-256color",
                          SYS_BOZO_EMBEDDED_CANCEL=str(int(cancel)), NO_COLOR="1")
        if color:
            os.environ.pop("NO_COLOR", None)
            os.environ["COLORTERM"] = "truecolor"
        os.execv(binary, [binary, "-test.run=^TestPTYEmbeddedTerminal$", "-test.timeout=20s", "-test.v"])
    pending, output = b"", b""
    reaped = False

    def expect(marker):
        nonlocal pending, output
        target = marker.encode()
        deadline = time.monotonic() + 6
        while target not in pending:
            if time.monotonic() > deadline:
                raise AssertionError(f"missing {marker!r}: {pending[-900:]!r}")
            ready, _, _ = select.select([fd], [], [], 0.1)
            if not ready:
                continue
            chunk = os.read(fd, 65536)
            if not chunk:
                raise AssertionError(f"closed before {marker!r}: {output[-1000:]!r}")
            pending += chunk
            output += chunk
            for query, response in ((b"\x1b]11;?\x1b\\", b"\x1b]11;rgb:1717/1c1c/2424\x1b\\"),
                                    (b"\x1b[6n", b"\x1b[1;1R")):
                if query in pending:
                    os.write(fd, response)
                    pending = pending.replace(query, b"")
        pending = pending.split(target, 1)[1]

    try:
        expect("CONFIRM")
        os.write(fd, b"\r")
        if cancel:
            expect("CANCEL_READY")
            os.write(fd, b"\x1b")
            expect("dashboard controls")
            os.write(fd, b"x")
        else:
            expect("Password:")
            os.write(fd, b"fixture-secret\r")
            expect("RESIZE_READY")
            os.write(fd, b"\x1d")  # Ctrl+]
            expect("dashboard controls")
            fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
            os.kill(pid, signal.SIGWINCH)
            # Await the resized dashboard before resuming input.
            expect("dashboard controls")
            os.write(fd, b"\r")
            expect("input active")
            os.write(fd, b"\r")
            expect("21 94")  # pane dimensions: height - 9, width - 6
            expect("CONTINUE_READY")
            os.write(fd, b"\r")
        expect("Run summary")
        if b"\x1b[?1049l" in output:
            raise AssertionError("TUI left the alternate screen during embedded work")
        if b"fixture-secret" in output:
            raise AssertionError("password-style input echoed")
        os.write(fd, b"\x03")
        expect("EMBEDDED_PTY_OK")
        _, status = os.waitpid(pid, 0)
        reaped = True
        if os.waitstatus_to_exitcode(status) != 0:
            raise AssertionError(output[-1000:])
        print(f"PASS: embedded prompt, focus, resize, cleanup (color={color}, cancel={cancel})")
    finally:
        if not reaped:
            os.kill(pid, signal.SIGKILL)
            os.waitpid(pid, 0)
        os.close(fd)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    for color in (False, True):
        for cancel in (False, True):
            check(str(args.binary.resolve()), color, cancel)
