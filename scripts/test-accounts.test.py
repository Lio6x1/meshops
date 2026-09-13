"""账号验收脚本的离线测试；不访问 HTTP、不启动 Docker。"""
import importlib.util
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

SPEC = importlib.util.spec_from_file_location("account_acceptance", Path(__file__).with_name("test-accounts.py"))
accounts = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(accounts)


class Gateway:
    def __init__(self):
        self.user = None
        self.version = 1
        self.enabled = True
        self.must = True
        self.password = None
        self.created = False
        self.fail_business = False
        self.calls = []

    def client(self, *_args):
        return Client(self)


class Client:
    def __init__(self, gateway):
        self.gateway = gateway
        self.session = None
        self.csrf = ""

    def fork(self):
        other = Client(self.gateway)
        other.session = self.session
        other.csrf = self.csrf
        return other

    def request(self, method, path, body=None, csrf=True):
        g = self.gateway
        g.calls.append((method, path))
        if path == "/api/session" and method == "POST":
            if set(body) != {"username", "password"}:
                return 400, None
            if body["username"] == "admin" and body["password"] == "private-admin-password":
                self.session = ("admin", 0)
            elif body["username"] == g.user and body["password"] == g.password and g.enabled:
                self.session = ("operator", g.version)
            else:
                return 401, None
            self.csrf = "private-csrf"
            return 200, self.session_data()
        if self.session is None:
            return 401, None
        role, version = self.session
        if role == "operator" and (version != g.version or not g.enabled):
            return 401, None
        if method not in ("GET", "HEAD") and not csrf:
            return 403, None
        if role == "operator" and g.must and path not in ("/api/session", "/api/account/password"):
            return 403, None
        if path == "/api/session":
            if method == "DELETE":
                if role == "operator":
                    g.version += 1
                self.session = None
                return 204, None
            return 200, self.session_data()
        if path == "/api/account/password":
            if body["currentPassword"] != g.password:
                return 401, None
            g.password = body["newPassword"]
            g.must = False
            g.version += 1
            self.session = None
            return 204, None
        if path.startswith("/api/accounts"):
            if role != "admin":
                return 403, None
            if method == "GET":
                return 200, {"accounts": [self.account()] if g.created else [], "nextCursor": ""}
            if method == "POST" and path == "/api/accounts":
                if g.created:
                    return 409, None
                g.user, g.password, g.created = body["username"], body["password"], True
                return 201, self.account()
            if method == "PATCH":
                g.enabled = body["enabled"]
            elif path.endswith("/reset-password"):
                g.password, g.must = body["password"], True
            g.version += 1
            return (200, self.account()) if method == "PATCH" else (204, None)
        if path == "/api/v1/entities":
            if g.fail_business:
                raise RuntimeError("private-csrf private-admin-password raw-cookie")
            return 200, {"entities": []}
        raise AssertionError("unexpected offline route")

    def account(self):
        return {"id": "11111111-2222-3333-4444-555555555555", "username": self.gateway.user,
                "displayName": "Account acceptance operator", "role": "operator", "enabled": self.gateway.enabled,
                "mustChangePassword": self.gateway.must, "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"}

    def session_data(self):
        role = self.session[0]
        return {"role": role, "actorId": "admin-id" if role == "admin" else self.account()["id"],
                "tenantId": "demo_tenant", "username": "admin" if role == "admin" else self.gateway.user,
                "displayName": "Admin" if role == "admin" else "Account acceptance operator",
                "mustChangePassword": False if role == "admin" else self.gateway.must,
                "csrfToken": "private-csrf", "expiresAt": "2026-01-01T08:00:00Z"}


class AccountAcceptanceTests(unittest.TestCase):
    def test_only_loopback_origins_are_accepted(self):
        for base in ("http://127.0.0.1:18090", "http://localhost:18091", "http://[::1]:18091"):
            self.assertEqual(accounts.validate_base(base), base)
        for base in ("https://example.com", "http://127.0.0.1.evil:18090", "http://user:secret@127.0.0.1:18090",
                     "http://127.0.0.1:18090/api", "http://127.0.0.1:18090?secret=x", "http://127.0.0.1:18090#x",
                     "http://127.0.0.1:99999", "http://127.0.0.1:18090\n", "file:///tmp/x"):
            with self.subTest(base=base), self.assertRaises(accounts.SafeFailure):
                accounts.validate_base(base)

    def test_redirects_are_never_followed(self):
        handler = accounts.NoRedirect()
        self.assertIsNone(handler.redirect_request(None, None, 302, "private", {}, "http://example.com"))

    def test_existing_report_is_preserved_before_network(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "report.json"
            path.write_text("existing evidence", encoding="utf-8")
            with self.assertRaises(accounts.SafeFailure):
                accounts.reserve_output(path)
            self.assertEqual(path.read_text(), "existing evidence")

    def test_complete_flow_disables_only_generated_operator(self):
        gateway = Gateway()
        report = accounts.run_checks("http://127.0.0.1:18091", "admin", "private-admin-password", factory=gateway.client)
        self.assertTrue(report["passed"], report)
        self.assertEqual(report["cleanup"], "passed")
        self.assertTrue(gateway.created)
        self.assertFalse(gateway.enabled)
        self.assertEqual(set(row["stage"] for row in report["checks"]), set(accounts.STAGES))
        self.assertFalse(any(method == "DELETE" and path.startswith("/api/accounts") for method, path in gateway.calls))
        raw = json.dumps(report)
        for secret in ("private-admin-password", "private-csrf", gateway.password, gateway.user, "11111111-2222"):
            self.assertNotIn(secret, raw)

    def test_failure_still_disables_operator_and_redacts_exception(self):
        gateway = Gateway()
        gateway.fail_business = True
        report = accounts.run_checks("http://127.0.0.1:18091", "admin", "private-admin-password", factory=gateway.client)
        self.assertFalse(report["passed"])
        self.assertEqual(report["cleanup"], "passed")
        self.assertFalse(gateway.enabled)
        self.assertNotIn("private", json.dumps(report))
        self.assertNotIn("raw-cookie", json.dumps(report))

    def test_request_budget_never_exceeds_ten_seconds_or_deadline(self):
        self.assertEqual(accounts.request_timeout(100, 0), 10)
        self.assertEqual(accounts.request_timeout(7, 0), 7)
        with self.assertRaises(accounts.SafeFailure):
            accounts.request_timeout(0, 1)

    def test_worker_events_cannot_add_unapproved_report_fields(self):
        report = accounts.clean_report({"passed": True, "checks": [{"stage": "admin_login", "status": "passed", "password": "secret"}],
                                        "cleanup": "passed", "cookie": "secret", "failure": "raw-secret", "elapsedSeconds": 2})
        self.assertNotIn("secret", json.dumps(report))
        self.assertEqual(set(report), {"passed", "checks", "cleanup", "failure", "elapsedSeconds"})

    def test_existing_output_prevents_worker_start(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "evidence.json"
            path.write_text("original", encoding="utf-8")
            captured = io.StringIO()
            with patch.object(accounts, "supervise") as supervisor, contextlib.redirect_stdout(captured):
                self.assertEqual(accounts.main(["--output", str(path)]), 1)
            supervisor.assert_not_called()
            self.assertEqual(path.read_text(), "original")
            self.assertEqual(json.loads(captured.getvalue())["failure"], "output_exists")

    def test_slow_request_has_independent_ten_second_bound(self):
        client = accounts.HTTPClient("http://127.0.0.1:18091", accounts.Budget(100))
        thread = Mock()
        thread.is_alive.return_value = True
        with patch.object(accounts.time, "monotonic", return_value=0), patch.object(accounts.threading, "Thread", return_value=thread):
            with self.assertRaises(accounts.SafeFailure) as caught:
                client.request("GET", "/api/session")
        self.assertEqual(caught.exception.code, "request_timeout")
        thread.join.assert_called_once_with(10)

    def test_hung_worker_is_terminated_before_three_minutes(self):
        clock = [0.0]
        receiver, sender = Mock(), Mock()
        def poll(seconds):
            clock[0] += seconds
            return False
        receiver.poll.side_effect = poll
        alive = [True]
        process = Mock(pid=123)
        process.is_alive.side_effect = lambda: alive[0]
        process.terminate.side_effect = lambda: alive.__setitem__(0, False)
        process.join.side_effect = lambda timeout: clock.__setitem__(0, clock[0] + timeout)
        context = Mock()
        context.Pipe.return_value = (receiver, sender)
        context.Process.return_value = process
        with patch.object(accounts.time, "monotonic", side_effect=lambda: clock[0]), patch.object(accounts.multiprocessing, "get_context", return_value=context):
            report = accounts.supervise("http://127.0.0.1:18091")
        process.terminate.assert_called_once()
        self.assertFalse(report["passed"])
        self.assertEqual(report["failure"], "deadline")
        self.assertEqual(report["cleanup"], "unconfirmed")
        self.assertLessEqual(report["elapsedSeconds"], 180)


if __name__ == "__main__":
    unittest.main()
