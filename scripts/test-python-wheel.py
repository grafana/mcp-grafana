#!/usr/bin/env python3
"""Test an installed wheel with its own Python (no SDK or source imports).

    /path/to/venv/python -I scripts/test-python-wheel.py -v
    python3.9 -I scripts/test-python-wheel.py --self-test -v

Installation is deliberately external: use a normal installer without overriding
Requires-Python. --self-test checks only this subprocess harness, not the wheel.
"""

import contextlib
import importlib.metadata
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import sys
import sysconfig
import tempfile
import threading
import time
import unittest


TIMEOUT = 30


@contextlib.contextmanager
def child(command, shutdown_timeout=5):
    # A neutral cwd and environment prevent source imports and credential reuse.
    env = {
        key: value for key, value in os.environ.items()
        if not key.startswith(("PYTHON", "GRAFANA_", "OTEL_"))
    }
    env.update(GRAFANA_URL="http://127.0.0.1:9", DO_NOT_TRACK="1")
    options = (
        {"creationflags": subprocess.CREATE_NEW_PROCESS_GROUP}
        if os.name == "nt" else {"start_new_session": True}
    )
    with tempfile.TemporaryDirectory() as cwd, tempfile.TemporaryFile() as errors:
        process = subprocess.Popen(
            command, cwd=cwd, env=env, stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=errors, text=True,
            encoding="utf-8", **options
        )
        failed = False
        try:
            yield process, errors
        except Exception:
            failed = True
            raise
        finally:
            if not process.stdin.closed:
                try:
                    process.stdin.close()
                except BrokenPipeError:
                    pass
            try:
                process.wait(timeout=shutdown_timeout)
            except subprocess.TimeoutExpired:
                # Windows' launcher spawns Go; killing only Python leaks the child.
                if os.name == "nt":
                    subprocess.run(
                        ["taskkill", "/PID", str(process.pid), "/T", "/F"],
                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                        timeout=TIMEOUT, check=False,
                    )
                else:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass  # The process may have exited just after the timeout.
                process.wait(timeout=TIMEOUT)
            process.stdout.close()
            if failed:
                errors.seek(0)
                print("Child stderr:\n" + errors.read().decode("utf-8", "replace"),
                      file=sys.stderr)


class StdioClient:
    def __init__(self, process):
        self.process = process
        self.lines = queue.Queue()
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()

    def _read(self):
        try:
            for line in self.process.stdout:
                self.lines.put(line)
        finally:
            self.lines.put(None)

    def send(self, message):
        # These four small messages fit in a pipe even if startup stalls.
        self.process.stdin.write(json.dumps(message) + "\n")
        self.process.stdin.flush()

    def request(self, request_id, method, params=None, timeout=TIMEOUT):
        message = {"jsonrpc": "2.0", "id": request_id, "method": method}
        if params is not None:
            message["params"] = params
        self.send(message)
        deadline = time.monotonic() + timeout
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise AssertionError("Timed out waiting for " + method)
            try:
                line = self.lines.get(timeout=remaining)
            except queue.Empty:
                raise AssertionError("Timed out waiting for " + method) from None
            if line is None:
                raise AssertionError("Server closed stdout during " + method)
            response = json.loads(line)
            if not isinstance(response, dict) or response.get("jsonrpc") != "2.0":
                raise AssertionError("Invalid JSON-RPC response: " + repr(response))
            if "id" not in response and "method" in response:
                continue  # Notifications may be interleaved with responses.
            if response.get("id") != request_id or "result" not in response or "error" in response:
                raise AssertionError("Unexpected response to " + method + ": " + repr(response))
            return response["result"]


class InstalledWheelTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.distribution = importlib.metadata.distribution("mcp-grafana")
        cls.console = Path(sysconfig.get_path("scripts")) / (
            "mcp-grafana.exe" if os.name == "nt" else "mcp-grafana"
        )
        cls.commands = ([str(cls.console)], [sys.executable, "-I", "-m", "mcp_grafana"])

    def test_installed_metadata(self):
        root = Path(self.distribution.locate_file("")).resolve()
        self.assertIn(Path(sys.prefix).resolve(), root.parents,
                      "Distribution must be installed in the tested environment")
        self.assertEqual(self.distribution.metadata["Requires-Python"].replace(" ", ""), ">=3.9")
        self.assertEqual(self.distribution.requires or [], [],
                         "The Go launcher must not depend on the Python MCP SDK or other packages")
        self.assertTrue(self.console.is_file(), self.console)
        self.assertTrue(any(
            entry.group == "console_scripts" and entry.name == "mcp-grafana"
            for entry in self.distribution.entry_points
        ))

    def test_launcher_commands(self):
        for command in self.commands:
            for flag in ("--help", "--version"):
                with self.subTest(command=command, flag=flag), child(command + [flag]) as (process, errors):
                    output, _ = process.communicate(timeout=TIMEOUT)
                    errors.seek(0)
                    output += errors.read().decode("utf-8", "replace")
                    self.assertEqual(process.returncode, 0, output)
                    self.assertTrue(output.strip(), "Launcher produced no output")
                    if flag == "--help":
                        self.assertIn("transport", output)
                    else:
                        self.assertEqual(output.strip(), self.distribution.version)

    def test_stdio_protocol(self):
        for command in self.commands:
            with self.subTest(command=command), child(
                command + ["--transport", "stdio", "--usage-stats", "disabled"]
            ) as (process, _):
                client = StdioClient(process)
                initialized = client.request(1, "initialize", {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {},
                    "clientInfo": {"name": "python-wheel-test", "version": "1.0"},
                })
                self.assertEqual(initialized["protocolVersion"], "2024-11-05")
                self.assertTrue(initialized["serverInfo"]["name"])
                self.assertIn("tools", initialized["capabilities"])
                client.send({"jsonrpc": "2.0", "method": "notifications/initialized"})
                tools = client.request(2, "tools/list")["tools"]
                self.assertTrue(tools, "The real Grafana server should expose tools")
                for tool in tools:
                    self.assertTrue(tool["name"])
                    self.assertIsInstance(tool["inputSchema"], dict)
                self.assertEqual(client.request(3, "ping"), {})
                process.stdin.close()
                self.assertEqual(process.wait(timeout=TIMEOUT), 0)
                client.reader.join(timeout=TIMEOUT)
                self.assertFalse(client.reader.is_alive())


class HarnessTests(unittest.TestCase):
    """Real subprocess checks of the harness; these do not prove compatibility."""

    def test_timeout_cleans_up_stalled_process(self):
        with child([sys.executable, "-I", "-c", "import time; time.sleep(60)"],
                   shutdown_timeout=0.1) as (process, _):
            client = StdioClient(process)
            with self.assertRaisesRegex(AssertionError, "Timed out"):
                client.request(1, "ping", timeout=0.1)
        self.assertIsNotNone(process.poll())
        client.reader.join(timeout=2)
        self.assertFalse(client.reader.is_alive())

    def test_eof_is_not_a_successful_response(self):
        with child([sys.executable, "-I", "-c", "import sys; sys.stdin.readline()"]
                   ) as (process, _):
            client = StdioClient(process)
            with self.assertRaisesRegex(AssertionError, "closed stdout"):
                client.request(1, "ping")

    def test_notifications_and_response(self):
        code = (
            "import json, sys; message = json.loads(sys.stdin.readline()); "
            "print(json.dumps({'jsonrpc': '2.0', 'method': 'notifications/message'})); "
            "print(json.dumps({'jsonrpc': '2.0', 'id': message['id'], 'result': {}}))"
        )
        with child([sys.executable, "-I", "-c", code]) as (process, _):
            self.assertEqual(StdioClient(process).request(7, "ping"), {})

    def test_rpc_error_is_not_a_successful_response(self):
        code = (
            "import sys; sys.stdin.readline(); "
            "print('{\"jsonrpc\":\"2.0\",\"id\":1,\"error\":{\"code\":-32601}}')"
        )
        with child([sys.executable, "-I", "-c", code]) as (process, _):
            with self.assertRaisesRegex(AssertionError, "Unexpected response"):
                StdioClient(process).request(1, "ping")


if __name__ == "__main__":
    suite = "InstalledWheelTests"
    if "--self-test" in sys.argv:
        sys.argv.remove("--self-test")
        suite = "HarnessTests"
    unittest.main(defaultTest=suite)
