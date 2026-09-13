#!/usr/bin/env python3
"""真实本机 HTTP 账号验收；凭证只从环境读取，结果只记录白名单阶段。

用法：python -B scripts/test-accounts.py --base-url http://127.0.0.1:18091 --output evidence/accounts.json
需要 MESHOPS_WEB_ADMIN_USERNAME、MESHOPS_WEB_ADMIN_PASSWORD；不启动 Docker。
"""
import argparse
import copy
import http.cookiejar
import ipaddress
import json
import math
import multiprocessing
import os
from pathlib import Path
import queue
import re
import secrets
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

STAGES = (
    "admin_login", "login_role_spoof", "create_operator", "duplicate_username", "initial_operator_login",
    "must_change_blocks_business", "password_csrf", "change_initial_password", "initial_cookie_revoked",
    "old_password_rejected", "personal_login", "personal_session", "business_allowed", "operator_management_denied",
    "disable_operator", "disabled_cookie_revoked", "enable_operator", "enable_preserves_revocation", "enabled_login",
    "reset_password", "reset_cookie_revoked", "reset_old_password_rejected", "reset_login", "reset_blocks_business",
    "change_reset_password", "final_login", "logout_operator", "logout_cookie_revoked", "cleanup_operator", "admin_logout",
)
FAILURES = {"", "invalid_base_url", "output_exists", "output_unavailable", "missing_credentials", "deadline",
            "request_timeout", "transport_error", "unexpected_status", "invalid_response", "response_too_large",
            "cleanup_failed", "worker_failure", "internal_error"}
ID = re.compile(r"^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$")


class SafeFailure(Exception):
    def __init__(self, code):
        self.code = code if code in FAILURES else "internal_error"
        super().__init__(self.code)


def validate_base(value):
    try:
        if not value or any(ord(c) <= 32 for c in value):
            raise ValueError()
        parsed = urllib.parse.urlsplit(value)
        if parsed.scheme not in ("http", "https") or parsed.username is not None or parsed.password is not None:
            raise ValueError()
        if parsed.path not in ("", "/") or parsed.query or parsed.fragment or parsed.port is None:
            raise ValueError()
        host = parsed.hostname
        if host != "localhost" and not ipaddress.ip_address(host).is_loopback:
            raise ValueError()
        return urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, "", "", ""))
    except (ValueError, TypeError):
        raise SafeFailure("invalid_base_url") from None


def request_timeout(deadline, now):
    remaining = deadline - now
    if remaining <= 0:
        raise SafeFailure("deadline")
    return min(10, remaining)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Budget:
    def __init__(self, end):
        self.end = end


class HTTPClient:
    def __init__(self, base, budget):
        self.base = validate_base(base)
        self.budget = budget
        self.cookies = http.cookiejar.CookieJar()
        self.csrf = ""
        # 禁用系统代理，拒绝所有跳转，避免凭证离开已校验的回环 origin。
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(),
                                                  urllib.request.HTTPCookieProcessor(self.cookies))

    def fork(self):
        client = HTTPClient(self.base, self.budget)
        for cookie in self.cookies:
            client.cookies.set_cookie(copy.copy(cookie))
        client.csrf = self.csrf
        return client

    def request(self, method, path, body=None, csrf=True):
        if not path.startswith("/api/") or path.startswith("//") or any(ord(c) <= 32 for c in path):
            raise SafeFailure("invalid_response")
        timeout = request_timeout(self.budget.end, time.monotonic())
        result = queue.Queue(maxsize=1)

        def perform():
            try:
                headers = {"Origin": self.base, "Content-Type": "application/json"}
                if csrf and self.csrf:
                    headers["X-CSRF-Token"] = self.csrf
                raw = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
                req = urllib.request.Request(self.base + path, data=raw, headers=headers, method=method)
                try:
                    response = self.opener.open(req, timeout=timeout)
                except urllib.error.HTTPError as error:
                    response = error
                with response:
                    status = response.getcode()
                    # 错误响应不读取或输出，避免原始网关诊断包含秘密。
                    if not 200 <= status < 300:
                        result.put((status, None))
                        return
                    data = response.read(131073)
                    if len(data) > 131072:
                        raise SafeFailure("response_too_large")
                    value = json.loads(data.decode("utf-8")) if data else None
                    result.put((status, value))
            except SafeFailure as error:
                result.put(error)
            except (ValueError, UnicodeError):
                result.put(SafeFailure("invalid_response"))
            except Exception:
                result.put(SafeFailure("transport_error"))

        # socket 超时不限制恶意滴流响应的总时间；守护线程另加整次请求 10 秒上限。
        thread = threading.Thread(target=perform, daemon=True)
        thread.start()
        thread.join(timeout)
        if thread.is_alive():
            raise SafeFailure("request_timeout")
        outcome = result.get_nowait()
        if isinstance(outcome, SafeFailure):
            raise outcome
        status, value = outcome
        if status == 200 and path == "/api/session" and isinstance(value, dict):
            self.csrf = value.get("csrfToken", "")
        return status, value


def clean_report(report):
    checks = []
    for row in report.get("checks", [])[:80]:
        if isinstance(row, dict) and row.get("stage") in STAGES and row.get("status") in ("passed", "failed", "running"):
            checks.append({"stage": row["stage"], "status": "passed" if row["status"] == "passed" else "failed"})
    cleanup = report.get("cleanup")
    if cleanup not in ("passed", "failed", "not_needed", "unconfirmed"):
        cleanup = "unconfirmed"
    failure = report.get("failure", "")
    if failure not in FAILURES:
        failure = "internal_error"
    elapsed = report.get("elapsedSeconds", 0)
    if not isinstance(elapsed, (int, float)) or not math.isfinite(elapsed) or elapsed < 0:
        elapsed = 0
    passed = report.get("passed") is True and cleanup == "passed" and failure == ""
    passed = passed and len(checks) == len(STAGES) and all(row == {"stage": stage, "status": "passed"} for row, stage in zip(checks, STAGES))
    return {"passed": passed, "checks": checks, "cleanup": cleanup, "failure": failure, "elapsedSeconds": round(elapsed, 3)}


def run_checks(base, admin_username, admin_password, factory=HTTPClient, notify=None):
    started = time.monotonic()
    budget = Budget(started + 155)
    report = {"passed": False, "checks": [], "cleanup": "not_needed", "failure": ""}
    admin = factory(base, budget)
    operator_id = None
    attempted_create = False
    admin_logged_in = False
    uncertain_mutation = False
    username = "accept_" + secrets.token_hex(8)
    initial, permanent, reset, final = ("Aa-" + secrets.token_urlsafe(32) for _ in range(4))
    current_stage = "admin_login"
    tenant = None

    def event(stage, status):
        row = {"stage": stage, "status": status}
        if status != "running":
            report["checks"].append(row)
        if notify:
            notify(row)

    def expect(stage, client, method, path, status, body=None, validate=None, csrf=True):
        nonlocal current_stage
        current_stage = stage
        event(stage, "running")
        actual, value = client.request(method, path, body, csrf=csrf)
        if actual != status:
            raise SafeFailure("unexpected_status")
        if validate is not None and not validate(value):
            raise SafeFailure("invalid_response")
        event(stage, "passed")
        return value

    def account(value, enabled=True):
        return (isinstance(value, dict) and isinstance(value.get("id"), str) and ID.fullmatch(value["id"])
                and value.get("username") == username and value.get("displayName") == "Account acceptance operator"
                and value.get("role") == "operator" and value.get("enabled") is enabled
                and isinstance(value.get("createdAt"), str) and isinstance(value.get("updatedAt"), str))

    def personal(value, must):
        return (isinstance(value, dict) and value.get("actorId") == operator_id and value.get("username") == username
                and value.get("displayName") == "Account acceptance operator" and value.get("role") == "operator"
                and value.get("tenantId") == tenant and value.get("mustChangePassword") is must
                and isinstance(value.get("csrfToken"), str) and bool(value["csrfToken"]))

    def login(stage, password, must):
        client = factory(base, budget)
        expect(stage, client, "POST", "/api/session", 200, {"username": username, "password": password}, lambda v: personal(v, must))
        return client

    try:
        data = expect("admin_login", admin, "POST", "/api/session", 200,
                      {"username": admin_username, "password": admin_password},
                      lambda v: isinstance(v, dict) and v.get("role") == "admin" and v.get("mustChangePassword") is False
                      and v.get("username") == admin_username.lower() and bool(v.get("tenantId")) and bool(v.get("csrfToken")))
        tenant, admin_logged_in = data["tenantId"], True
        expect("login_role_spoof", factory(base, budget), "POST", "/api/session", 400,
               {"username": admin_username, "password": admin_password, "role": "operator"})
        body = {"username": username, "displayName": "Account acceptance operator", "password": initial}
        attempted_create = True
        created = expect("create_operator", admin, "POST", "/api/accounts", 201, body,
                         lambda v: account(v) and v.get("mustChangePassword") is True)
        operator_id = created["id"]
        path = "/api/accounts/" + operator_id
        expect("duplicate_username", admin, "POST", "/api/accounts", 409, body)
        operator = login("initial_operator_login", initial, True)
        expect("must_change_blocks_business", operator, "GET", "/api/v1/entities", 403)
        expect("password_csrf", operator, "POST", "/api/account/password", 403,
               {"currentPassword": initial, "newPassword": permanent}, csrf=False)
        old = operator.fork()
        expect("change_initial_password", operator, "POST", "/api/account/password", 204,
               {"currentPassword": initial, "newPassword": permanent})
        expect("initial_cookie_revoked", old, "GET", "/api/session", 401)
        expect("old_password_rejected", factory(base, budget), "POST", "/api/session", 401, {"username": username, "password": initial})
        operator = login("personal_login", permanent, False)
        expect("personal_session", operator, "GET", "/api/session", 200, validate=lambda v: personal(v, False))
        expect("business_allowed", operator, "GET", "/api/v1/entities", 200)
        expect("operator_management_denied", operator, "GET", "/api/accounts", 403)
        old = operator.fork()
        expect("disable_operator", admin, "PATCH", path, 200, {"enabled": False}, lambda v: account(v, False))
        expect("disabled_cookie_revoked", old, "GET", "/api/session", 401)
        expect("enable_operator", admin, "PATCH", path, 200, {"enabled": True}, account)
        expect("enable_preserves_revocation", old, "GET", "/api/session", 401)
        operator = login("enabled_login", permanent, False)
        old = operator.fork()
        expect("reset_password", admin, "POST", path + "/reset-password", 204, {"password": reset})
        expect("reset_cookie_revoked", old, "GET", "/api/session", 401)
        expect("reset_old_password_rejected", factory(base, budget), "POST", "/api/session", 401, {"username": username, "password": permanent})
        operator = login("reset_login", reset, True)
        expect("reset_blocks_business", operator, "GET", "/api/v1/entities", 403)
        expect("change_reset_password", operator, "POST", "/api/account/password", 204,
               {"currentPassword": reset, "newPassword": final})
        operator = login("final_login", final, False)
        old = operator.fork()
        expect("logout_operator", operator, "DELETE", "/api/session", 204)
        expect("logout_cookie_revoked", old, "GET", "/api/session", 401)
        report["passed"] = True
    except Exception as error:
        event(current_stage, "failed")
        report["failure"] = error.code if isinstance(error, SafeFailure) else "internal_error"
        uncertain_mutation = report["failure"] in ("request_timeout", "transport_error") and current_stage in (
            "create_operator", "change_initial_password", "disable_operator", "enable_operator", "reset_password", "change_reset_password")
    finally:
        # 主流程预留 20 秒清理；进程监督器另有 180 秒绝对硬上限。
        budget.end = started + 175
        if attempted_create:
            report["cleanup"] = "unconfirmed"
            try:
                if operator_id is None:
                    # 创建响应不确定时，只查找本次随机用户名，绝不触碰其他账号。
                    cursor = ""
                    for _ in range(10):
                        status, data = admin.request("GET", "/api/accounts?limit=100&after=" + urllib.parse.quote(cursor, safe=""))
                        if status != 200 or not isinstance(data, dict) or not isinstance(data.get("accounts"), list):
                            raise SafeFailure("cleanup_failed")
                        for value in data["accounts"]:
                            if isinstance(value, dict) and value.get("username") == username:
                                if not account(value, value.get("enabled")):
                                    raise SafeFailure("cleanup_failed")
                                operator_id = value["id"]
                        cursor = data.get("nextCursor", "")
                        if operator_id or not cursor:
                            break
                        if not isinstance(cursor, str) or not ID.fullmatch(cursor):
                            raise SafeFailure("cleanup_failed")
                if operator_id is None:
                    raise SafeFailure("cleanup_failed")
                expect("cleanup_operator", admin, "PATCH", "/api/accounts/" + operator_id, 200,
                       {"enabled": False}, lambda v: account(v, False))
                # 超时写请求可能仍在服务端执行；已再次停用也不能声称结果最终确定。
                report["cleanup"] = "unconfirmed" if uncertain_mutation else "passed"
            except Exception:
                if current_stage != "cleanup_operator" or not report["checks"] or report["checks"][-1] != {"stage": "cleanup_operator", "status": "failed"}:
                    event("cleanup_operator", "failed")
                report["passed"] = False
                report["cleanup"] = "failed"
                if not report["failure"]:
                    report["failure"] = "cleanup_failed"
        if admin_logged_in:
            try:
                expect("admin_logout", admin, "DELETE", "/api/session", 204)
            except Exception:
                event("admin_logout", "failed")
                report["passed"] = False
                if not report["failure"]:
                    report["failure"] = "cleanup_failed"
    report["elapsedSeconds"] = time.monotonic() - started
    return clean_report(report)


def reserve_output(path):
    try:
        path = Path(path)
        path.parent.mkdir(parents=True, exist_ok=True)
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        return os.fdopen(descriptor, "w", encoding="utf-8")
    except FileExistsError:
        raise SafeFailure("output_exists") from None
    except OSError:
        raise SafeFailure("output_unavailable") from None


def worker(base, pipe):
    try:
        report = run_checks(base, os.environ["MESHOPS_WEB_ADMIN_USERNAME"], os.environ["MESHOPS_WEB_ADMIN_PASSWORD"],
                            notify=lambda row: pipe.send({"event": "check", **row}))
        pipe.send({"event": "result", "report": report})
    except Exception:
        pipe.send({"event": "result", "report": {"failure": "worker_failure"}})
    finally:
        pipe.close()


def supervise(base):
    started = time.monotonic()
    stop_at = started + 179
    context = multiprocessing.get_context("spawn")
    receiver, sender = context.Pipe(duplex=False)
    process = context.Process(target=worker, args=(base, sender), daemon=True)
    progress = []
    report = None
    try:
        process.start()
        sender.close()
        while time.monotonic() < stop_at:
            if receiver.poll(min(.25, max(.001, stop_at - time.monotonic()))):
                try:
                    message = receiver.recv()
                except EOFError:
                    break
                if message.get("event") == "result":
                    report = clean_report(message.get("report", {}))
                    break
                if message.get("event") == "check" and message.get("stage") in STAGES and message.get("status") in ("running", "passed", "failed"):
                    row = {"stage": message["stage"], "status": message["status"]}
                    if progress and progress[-1]["stage"] == row["stage"]:
                        progress[-1] = row
                    else:
                        progress.append(row)
            elif not process.is_alive():
                break
        if report is None:
            report = {"passed": False, "checks": progress, "cleanup": "unconfirmed",
                      "failure": "deadline" if time.monotonic() >= stop_at else "worker_failure"}
    except Exception:
        report = {"failure": "worker_failure", "checks": progress, "cleanup": "unconfirmed"}
    finally:
        if process.pid is not None:
            if process.is_alive():
                process.terminate()
            process.join(timeout=1)
            if process.is_alive():
                process.kill()
        receiver.close()
        sender.close()
    report["elapsedSeconds"] = time.monotonic() - started
    return clean_report(report)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:18090")
    parser.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    output = None
    try:
        base = validate_base(args.base_url)
        output = reserve_output(args.output)
        if not os.environ.get("MESHOPS_WEB_ADMIN_USERNAME") or not os.environ.get("MESHOPS_WEB_ADMIN_PASSWORD"):
            raise SafeFailure("missing_credentials")
        report = supervise(base)
    except Exception as error:
        report = clean_report({"failure": error.code if isinstance(error, SafeFailure) else "internal_error"})
    rendered = json.dumps(clean_report(report), ensure_ascii=False, indent=2) + "\n"
    if output is not None:
        try:
            with output:
                output.write(rendered)
        except OSError:
            report = clean_report({"failure": "output_unavailable"})
            rendered = json.dumps(report, ensure_ascii=False) + "\n"
    sys.stdout.write(rendered)
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    multiprocessing.freeze_support()
    raise SystemExit(main())
