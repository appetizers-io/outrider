import hashlib
import hmac
import importlib.util
import json
from pathlib import Path
import tempfile
import threading
import unittest
from urllib.error import HTTPError
from urllib.request import Request, urlopen

MODULE = Path(__file__).resolve().parents[1] / 'backend/server.py'
spec = importlib.util.spec_from_file_location('outrider_backend', MODULE)
backend = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backend)


class FakeGateway:
    def __init__(self):
        self.calls = []
        self.fail = False

    def call(self, path, payload=None):
        self.calls.append((path, payload))
        if self.fail:
            raise TimeoutError('gateway timeout')
        if payload:
            return {'id': 'task/1'}
        return {'done': True, 'result': 'No supported findings.', 'error': ''}


class InboxTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.data = Path(self.temp.name)
        self.gateway = FakeGateway()
        self.agent = self.data / 'reviewer.json'
        self.agent.write_text((backend.ROOT / 'agents/reviewer.json').read_text())
        self.inbox = backend.Inbox(self.data, self.gateway, self.agent)
        self.item_id = 'a' * 64
        backend.atomic_json(self.data / 'inbox/queue' / (self.item_id + '.json'), {
            'id': self.item_id, 'repo': 'o/r', 'pr': 1, 'created': '2026-01-01', 'prompt': 'untrusted snapshot'
        })

    def test_review_survives_restart_and_finishes_without_duplicate_dispatch(self):
        self.inbox.request_review(self.item_id)
        self.inbox.request_review(self.item_id)
        self.inbox.update_reviews()
        self.assertEqual(len(self.gateway.calls), 1)
        self.assertEqual(self.gateway.calls[0][1]['agent'], 'outrider-reviewer')
        restarted = backend.Inbox(self.data, self.gateway, self.agent)
        restarted.update_reviews()
        self.assertEqual(self.gateway.calls[-1][0], '/api/spawn/task%2F1')
        self.assertEqual(restarted.runs[self.item_id]['status'], 'completed')
        self.assertEqual(restarted.runs[self.item_id]['result'], 'No supported findings.')
        self.assertNotIn('prompt', restarted.snapshot()['items'][0])
        with self.assertRaises(ValueError):
            restarted.request_review(self.item_id)

    def test_crew_session_preferences_are_not_overridden(self):
        agent = json.loads(self.agent.read_text())
        agent['model'] = 'crew-selected-model'
        self.agent.write_text(json.dumps(agent))
        self.inbox.request_review(self.item_id)
        self.inbox.update_reviews()
        self.assertEqual(self.inbox.runs[self.item_id]['status'], 'running')
        payload = self.gateway.calls[0][1]
        for field in ('model', 'reasoning_effort', 'max_turns', 'approval_mode'):
            self.assertNotIn(field, payload)
        self.assertNotIn('model', json.loads((backend.ROOT / 'agents/reviewer.json').read_text()))

    def test_ambiguous_dispatch_is_not_retried(self):
        self.gateway.fail = True
        self.inbox.request_review(self.item_id)
        self.inbox.update_reviews()
        self.inbox.update_reviews()
        self.assertEqual(len(self.gateway.calls), 1)
        self.assertEqual(self.inbox.runs[self.item_id]['status'], 'uncertain')
        restarted = backend.Inbox(self.data, self.gateway, self.agent)
        restarted.update_reviews()
        self.assertEqual(len(self.gateway.calls), 1)

    def test_restart_during_dispatch_is_uncertain(self):
        backend.atomic_json(self.data / 'runs.json', {self.item_id: {'status': 'dispatching'}})
        restarted = backend.Inbox(self.data, self.gateway, self.agent)
        self.assertEqual(restarted.runs[self.item_id]['status'], 'uncertain')
        restarted.update_reviews()
        self.assertFalse(self.gateway.calls)

    def test_changed_agent_policy_fails_closed(self):
        agent = json.loads(self.agent.read_text())
        agent['tools'] = ['shell']
        self.agent.write_text(json.dumps(agent))
        self.inbox.request_review(self.item_id)
        self.inbox.update_reviews()
        self.assertFalse(self.gateway.calls)
        self.assertEqual(self.inbox.runs[self.item_id]['status'], 'failed')

    def test_config_validation_and_argv(self):
        for invalid in [['--help'], ['o/r;touch bad'], ['../r'], ['o/*']]:
            with self.assertRaises(ValueError):
                self.inbox.configure({'repos': invalid})
        self.inbox.configure({'repos': ['o/r'], 'interval': 45})
        calls = []
        def runner(args, **kwargs):
            calls.append((args, kwargs))
            return type('Result', (), {'returncode': 0})()
        self.inbox.runner = runner
        self.inbox.scan()
        self.assertEqual(calls[0][0][-2:], ['--repo', 'o/r'])
        self.assertNotIn('shell', calls[0][1])
        self.assertFalse(self.inbox.scanning)
        with self.assertRaises(ValueError):
            self.inbox.request_review('../state')

    def test_proxy_signatures_and_http_routes(self):
        server = backend.ThreadingHTTPServer(('127.0.0.1', 0), backend.Handler)
        server.inbox, server.secret = self.inbox, 'test-secret'
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        origin = f'http://127.0.0.1:{server.server_port}'
        with urlopen(origin + '/health') as response:
            self.assertTrue(json.load(response)['ok'])
        with self.assertRaises(HTTPError) as caught:
            urlopen(origin + '/api/status')
        self.assertEqual(caught.exception.code, 401)
        caught.exception.close()
        import time
        stamp = str(int(time.time()))
        body = json.dumps({'id': self.item_id}).encode()
        def sign(method, target, data):
            message = f'{stamp}:{method}:{target}:{hashlib.sha256(data).hexdigest()}'
            return stamp + ':' + hmac.new(b'test-secret', message.encode(), hashlib.sha256).hexdigest()
        signature = sign('POST', '/api/review', body)
        self.assertTrue(backend.verify_signature(signature, 'POST', '/api/review', body, 'test-secret'))
        self.assertFalse(backend.verify_signature(signature, 'POST', '/api/config', body, 'test-secret'))
        self.assertFalse(backend.verify_signature(signature, 'POST', '/api/review', b'{}', 'test-secret'))
        self.assertFalse(backend.verify_signature(signature, 'POST', '/api/review', body, 'test-secret', now=int(stamp) + 61))
        req = Request(origin + '/api/review', data=body, headers={'X-KiroCrew-Proxy': signature})
        with urlopen(req) as response:
            self.assertEqual(response.status, 202)
        self.assertEqual(self.inbox.runs[self.item_id]['status'], 'requested')

    def test_gateway_uses_port_scoped_cookie_and_does_not_retry_post(self):
        calls = []
        class GatewayHandler(backend.BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass
            def do_POST(self):
                calls.append((self.path, dict(self.headers)))
                if self.path.endswith('/token'):
                    value, status = {'token': 'test-token'}, 200
                else:
                    value, status = {'error': 'denied'}, 403
                self.send_response(status)
                self.end_headers()
                self.wfile.write(json.dumps(value).encode())
        server = backend.ThreadingHTTPServer(('127.0.0.1', 0), GatewayHandler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        client = backend.Gateway(f'http://127.0.0.1:{server.server_port}', 'secret')
        with self.assertRaisesRegex(RuntimeError, '403'):
            client.call('/api/spawn', {'task': 'test'})
        self.assertEqual(len(calls), 2)
        self.assertEqual({k.lower(): v for k, v in calls[0][1].items()}['x-app-secret'], 'secret')
        self.assertEqual(calls[1][1]['Cookie'], f'mc_token_{server.server_port}=test-token')

    def test_scan_error_does_not_erase_queue(self):
        self.inbox.configure({'repos': ['o/r']})
        self.inbox.runner = lambda *args, **kw: type('Result', (), {'returncode': 1, 'stderr': 'offline'})()
        with self.assertRaisesRegex(RuntimeError, 'offline'):
            self.inbox.scan()
        self.assertFalse(self.inbox.scanning)
        self.assertEqual(len(self.inbox.items()), 1)


if __name__ == '__main__':
    unittest.main()
