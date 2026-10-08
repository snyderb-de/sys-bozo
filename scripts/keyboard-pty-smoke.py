#!/usr/bin/env python3
"""Exercise real terminal input after a harmless sys-bozo test handoff.

    go test -c -o .tmp/keyboard-tests ./internal/tui
    python3 scripts/keyboard-pty-smoke.py .tmp/keyboard-tests

Only the opt-in fixture test runs; it never probes or updates the workstation.
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

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("binary", type=Path)
parser.add_argument("--color", action="store_true", help="test the styled terminal output")
parser.add_argument("--idle-seconds", type=float, default=15, help="idle before Escape on Inspect and Repository (default: 15)")
args = parser.parse_args()
if args.idle_seconds < 0:
    parser.error("--idle-seconds must be nonnegative")
binary = str(args.binary.resolve())
pid, fd = pty.fork()
if pid == 0:
    fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
    os.environ.update(SYS_BOZO_KEYBOARD_PTY="1", TERM="xterm-256color", NO_COLOR="1")
    if args.color:
        os.environ.pop("NO_COLOR", None)
        os.environ["COLORTERM"] = "truecolor"
    timeout = int(60 + 2 * args.idle_seconds)
    os.execv(binary, [binary, "-test.run=^TestPTYKeyboardAfterHandoff$", f"-test.timeout={timeout}s", "-test.v"])

pending = b""
reaped = False
back_latencies = []


def expect(marker):
    global pending
    target = marker.encode()
    deadline = time.monotonic() + 5
    while target not in pending:
        if time.monotonic() > deadline:
            raise RuntimeError(f"Input stalled while waiting for {marker!r}: {pending[-500:]!r}")
        ready, _, _ = select.select([fd], [], [], 0.1)
        if ready:
            chunk = os.read(fd, 65536)
            if not chunk:
                raise RuntimeError(f"Terminal closed before {marker!r}")
            pending += chunk
            # A PTY is not a terminal emulator: answer Bubble Tea's startup
            # background/cursor queries before testing ordinary keyboard input.
            for query, response in ((b"\x1b]11;?\x1b\\", b"\x1b]11;rgb:1919/1717/2424\x1b\\"), (b"\x1b[6n", b"\x1b[1;1R")):
                if query in pending:
                    os.write(fd, response)
                    pending = pending.replace(query, b"")
    pending = pending.split(target, 1)[1]


def back_to(marker):
    started = time.monotonic()
    os.write(fd, b"\x1b")
    expect(marker)
    back_latencies.append(time.monotonic() - started)


try:
    # Key and description have separate ANSI styles in color mode.
    expect("CONFIRM")
    os.write(fd, b"\r")
    expect("RUN/RESULT")
    back_to("SYS/BOZO")
    for _ in range(30):
        os.write(fd, b"2")
        expect("ADD/PACKAGE")
        back_to("SYS/BOZO")
        os.write(fd, b"3")
        expect("INSPECT/SYSTEM")
        os.write(fd, b"3")
        expect("INSPECT/DOCTOR")
        back_to("INSPECT/SYSTEM")
        os.write(fd, b"5")
        expect("REPO/TRIAGE")
        expect("fixture.nix")
        back_to("INSPECT/SYSTEM")
        back_to("SYS/BOZO")
    for route in ("Inspect", "Repository"):
        os.write(fd, b"3")
        expect("INSPECT/SYSTEM")
        if route == "Repository":
            os.write(fd, b"5")
            expect("REPO/TRIAGE")
            expect("fixture.nix")
        print(f"Waiting {args.idle_seconds:g}s on {route} before Escape...", flush=True)
        time.sleep(args.idle_seconds)
        back_to("INSPECT/SYSTEM" if route == "Repository" else "SYS/BOZO")
        print(f"{route}: Escape redrew in {back_latencies[-1] * 1000:.0f} ms", flush=True)
        if route == "Repository":
            back_to("SYS/BOZO")
    # Bubble Tea decodes a pair received together as Alt+Esc.
    os.write(fd, b"3")
    expect("INSPECT/SYSTEM")
    os.write(fd, b"5")
    expect("REPO/TRIAGE")
    os.write(fd, b"\x1b\x1b")
    expect("INSPECT/SYSTEM")
    os.write(fd, b"\x1b\x1b")
    expect("SYS/BOZO")
    for rows, cols in ((19, 80), (24, 59)):
        os.write(fd, b"3")
        expect("INSPECT/SYSTEM")
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        expect("Resize")
        os.write(fd, b"\x1b")
        # Escape has no visible full-size frame until the terminal grows.
        time.sleep(0.1)
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
        expect("SYS/BOZO")
    os.write(fd, b"2")
    expect("ADD/PACKAGE")
    os.write(fd, b"\x03")
    expect("KEYBOARD_PTY_OK")
    _, status = os.waitpid(pid, 0)
    reaped = True
    if os.waitstatus_to_exitcode(status) != 0:
        raise RuntimeError(f"Fixture exited with status {status}")
    print(f"PASS: terminal handoff, 30 package/Inspect/Doctor/Repository back cycles, {args.idle_seconds:g}s idle checks, Escape after resize, and Ctrl-C from package input")
    print(f"Slowest Escape-to-redraw: {max(back_latencies) * 1000:.0f} ms")
finally:
    os.close(fd)
    if not reaped:
        os.kill(pid, signal.SIGKILL)
        os.waitpid(pid, 0)
