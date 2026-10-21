"""Outrider Crew app backend. Standard library only; no PR code is executed."""
from __future__ import annotations

import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.parse import quote, urlsplit
from urllib.request import Request, build_opener, HTTPRedirectHandler

APP = "outrider"
AGENT = "outrider-reviewer"
ROOT = Path(__file__).resolve().parents[1]
ID = re.compile(r"[a-f0-9]{64}")
REPO = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")
ACTIVE = {"requested", "dispatching", "running"}
MAX_POLL_FAILURES = 5


class GatewayRejected(RuntimeError):
    """An HTTP response from Crew, retaining its status for recovery decisions."""

    def __init__(self, status):
        self.status = status
        super().__init__(f"Crew returned HTTP {status}; check the app grant and gateway")


def normalize_repo(value):
    if not isinstance(value, str):
        raise ValueError("Enter repositories as owner/repo or GitHub repository URLs")
    value = value.strip()
    if value.startswith("https://"):
        parsed = urlsplit(value)
        if parsed.netloc != "github.com" or parsed.query or parsed.fragment:
            raise ValueError("Use a github.com repository URL without query or fragment")
        value = parsed.path.strip("/")
    if value.endswith(".git"):
        value = value[:-4]
    if not REPO.fullmatch(value) or any(p in {".", ".."} for p in value.split("/")):
        raise ValueError("Enter repositories as owner/repo or GitHub repository URLs")
    return value


def find_scanner(runner=subprocess.run):
    override = os.environ.get("OUTRIDER_BIN")
    candidates = [override] if override else [str(Path.home() / ".local/bin/outrider"), shutil.which("outrider")]
    for candidate in dict.fromkeys(candidates):
        if not candidate:
            continue
        try:
            probe = runner([candidate, "crew", "scan", "--help"], capture_output=True, text=True, timeout=10, check=False)
            if probe.returncode == 0 and "--state-dir" in probe.stdout:
                return candidate
        except (OSError, subprocess.TimeoutExpired):
            continue
    raise RuntimeError("No compatible Outrider binary found. Install this branch with task install (requires crew scan --state-dir), or set OUTRIDER_BIN to its absolute path, then scan again.")


def atomic_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    tmp = path.with_suffix(".tmp")
    with open(tmp, "w", encoding="utf-8") as out:
        os.chmod(tmp, 0o600)
        json.dump(value, out, ensure_ascii=False)
        out.flush()
        os.fsync(out.fileno())
    os.replace(tmp, path)


def read_json(path, default):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return default


def verify_signature(header, method, target, body, secret, now=None):
    if not secret:
        return False
    try:
        ts, signature = header.split(":", 1)
        stamp = int(ts)
        if abs((time.time() if now is None else now) - stamp) > 60:
            return False
        message = f"{ts}:{method}:{target}:{hashlib.sha256(body).hexdigest()}"
        expected = hmac.new(secret.encode(), message.encode(), hashlib.sha256).hexdigest()
        return hmac.compare_digest(signature.encode(), expected.encode())
    except (ValueError, TypeError):
        return False


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError("Crew gateway redirected a request; refusing to forward credentials")


class Gateway:
    def __init__(self, origin, secret):
        parsed = urlsplit(origin)
        if parsed.scheme != "http" or parsed.hostname not in {"localhost", "127.0.0.1", "::1"} or parsed.path not in {"", "/"} or parsed.username or parsed.query or parsed.fragment:
            raise ValueError("Crew gateway must be a local HTTP origin")
        self.origin, self.secret = origin.rstrip("/"), secret
        self.cookie_name = f"mc_token_{parsed.port or 80}"
        self.token = ""
        self.http = build_opener(NoRedirect())

    def _request(self, path, payload=None, headers=None):
        data = None if payload is None else json.dumps(payload).encode()
        req = Request(self.origin + path, data=data, headers={"Content-Type": "application/json", **(headers or {})})
        with self.http.open(req, timeout=30) as response:
            return json.load(response)

    def authenticate(self):
        """Acquire credentials before a review enters the dispatching state."""
        if not self.token:
            response = self._request(f"/api/apps/{APP}/token", {}, {"X-App-Secret": self.secret})
            self.token = response["token"]

    def call(self, path, payload=None):
        self.authenticate()
        try:
            return self._request(path, payload, {"Cookie": self.cookie_name + "=" + self.token})
        except HTTPError as error:
            error.close()
            if error.code == 401:
                self.token = ""  # refresh on the NEXT call; never retry a spawn
            raise GatewayRejected(error.code) from error


class Inbox:
    def __init__(self, data, gateway, agent_path=None, runner=subprocess.run):
        self.data, self.gateway, self.runner = Path(data), gateway, runner
        self.data.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.agent_path = agent_path or Path.home() / ".kiro/agents/outrider--outrider-reviewer.json"
        self.lock = threading.RLock()
        self.wake, self.stop = threading.Event(), threading.Event()
        self.config = read_json(self.data / "settings.json", {"repos": [], "interval": 60})
        self.runs = read_json(self.data / "runs.json", {})
        for run in self.runs.values():
            if run["status"] == "dispatching":
                run.update(status="uncertain", error="Backend stopped during dispatch. Check Crew tasks before retrying.")
        for run in self.runs.values():
            if run.get("delivery", {}).get("status") == "sending":
                run["delivery"]["status"] = "uncertain"
        self.save_runs()
        self.scan_requested = False
        self.scanning = False
        self.last_scan = None
        self.error = ""

    def save_runs(self):
        atomic_json(self.data / "runs.json", self.runs)

    def configure(self, body):
        repos, interval = body.get("repos"), body.get("interval", 60)
        if not isinstance(repos, list) or len(repos) > 50 :
            raise ValueError("Enter up to 50 repositories as owner/repo, one per line")
        repos = [normalize_repo(repo) for repo in repos]
        if type(interval) is not int or not 30 <= interval <= 3600:
            raise ValueError("Poll interval must be between 30 and 3600 seconds")
        folders = body.get("folders", self.config.get("folders", {}))
        if not isinstance(folders, dict) or any(not isinstance(k, str) or not isinstance(v, str) or len(v) > 200 for k, v in folders.items()):
            raise ValueError("Invalid folder mapping")
        with self.lock:
            self.config = {"repos": sorted(set(repos)), "interval": interval, "folders": {r: folders[r] for r in repos if folders.get(r)}}
            atomic_json(self.data / "settings.json", self.config)
            self.scan_requested = bool(repos)
        self.wake.set()

    def folders(self):
        rows = self.gateway.call("/api/chat/folders")
        return [{k: row.get(k, "") for k in ("id", "name", "parent_id")} for row in rows]

    def deliver(self, item_id):
        with self.lock:
            item = self.item(item_id)
            run = self.runs.get(item_id, {})
            if run.get("status") != "completed" or not run.get("result"):
                raise ValueError("Complete a review before saving findings")
            if run.get("delivery"):
                raise ValueError("Delivery already attempted. Check the Crew folder before saving again.")
            folder = self.config.get("folders", {}).get(item["repo"])
            if not folder:
                raise ValueError("Choose a Crew folder for this repository first")
            if folder not in {row["id"] for row in self.folders()}:
                raise ValueError("The selected Crew folder no longer exists")
            self.check_agent()
            run["delivery"] = {"status": "sending", "folder_id": folder}
            self.save_runs()
        try:
            slot = self.gateway.call("/api/chat/slots", {"name": "outrider-" + item_id[:24], "agent": AGENT, "agent_kind": "template", "folder_id": folder})
            key = slot.get("key")
            if not isinstance(key, str) or not key:
                raise RuntimeError("Crew did not return a chat key")
            with self.lock:
                run["delivery"]["slot"] = key
                self.save_runs()
            content = f"Outrider review: {item['repo']} #{item['pr']}\nSnapshot: {item.get('head', '')}\n{item.get('url', '')}\n\n{run['result']}"
            self.gateway.call("/api/chat/slots/" + quote(key, safe="") + "/note", {"content": content, "source": "outrider"})
            with self.lock:
                run["delivery"]["status"] = "saved"
                self.save_runs()
        except Exception as error:
            with self.lock:
                run["delivery"].update(status="uncertain", error=str(error))
                self.save_runs()
            raise

    def items(self):
        return [read_json(p, {}) for p in sorted((self.data / "inbox/queue").glob("*.json"))]

    def snapshot(self):
        with self.lock:
            items = [{k: v for k, v in item.items() if k != "prompt"} | {"run": self.runs.get(item["id"], {"status": "pending"})} for item in self.items()]
            items.sort(key=lambda x: x["created"], reverse=True)
            return {"config": self.config, "items": items, "scanning": self.scanning, "last_scan": self.last_scan, "error": self.error}

    def item(self, item_id):
        if not ID.fullmatch(item_id):
            raise ValueError("Invalid review ID")
        item = read_json(self.data / "inbox/queue" / (item_id + ".json"), None)
        if not item:
            raise ValueError("Review not found")
        return item

    def request_review(self, item_id, retry=False):
        with self.lock:
            item = self.item(item_id)
            prior = self.runs.get(item_id, {})
            if prior.get("status") in ACTIVE:
                return
            if prior and not retry:
                raise ValueError("This review already has a run; use Retry explicitly")
            if any(r.get("status") in ACTIVE for r in self.runs.values()):
                raise ValueError("A review is already active; wait for it to finish")
            # Retain previous results when the user explicitly retries.
            history = prior.get("history", []) + ([{k: v for k, v in prior.items() if k != "history"}] if prior else [])
            self.runs[item["id"]] = {"status": "requested", "history": history}
            self.save_runs()
        self.wake.set()

    def check_agent(self):
        actual = read_json(self.agent_path, None)
        expected = read_json(ROOT / "agents/reviewer.json", {})
        if actual is None or any(actual.get(key) != expected[key] for key in ("name", "tools", "allowedTools", "includeMcpJson", "mcpServers", "resources", "hooks")):
            raise RuntimeError("The installed Outrider reviewer is missing or its no-tools policy changed. Reinstall the app before reviewing.")

    def scan(self):
        with self.lock:
            repos = list(self.config["repos"])
            self.scanning = True
        try:
            if not repos:
                return
            args = [find_scanner(), "crew", "scan", "--state-dir", str(self.data / "inbox"), "--process-existing"]
            for repo in repos:
                args += ["--repo", repo]
            result = self.runner(args, cwd=self.data, capture_output=True, text=True, timeout=180, check=False)
            if result.returncode:
                raise RuntimeError("Scan failed: " + result.stderr[-3000:])
            with self.lock:
                self.last_scan, self.error = time.time(), ""
        finally:
            with self.lock:
                self.scanning = False

    def update_reviews(self):
        with self.lock:
            active = [(key, dict(run)) for key, run in self.runs.items() if run["status"] in ACTIVE]
        for item_id, run in active:
            try:
                if run["status"] == "requested":
                    self.check_agent()
                    item = self.item(item_id)
                    self.gateway.authenticate()
                    with self.lock:
                        self.runs[item_id]["status"] = "dispatching"
                        self.save_runs()
                    # Never retry an ambiguous POST automatically.
                    response = self.gateway.call("/api/spawn", {"agent": AGENT, "task": item["prompt"], "cwd": str(self.data), "backend": "kiro"})
                    task_id = response.get("id")
                    if not isinstance(task_id, str) or not task_id:
                        raise RuntimeError("Crew did not return a task ID; check Crew tasks before retrying")
                    with self.lock:
                        self.runs[item_id].update(status="running", task_id=task_id)
                        self.save_runs()
                elif run["status"] == "running":
                    response = self.gateway.call("/api/spawn/" + quote(run["task_id"], safe=""))
                    with self.lock:
                        self.runs[item_id].update(poll_failures=0, result=response.get("result", ""), error=response.get("error", ""), awaiting_approval=response.get("awaiting_approval", False))
                        if response.get("done"):
                            self.runs[item_id]["status"] = "failed" if response.get("error") or response.get("stopped") else "completed"
                        self.save_runs()
            except Exception as error:
                with self.lock:
                    current = self.runs[item_id]
                    if current["status"] == "dispatching":
                        rejected = isinstance(error, GatewayRejected) and 400 <= error.status < 500 and error.status != 408
                        current["status"] = "failed" if rejected else "uncertain"
                    elif current["status"] == "requested":
                        current["status"] = "failed"
                    elif current["status"] == "running":
                        failures = current.get("poll_failures", 0) + 1
                        current["poll_failures"] = failures
                        gone = isinstance(error, GatewayRejected) and error.status in (404, 410)
                        if gone or failures >= MAX_POLL_FAILURES:
                            current["status"] = "uncertain"
                    current["error"] = str(error)
                    self.save_runs()

    def work(self):
        next_scan = 0.0
        while not self.stop.is_set():
            try:
                with self.lock:
                    scan = bool(self.config["repos"]) and (self.scan_requested or time.monotonic() >= next_scan)
                    self.scan_requested = False
                if scan:
                    try:
                        self.scan()
                    finally:
                        next_scan = time.monotonic() + self.config["interval"]
                self.update_reviews()
            except Exception as error:
                with self.lock:
                    self.error = str(error)
            self.wake.wait(3)
            self.wake.clear()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass  # Do not log PR data, auth headers, or request bodies.

    def reply(self, status, value):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        if self.path == "/health" and self.command == "GET":
            self.reply(200, {"ok": True})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 <= length <= 16384:
                self.reply(413, {"error": "Request too large"})
                return
            body = self.rfile.read(length)
            if not verify_signature(self.headers.get("X-KiroCrew-Proxy", ""), self.command, self.path, body, self.server.secret):
                self.reply(401, {"error": "A signed Crew gateway request is required"})
                return
            inbox = self.server.inbox
            if self.command == "GET" and self.path == "/api/status":
                self.reply(200, inbox.snapshot())
                return
            if self.command == "GET" and self.path == "/api/folders":
                self.reply(200, inbox.folders())
                return
            if self.command != "POST":
                self.reply(404, {"error": "Not found"})
                return
            payload = json.loads(body or b"{}")
            if not isinstance(payload, dict):
                raise ValueError("Expected an object")
            if self.path == "/api/config":
                inbox.configure(payload)
            elif self.path == "/api/scan":
                with inbox.lock:
                    inbox.scan_requested = True
                inbox.wake.set()
            elif self.path == "/api/deliver":
                if not isinstance(payload.get("id"), str):
                    raise ValueError("Review ID required")
                inbox.deliver(payload["id"])
            elif self.path == "/api/review":
                if not isinstance(payload.get("id"), str):
                    raise ValueError("Review ID required")
                inbox.request_review(payload["id"], payload.get("retry") is True)
            else:
                self.reply(404, {"error": "Not found"})
                return
            self.reply(202, {"ok": True})
        except (ValueError, TypeError) as error:
            self.reply(400, {"error": str(error)})
        except Exception:
            self.reply(500, {"error": "Outrider could not complete this request"})


def main():
    os.umask(0o077)
    home = Path(os.environ.get("KIROCREW_HOME", Path.home() / ".kiro/crew"))
    secret = os.environ.get("KIROCREW_PROXY_SECRET", "")
    if not secret:
        raise RuntimeError("Start this backend through Crew (missing proxy secret)")
    origin = os.environ.get("KIROCREW_GATEWAY_ORIGIN", "")
    if not origin:
        raise RuntimeError("Crew did not supply its gateway origin; update Crew")
    gateway = Gateway(origin, secret)
    inbox = Inbox(home / "apps/outrider/data", gateway)
    server = ThreadingHTTPServer(("127.0.0.1", int(os.environ["PORT"])), Handler)
    server.inbox, server.secret = inbox, secret
    worker = threading.Thread(target=inbox.work, daemon=True)
    worker.start()
    try:
        server.serve_forever()
    finally:
        inbox.stop.set()
        inbox.wake.set()
        server.server_close()


if __name__ == "__main__":
    main()
