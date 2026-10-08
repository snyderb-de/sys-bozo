#!/usr/bin/env python3
"""Test sys-bozo keyboard delivery through an isolated Herdr client/server.

    go test -c -o .tmp/keyboard-tests ./internal/tui
    python3 scripts/keyboard-herdr-smoke.py .tmp/keyboard-tests --herdr /path/to/herdr

Only fixture data and a harmless printf handoff run. No user session is touched.
The unpatched Herdr 0.9.1 macOS build fails the paired-Escape case.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import shlex
import shutil
import signal
import struct
import subprocess
import tempfile
import termios
import time


class Terminal:
    def __init__(self, fd, trace):
        self.fd, self.trace = fd, trace
        self.pending = b""
        self.events = []
        self.offset = 0
        self.screens = {}
        self.latencies = []
        self.review_visible = False
        # Negotiate legacy host input, as in the affected macOS terminal.
        self.queries = [(b"\x1b[?u", b"\x1b[?0u"),
                        (b"\x1b[c", b"\x1b[?1;2c"),
                        (b"\x1b[6n", b"\x1b[1;1R")]
        for code, color in ((10, b"eeee/eeee/eeee"), (11, b"1919/1717/2424")):
            for end in (b"\x07", b"\x1b\\"):
                prefix = b"\x1b]" + str(code).encode() + b";"
                self.queries.append((prefix + b"?" + end, prefix + b"rgb:" + color + b"\x1b\\"))

    def pump(self, seconds):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if select.select([self.fd], [], [], min(.02, max(0, deadline - time.monotonic())))[0]:
                chunk = os.read(self.fd, 65536)
                if not chunk:
                    raise RuntimeError("Herdr client closed during the test")
                self.pending += chunk
                self.review_visible |= b"CONFIRM" in self.pending
                for query, reply in self.queries:
                    while query in self.pending:
                        os.write(self.fd, reply)
                        self.pending = self.pending.replace(query, b"", 1)
                self.pending = self.pending[-256:]
            if self.trace.exists():
                with self.trace.open() as log:
                    log.seek(self.offset)
                    while line := log.readline():
                        try:
                            event = json.loads(line)
                        except json.JSONDecodeError:
                            break  # Retry a partially written record next time.
                        self.offset = log.tell()
                        self.events.append(event)
                        if event["event"] == "fixture":
                            self.screens = event

    def screen(self):
        return next((event["screen"] for event in reversed(self.events)
                     if event["event"] == "view_done"), None)

    def key(self, data, target, label):
        start, count = time.monotonic(), len(self.events)
        os.write(self.fd, data)
        while time.monotonic() - start < 3:
            self.pump(.02)
            if any(e["event"] == "view_done" and e["screen"] == self.screens[target]
                   for e in self.events[count:]):
                elapsed = (time.monotonic() - start) * 1000
                if data == b"\x1b":
                    self.latencies.append(elapsed)
                return
        delivered = sum(e.get("escape_bytes", 0) for e in self.events[count:])
        raise RuntimeError(f"{label}: no {target} page; delivered Escape bytes={delivered}")

    def burst(self, count, spacing):
        self.key(b"3", "inspect", "open Inspect")
        self.key(b"5", "repo", "open Repository")
        start = len(self.events)
        if spacing:
            for _ in range(count):
                os.write(self.fd, b"\x1b")
                self.pump(spacing)
        else:
            os.write(self.fd, b"\x1b" * count)
        self.pump(.5)
        delivered = sum(e.get("escape_bytes", 0) for e in self.events[start:])
        if delivered != count or self.screen() == self.screens["repo"]:
            raise RuntimeError(f"{count} rapid Escapes ({spacing:g}s spacing): "
                               f"delivered {delivered}/{count}; "
                               f"back page={'no' if self.screen() == self.screens['repo'] else 'yes'}")
        # Bubble Tea may combine adjacent bytes as Alt+Esc; either back depth
        # is acceptable, but every sent byte must reach the application.
        if self.screen() == self.screens["inspect"]:
            self.key(b"\x1b", "home", "return Home after burst")
        if self.screen() != self.screens["home"]:
            raise RuntimeError("Escape burst landed on an unexpected page")
        print(f"PASS: {count} rapid Escapes, {spacing:g}s spacing; all bytes delivered", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--herdr", default="herdr", help="Herdr executable to test")
    parser.add_argument("--server-herdr", help="start the isolated server with a different build")
    parser.add_argument("--idle-seconds", type=float, default=15)
    args = parser.parse_args()
    if args.idle_seconds < 0:
        parser.error("--idle-seconds must be nonnegative")
    binary = args.binary.resolve(strict=True)
    herdr = shutil.which(args.herdr)
    if not herdr:
        parser.error("Herdr executable not found")
    herdr = str(Path(herdr).resolve())
    server_herdr = shutil.which(args.server_herdr) if args.server_herdr else None
    if args.server_herdr and not server_herdr:
        parser.error("Server Herdr executable not found")
    # Keep Unix socket paths below macOS's limit; use a unique config root.
    with tempfile.TemporaryDirectory(prefix="bozo-keys-", dir="/tmp") as directory:
        base = Path(directory)
        trace = base / "input.jsonl"
        config = base / "config/herdr"
        config.mkdir(parents=True)
        shell = base / "fixture-shell"
        timeout = int(120 + 2 * args.idle_seconds)
        shell.write_text("#!/bin/sh\nexec " + shlex.join([
            str(binary), "-test.run=^TestPTYKeyboardAfterHandoff$",
            f"-test.timeout={timeout}s", "-test.v"]) + "\n")
        shell.chmod(0o700)
        (config / "config.toml").write_text(
            'onboarding = false\n[terminal]\ndefault_shell = ' + json.dumps(str(shell)) +
            '\n[ui.sound]\nenabled = false\n')
        env = {key: value for key, value in os.environ.items() if not key.startswith("HERDR_")}
        env.update(XDG_CONFIG_HOME=str(base / "config"), XDG_STATE_HOME=str(base / "state"),
                   TERM="xterm-256color", COLORTERM="truecolor", SYS_BOZO_KEYBOARD_PTY="1",
                   SYS_BOZO_KEYBOARD_TRACE=str(trace))
        env.pop("NO_COLOR", None)
        command = [herdr, "--session", "keys"]
        server = None
        if server_herdr:
            server = subprocess.Popen([server_herdr, "--session", "keys", "server"],
                                      env=env, cwd=base, stdout=subprocess.DEVNULL,
                                      stderr=subprocess.DEVNULL)
            socket = config / "sessions/keys/herdr-client.sock"
            deadline = time.monotonic() + 20
            while not socket.exists() and server.poll() is None and time.monotonic() < deadline:
                time.sleep(.05)
            if not socket.exists():
                server.terminate()
                server.wait(timeout=5)
                raise RuntimeError("Isolated server did not start")
        pid, fd = pty.fork()
        if pid == 0:
            os.chdir(base)
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 39, 103, 0, 0))
            os.execve(herdr, command, env)
        terminal = Terminal(fd, trace)
        try:
            deadline = time.monotonic() + 25
            while time.monotonic() < deadline:
                terminal.pump(.05)
                if (terminal.review_visible and terminal.screens
                        and terminal.screen() == terminal.screens["review"]):
                    break
            else:
                raise RuntimeError("Fixture did not reach Review through Herdr")
            terminal.key(b"\r", "result", "confirm harmless printf")
            terminal.key(b"\x1b", "home", "return after terminal handoff")
            for _ in range(3):
                terminal.key(b"3", "inspect", "open Inspect")
                terminal.key(b"5", "repo", "open Repository")
                terminal.key(b"\x1b", "inspect", "Escape Repository")
                terminal.key(b"\x1b", "home", "Escape Inspect")
            for count, spacing in ((2, 0), (3, 0), (12, 0), (12, .04), (12, .1)):
                terminal.burst(count, spacing)
            terminal.key(b"3", "inspect", "open Inspect for Alt-arrow")
            count = len(terminal.events)
            os.write(fd, b"\x1b\x1b[A")
            terminal.pump(.3)
            if (terminal.screen() != terminal.screens["inspect"] or not any(
                    e.get("key_type") == -2 and e.get("alt")
                    for e in terminal.events[count:])):
                raise RuntimeError("Legacy Alt-Up was changed into plain Escape or Up")
            terminal.key(b"\x1b", "home", "return after Alt-arrow")
            print("PASS: legacy Alt-Up remains Alt-Up", flush=True)
            for page in ("inspect", "repo"):
                terminal.key(b"3", "inspect", "open Inspect")
                if page == "repo":
                    terminal.key(b"5", "repo", "open Repository")
                print(f"Waiting {args.idle_seconds:g}s on {page} before Escape...", flush=True)
                terminal.pump(args.idle_seconds)
                terminal.key(b"\x1b", "inspect" if page == "repo" else "home", "idle Escape")
                if page == "repo":
                    terminal.key(b"\x1b", "home", "return Home")
            terminal.key(b"2", "package", "open package text input")
            os.write(fd, b"\x03")
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline:
                terminal.pump(.02)
                if any(e["event"] == "fixture_done" for e in terminal.events):
                    break
            else:
                raise RuntimeError("Ctrl-C did not quit successfully from package input")
            print(f"PASS: handoff, rapid Escape, idle navigation, Ctrl-C through Herdr; "
                  f"slowest single Escape {max(terminal.latencies):.0f} ms")
        finally:
            # This command can only reach the isolated server started above.
            stopped = subprocess.run(command + ["server", "stop"], env=env,
                                     capture_output=True, timeout=20)
            if stopped.returncode:
                print("Warning: isolated test server did not stop cleanly")
            os.close(fd)
            try:
                os.kill(pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            os.waitpid(pid, 0)
            if server:
                server.wait(timeout=5)


if __name__ == "__main__":
    main()
