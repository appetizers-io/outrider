import hashlib
import hmac
import importlib.util
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch
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

    def authenticate(self):
        pass

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
        self.scanner_patch = patch.object(backend, 'find_scanner', return_value='/new/outrider')
        self.scanner_patch.start()
        self.addCleanup(self.scanner_patch.stop)
        self.inbox = backend.Inbox(self.data, self.gateway, self.agent)
        self.item_id = 'a' * 64
        backend.atomic_json(self.data / 'inbox/queue' / (self.item_id + '.json'), {
            'id': self.item_id, 'repo': 'o/r', 'pr': 1, 'created': '2026-01-01T00:00:00Z', 'prompt': 'untrusted snapshot'
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

    def test_auth_failure_happens_before_dispatch(self):
        def fail_auth():
            raise TimeoutError('auth unavailable')
        self.gateway.authenticate = fail_auth
        self.inbox.request_review(self.item_id)
        self.inbox.update_reviews()
        self.assertFalse(self.gateway.calls)
        self.assertEqual(self.inbox.runs[self.item_id]['status'], 'failed')

    def test_spawn_http_status_classification(self):
        for code, expected in [(401, 'failed'), (403, 'failed'), (429, 'failed'), (408, 'uncertain'), (500, 'uncertain')]:
            with self.subTest(code=code):
                self.inbox.runs.clear()
                def reject(*args, **kwargs):
                    raise backend.GatewayRejected(code)
                self.gateway.call = reject
                self.inbox.request_review(self.item_id)
                self.inbox.update_reviews()
                self.assertEqual(self.inbox.runs[self.item_id]['status'], expected)

    def test_lost_task_releases_active_slot(self):
        for code in (404, 410):
            with self.subTest(code=code):
                self.inbox.runs[self.item_id] = {'status': 'running', 'task_id': 'lost'}
                def reject(*args, **kwargs):
                    raise backend.GatewayRejected(code)
                self.gateway.call = reject
                self.inbox.update_reviews()
                self.assertEqual(self.inbox.runs[self.item_id]['status'], 'uncertain')
                self.assertEqual(self.inbox.runs[self.item_id]['task_id'], 'lost')
                self.inbox.request_review(self.item_id, retry=True)
                self.assertEqual(self.inbox.runs[self.item_id]['status'], 'requested')

    def test_repeated_poll_errors_are_bounded_and_success_resets_counter(self):
        self.inbox.runs[self.item_id] = {'status': 'running', 'task_id': 'task1'}
        self.gateway.fail = True
        for _ in range(backend.MAX_POLL_FAILURES - 1):
            self.inbox.update_reviews()
        self.assertEqual(self.inbox.runs[self.item_id]['status'], 'running')
        self.gateway.fail = False
        self.gateway.call = lambda *args: {'done': False}
        self.inbox.update_reviews()
        self.assertEqual(self.inbox.runs[self.item_id]['poll_failures'], 0)
        self.gateway.call = FakeGateway.call.__get__(self.gateway)
        self.gateway.fail = True
        for _ in range(backend.MAX_POLL_FAILURES):
            self.inbox.update_reviews()
        self.assertEqual(self.inbox.runs[self.item_id]['status'], 'uncertain')

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

    def test_folder_delivery_and_no_duplicate(self):
        self.inbox.runs[self.item_id] = {'status': 'completed', 'result': 'Reviewed.'}
        self.inbox.configure({'repos': ['o/r'], 'folders': {'o/r': 'folder-1'}})
        def call(path, payload=None):
            self.gateway.calls.append((path, payload))
            if path == '/api/chat/folders':
                return [{'id': 'folder-1', 'name': 'Project'}]
            if path == '/api/chat/slots':
                return {'key': 'review-chat'}
            return {'ok': True}
        self.gateway.call = call
        self.inbox.deliver(self.item_id)
        self.assertEqual(self.inbox.runs[self.item_id]['delivery']['status'], 'saved')
        create = self.gateway.calls[1][1]
        self.assertEqual(create['folder_id'], 'folder-1')
        self.assertNotIn('model', create)
        self.assertEqual(self.gateway.calls[-1][0], '/api/chat/slots/review-chat/note')
        with self.assertRaises(ValueError):
            self.inbox.deliver(self.item_id)
        self.assertEqual(len(self.gateway.calls), 3)

    def test_delivery_timeout_is_not_retried_after_restart(self):
        self.inbox.runs[self.item_id] = {'status': 'completed', 'result': 'Reviewed.'}
        self.inbox.configure({'repos': ['o/r'], 'folders': {'o/r': 'folder-1'}})
        def call(path, payload=None):
            if path == '/api/chat/folders':
                return [{'id': 'folder-1'}]
            raise TimeoutError('lost response')
        self.gateway.call = call
        with self.assertRaises(TimeoutError):
            self.inbox.deliver(self.item_id)
        restarted = backend.Inbox(self.data, self.gateway, self.agent)
        self.assertEqual(restarted.runs[self.item_id]['delivery']['status'], 'uncertain')
        with self.assertRaises(ValueError):
            restarted.deliver(self.item_id)

    def test_wizard_preserves_imported_task_settings_and_crew_choices(self):
        self.inbox.config = {'repos': [], 'interval': 60}
        with patch.object(backend.runtime, 'doctor', return_value={'ready': True}), patch.object(self.inbox, 'checkout', return_value={'remotes': [{'name': 'upstream', 'repo': 'o/r'}]}), patch.object(backend, 'validate_repo_settings'):
            self.inbox.finish_onboarding({'repo': 'o/r', 'folder': 'folder', 'remote': 'upstream', 'automatic': True, 'trust': 'trust_reads', 'reviewer': 'chosen', 'maximum': 2, 'settings': {'push': 'ask'}, 'polling': {'lookback_hours': 4, 'interval_seconds': 90}})
        self.assertEqual(self.inbox.config['repo_settings']['o/r'], {'push': 'ask'})
        self.assertEqual(self.inbox.config['validation_remotes']['o/r'], 'upstream')
        self.assertEqual(self.inbox.config['session_trust_default'], 'trust_reads')
        self.assertEqual(self.inbox.config['lookback_hours'], 4)
        self.assertEqual(self.inbox.config['interval'], 90)
        self.assertFalse(self.inbox.config['paused'])
        self.assertTrue((self.inbox.data / 'onboarding.json').exists())

    def test_wizard_cannot_replace_existing_repositories(self):
        self.inbox.config = {'repos': ['o/existing'], 'interval': 60}
        with patch.object(backend.runtime, 'doctor', return_value={'ready': True}):
            with self.assertRaisesRegex(ValueError, 'Add repository'):
                self.inbox.finish_onboarding({'repo': 'o/new'})
        self.assertEqual(self.inbox.config['repos'], ['o/existing'])

    def test_wizard_does_not_save_when_doctor_fails(self):
        self.inbox.config = {'repos': [], 'interval': 60}
        with patch.object(backend.runtime, 'doctor', return_value={'ready': False}):
            with self.assertRaisesRegex(ValueError, 'Doctor'):
                self.inbox.finish_onboarding({'repo': 'o/new'})
        self.assertFalse((self.inbox.data / 'onboarding.json').exists())
        self.assertEqual(self.inbox.config['repos'], [])

    def test_manual_review_rejects_invalid_urls_and_unconfigured_repos(self):
        for url in ['https://evil.test/o/r/pull/1', 'https://github.com/o/r/issues/1', 'https://github.com/o/r/pull/0', 'https://github.com/unknown/repo/pull/1']:
            with self.assertRaises(ValueError):
                self.inbox.request_manual({'url': url})

    def test_manual_review_reuses_capture_and_honors_capacity(self):
        item = self.inbox.items()[0]
        self.inbox.config.update(repos=[item['repo']], folders={item['repo']: 'folder'}, validation_remotes={item['repo']: 'upstream'})
        result = type('Result', (), {'returncode': 0, 'stdout': json.dumps({'id': item['id']})})()
        self.inbox.runner = lambda *a, **kw: result
        with patch.object(self.inbox, 'active_sessions', return_value=1), patch.object(self.inbox, 'request_validation') as start:
            self.inbox.capture_manual(item['repo'], item['pr'], backend.scan_config(self.inbox.config))
            self.assertEqual(self.inbox.manual['status'], 'queued')
            start.assert_not_called()
        self.inbox.validations[item['id']] = {'status': 'completed'}
        with patch.object(self.inbox, 'request_validation') as start:
            self.inbox.capture_manual(item['repo'], item['pr'], backend.scan_config(self.inbox.config))
            self.assertEqual(self.inbox.manual['status'], 'existing')
            start.assert_not_called()

    def test_repository_urls(self):
        self.inbox.configure({'repos': ['https://github.com/org/repo.git/', 'org/repo']})
        self.assertEqual(self.inbox.config['repos'], ['org/repo'])
        for invalid in ['https://evil.test/org/repo', 'https://github.com/org/repo/pull/1', 'https://github.com/org/repo?x=1']:
            with self.assertRaises(ValueError):
                self.inbox.configure({'repos': [invalid]})

    def test_scanner_prefers_compatible_local_install(self):
        self.scanner_patch.stop()
        calls = []
        def run(args, **kwargs):
            calls.append(args)
            return type('Result', (), {'returncode': 0, 'stdout': '--state-dir --config-format --pr'})()
        with patch.dict(backend.os.environ, {'USERPROFILE': str(Path.home())}, clear=True), patch.object(backend.runtime.shutil, 'which', return_value='/host/outrider'), patch.object(Path, 'is_file', return_value=True):
            self.assertEqual(backend.find_scanner(run), str(Path('/host/outrider').resolve()))
        self.assertEqual(len(calls), 4)
        with patch.dict(backend.os.environ, {'OUTRIDER_BIN': '/missing/out'}, clear=True):
            with self.assertRaisesRegex(RuntimeError, 'No compatible'):
                backend.find_scanner(lambda *a, **kw: type('Result', (), {'returncode': 0, 'stdout': 'old CLI'})())

    def test_gui_path_includes_cli_directories_and_preserves_auth_environment(self):
        original = {'USERPROFILE': str(Path.home()), 'PATH': backend.os.pathsep.join(['/crew/bin', '/usr/bin']), 'GH_CONFIG_DIR': '/custom/gh', 'GH_TOKEN': 'test-only'}
        with patch.dict(backend.os.environ, original, clear=True):
            env = backend.scanner_environment()
            self.assertEqual(backend.os.environ['PATH'], original['PATH'])
        entries = env['PATH'].split(backend.os.pathsep)
        self.assertEqual(entries[:2], ['/crew/bin', '/usr/bin'])
        for directory in ([str(Path.home() / '.local/bin')] + ([] if backend.os.name == 'nt' else ['/opt/homebrew/bin', '/usr/local/bin'])):
            self.assertIn(directory, entries)
        self.assertEqual(entries.count('/usr/bin'), 1)
        self.assertEqual(env['GH_CONFIG_DIR'], '/custom/gh')
        self.assertEqual(env['GH_TOKEN'], 'test-only')

    def test_per_repo_options_use_standalone_validation_and_preserve_old_config_on_error(self):
        options = {'o/r': {'triggers': {'mentions': {'enabled': False}}, 'workflows': [{'name': 'review', 'match': [{}], 'steps': ['Inspect APIs']}]}}
        calls = []
        def validate(args, **kwargs):
            calls.append((args, json.loads(Path(args[-1]).read_text())))
            return type('Result', (), {'returncode': 0})()
        with patch.object(backend.subprocess, 'run', side_effect=validate):
            self.inbox.configure({'repos': ['o/r', 'other/repo'], 'repo_settings': options})
        self.assertEqual(calls[0][0][1:3], ['config', 'check'])
        generated = backend.scan_config(self.inbox.config)
        self.assertEqual(generated['overrides'], [{'match': [{'repo': 'o/r'}], **options['o/r']}])
        prior = json.loads((self.data / 'settings.json').read_text())
        with patch.object(backend.subprocess, 'run', return_value=type('Result', (), {'returncode': 1, 'stderr': 'invalid trigger'})()):
            with self.assertRaisesRegex(ValueError, 'invalid trigger'):
                self.inbox.configure({'repos': ['o/r'], 'repo_settings': options})
        self.assertEqual(json.loads((self.data / 'settings.json').read_text()), prior)
        for field in ['model', 'agent', 'tool_gate', 'sandbox']:
            with self.assertRaisesRegex(ValueError, 'Session settings belong to Crew'):
                backend.validate_repo_settings({'o/r': {field: 'value'}})

    def test_ai_setup_only_returns_validated_draft_and_keeps_crew_preferences(self):
        agent = self.agent.with_name('outrider--outrider-setup.json')
        agent.write_text((backend.ROOT / 'agents/setup.json').read_text())
        proposal = {'yaml': 'triggers: {}\n', 'settings': {'triggers': {}}}
        with patch.object(backend, 'convert_settings', return_value=proposal):
            state = self.inbox.generate_setup({'repo': 'o/r', 'intent': 'Watch mentions'})
            self.assertEqual(state['status'], 'running')
            payload = self.gateway.calls[-1][1]
            self.assertEqual(payload['agent'], 'outrider-setup')
            for field in ['model', 'reasoning_effort', 'approval_mode', 'max_turns']:
                self.assertNotIn(field, payload)
            with self.assertRaises(ValueError):
                self.inbox.generate_setup({'repo': 'o/r', 'intent': 'Duplicate'})
            self.gateway.call = lambda *a: {'done': True, 'result': '```yaml\ntriggers: {}\n```'}
            state = self.inbox.setup_status()
        self.assertEqual(state['status'], 'completed')
        self.assertEqual(state['proposal'], proposal)
        self.assertFalse((self.data / 'settings.json').exists())

    def test_ai_setup_timeout_is_uncertain_and_not_retried(self):
        self.agent.with_name('outrider--outrider-setup.json').write_text((backend.ROOT / 'agents/setup.json').read_text())
        self.gateway.fail = True
        with patch.object(backend, 'convert_settings', return_value={'yaml': '{}'}):
            self.assertEqual(self.inbox.generate_setup({'repo': 'o/r', 'intent': 'Watch mentions'})['status'], 'uncertain')
        self.inbox.setup_status()
        self.assertEqual(len(self.gateway.calls), 1)
        self.assertFalse((self.data / 'settings.json').exists())

    def test_ownership_overrides_are_scoped_to_their_repository(self):
        options = {'o/r': {'workflows': [{'name': 'others'}], 'overrides': [{'match': [{'prs': 'own'}], 'workflows': [], 'triggers': {'mentions': {'enabled': False}}}]}, 'o/other': {'prompts': {'extra': 'Other repository'}}}
        with patch.object(backend.subprocess, 'run', return_value=type('Result', (), {'returncode': 0})()):
            backend.validate_repo_settings(options)
        config = backend.scan_config({'repos': ['o/r', 'o/other'], 'repo_settings': options})
        self.assertEqual(config['overrides'][1]['match'], [{'repo': 'o/r', 'prs': 'own'}])
        self.assertEqual(config['overrides'][1]['workflows'], [])
        self.assertEqual(config['overrides'][2]['match'], [{'repo': 'o/other'}])
        for override in [
            {'match': [{'prs': 'own'}], 'agent': 'claude'},
            {'match': [{'repo': 'other/repo'}], 'prompts': {}},
            {'match': [{'prs': 'own'}], 'overrides': []},
        ]:
            with self.assertRaises(ValueError):
                backend.validate_repo_settings({'o/r': {'overrides': [override]}})

    def test_pause_persists_and_manual_scan_is_allowed(self):
        self.inbox.config['repos'] = ['o/r']
        self.assertTrue(self.inbox.scan_due())
        self.inbox.set_paused(True)
        self.assertFalse(self.inbox.scan_due())
        restarted = backend.Inbox(self.data, self.gateway, self.agent)
        self.assertTrue(restarted.config['paused'])
        self.assertFalse(restarted.scan_due())
        restarted.scan_requested = True
        self.assertTrue(restarted.scan_due())
        restarted.set_paused(False)
        self.assertTrue(restarted.scan_due())

    def test_partial_scan_is_not_a_successful_full_scan(self):
        self.inbox.config['repos'] = ['o/r']
        self.inbox.runner = lambda *a, **kw: type('Result', (), {'returncode': 0, 'stdout': '{"complete": false}'})()
        self.inbox.scan()
        self.assertTrue(self.inbox.snapshot()['scan_partial'])
        self.assertIsNone(self.inbox.last_scan)
        self.assertFalse(self.inbox.scanning)
        self.assertIsNone(self.inbox.scan_started)
        restarted = backend.Inbox(self.data, self.gateway, self.agent)
        self.assertTrue(restarted.scan_partial)

    def test_scan_timeout_is_actionable_and_persisted(self):
        self.inbox.config['repos'] = ['o/r']
        def timeout(*args, **kwargs):
            raise backend.subprocess.TimeoutExpired(['sensitive-path'], 180)
        self.inbox.runner = timeout
        with self.assertRaisesRegex(RuntimeError, 'Scan timed out'):
            self.inbox.scan()
        self.assertNotIn('sensitive-path', self.inbox.error)
        self.assertFalse(self.inbox.scanning)
        self.assertIsNone(self.inbox.scan_started)
        self.assertIn('Scan timed out', backend.Inbox(self.data, self.gateway, self.agent).error)

    def enable_automatic(self, maximum=1):
        self.inbox.config.update(repos=['o/r'], automatic={'o/r': True}, automatic_since={'o/r': '2025-01-01T00:00:00+00:00'}, max_sessions=maximum, folders={'o/r': 'folder'}, validation_remotes={'o/r': 'upstream'})

    def test_automatic_dispatch_reserves_capacity_and_does_not_duplicate(self):
        self.enable_automatic()
        with patch.object(backend.threading, 'Thread') as thread:
            self.inbox.dispatch_automatic()
            self.inbox.dispatch_automatic()
        self.assertEqual(thread.call_count, 1)
        self.assertEqual(self.inbox.active_sessions(), 1)
        self.assertEqual(self.inbox.validations[self.item_id]['status'], 'preparing')

    def test_automatic_opt_out_pause_backlog_and_dismissal(self):
        self.enable_automatic()
        with patch.object(self.inbox, 'request_validation') as launch:
            self.inbox.config['paused'] = True
            self.inbox.dispatch_automatic()
            self.inbox.config['paused'] = False
            self.inbox.config['automatic']['o/r'] = False
            self.inbox.dispatch_automatic()
            self.inbox.config['automatic']['o/r'] = True
            self.inbox.config['automatic_since']['o/r'] = '2027-01-01T00:00:00+00:00'
            self.inbox.dispatch_automatic()
            self.inbox.config['automatic_since']['o/r'] = '2025-01-01T00:00:00+00:00'
            self.inbox.dismiss(self.item_id)
            self.inbox.dispatch_automatic()
            launch.assert_not_called()
        self.assertTrue(self.inbox.snapshot()['items'][0]['dismissed'])
        self.assertIn(self.item_id, backend.Inbox(self.data, self.gateway, self.agent).dismissed)

    def test_same_pr_is_serialized_even_with_spare_capacity(self):
        self.enable_automatic(3)
        self.inbox.validations['existing'] = {'status': 'started', 'repo': 'o/r', 'pr': 1}
        with self.assertRaisesRegex(ValueError, 'PR is already active'):
            self.inbox.request_validation(self.item_id)

    def test_same_pr_guard_covers_both_review_types(self):
        self.enable_automatic(3)
        self.inbox.runs[self.item_id] = {'status': 'running'}
        with self.assertRaisesRegex(ValueError, 'PR is already active'):
            self.inbox.request_validation(self.item_id)
        self.inbox.runs.clear()
        self.inbox.validations['existing'] = {'status': 'started', 'repo': 'o/r', 'pr': 1}
        with self.assertRaisesRegex(ValueError, 'PR is already active'):
            self.inbox.request_review(self.item_id)

    def test_completed_chat_releases_capacity_but_unknown_status_does_not(self):
        self.inbox.validations[self.item_id] = {'status': 'started', 'slot': 's', 'repo': 'o/r', 'pr': 1, 'started_at': 1}
        self.gateway.call = lambda *a: {'running': False, 'queue': [], 'messages': []}
        self.inbox.track_validations()
        self.assertEqual(self.inbox.active_sessions(), 1)
        self.gateway.call = lambda *a: {'running': False, 'queue': [], 'messages': [{'role': 'assistant', 'content': 'Done'}]}
        self.inbox.track_validations()
        self.assertEqual(self.inbox.active_sessions(), 0)
        self.assertEqual(self.inbox.validations[self.item_id]['status'], 'completed')

    def test_closed_session_releases_capacity_without_automatic_replay(self):
        self.enable_automatic()
        for status in (404, 410):
            self.inbox.validations[self.item_id] = {'status': 'started', 'slot': 'closed', 'repo': 'o/r', 'pr': 1, 'worktree': '/retained'}
            def missing(*args):
                raise backend.GatewayRejected(status)
            self.gateway.call = missing
            self.inbox.track_validations()
            self.assertEqual(self.inbox.active_sessions(), 0)
            self.assertEqual(self.inbox.validations[self.item_id]['status'], 'stopped')
            self.assertTrue(self.inbox.validations[self.item_id]['session_missing'])
            with patch.object(self.inbox, 'request_validation') as launch:
                self.inbox.dispatch_automatic()
                launch.assert_not_called()
            restarted = backend.Inbox(self.data, self.gateway, self.agent)
            self.assertEqual(restarted.validations[self.item_id]['status'], 'stopped')

    def test_unreachable_session_retains_capacity(self):
        for error in (backend.GatewayRejected(403), backend.GatewayRejected(500), TimeoutError()):
            self.inbox.validations[self.item_id] = {'status': 'started', 'slot': 's'}
            with patch.object(self.gateway, 'call', side_effect=error):
                self.inbox.track_validations()
            self.assertEqual(self.inbox.active_sessions(), 1)
            self.assertEqual(self.inbox.validations[self.item_id]['status'], 'started')

    def test_stopped_session_retry_is_explicit_and_retains_history(self):
        self.enable_automatic()
        self.inbox.validations[self.item_id] = {'status': 'stopped', 'slot': 'old', 'worktree': '/retained', 'attempt': 1}
        with self.assertRaises(ValueError):
            self.inbox.request_validation(self.item_id)
        with patch.object(backend.threading, 'Thread'):
            self.inbox.request_validation(self.item_id, retry=True)
        record = self.inbox.validations[self.item_id]
        self.assertEqual(record['attempt'], 2)
        self.assertEqual(record['history'][0]['slot'], 'old')
        self.assertEqual(record['history'][0]['worktree'], '/retained')

    def test_completed_session_remains_completed_when_closed_later(self):
        self.inbox.validations[self.item_id] = {'status': 'completed', 'slot': 'old'}
        with patch.object(self.gateway, 'call') as call:
            self.inbox.track_validations()
            call.assert_not_called()
        self.assertEqual(self.inbox.validations[self.item_id]['status'], 'completed')

    def test_latest_item_per_pr_wins_automatic_dispatch(self):
        self.enable_automatic()
        newer = self.inbox.item(self.item_id) | {'id': 'b' * 64, 'created': '2026-02-01T00:00:00Z'}
        backend.atomic_json(self.data / 'inbox/queue' / (newer['id'] + '.json'), newer)
        with patch.object(self.inbox, 'request_validation') as launch:
            self.inbox.dispatch_automatic()
        launch.assert_called_once_with(newer['id'])

    def test_global_defaults_and_repository_overrides(self):
        self.inbox.config.update(automatic_default=True, automatic={'o/off': False}, session_trust_default='trust_reads', session_trust={'o/normal': 'normal'})
        self.assertTrue(self.inbox.automatic_enabled('o/new'))
        self.assertFalse(self.inbox.automatic_enabled('o/off'))
        self.assertEqual(self.inbox.session_trust('o/new'), 'trust_reads')
        self.assertEqual(self.inbox.session_trust('o/normal'), 'normal')
        self.inbox.configure({'repos': ['o/r'], 'automatic_default': False, 'lookback_hours': 4, 'reviewer_default': 'reviewer', 'session_trust_default': 'inherit'})
        self.assertEqual(backend.scan_config(self.inbox.config)['lookback_hours'], 4)
        self.assertEqual(self.inbox.config['reviewer_default'], 'reviewer')
        with self.assertRaises(ValueError):
            self.inbox.configure({'repos': ['o/r'], 'session_trust_default': 'yolo'})

    def test_clear_queue_scopes_and_keeps_active_work(self):
        self.inbox.config['repos'] = ['o/r', 'o/other']
        other = self.inbox.item(self.item_id) | {'id': 'b' * 64, 'repo': 'o/other'}
        backend.atomic_json(self.data / 'inbox/queue' / (other['id'] + '.json'), other)
        self.inbox.validations[self.item_id] = {'status': 'started'}
        self.assertEqual(self.inbox.clear_queue('o/r')['cleared'], 0)
        self.assertEqual(self.inbox.clear_queue('o/other')['cleared'], 1)
        self.assertNotIn(self.item_id, self.inbox.dismissed)
        self.assertIn(other['id'], self.inbox.dismissed)

    def test_materialized_own_mcp_does_not_grant_tools(self):
        expected = json.loads((backend.ROOT / 'agents/reviewer.json').read_text())
        actual = expected | {'mcpServers': {'outrider:outrider': {'command': 'python3'}}}
        self.assertTrue(backend.no_tools_policy_matches(actual, expected))
        self.assertFalse(backend.no_tools_policy_matches(actual | {'tools': ['@outrider:outrider']}, expected))
        self.assertFalse(backend.no_tools_policy_matches(actual | {'hooks': {}}, expected))

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
        with patch.object(backend, "find_scanner", return_value="/new/outrider"):
            self.inbox.scan()
        self.assertEqual(calls[0][0][-2:], ['--repo', 'o/r'])
        self.assertNotIn('shell', calls[0][1])
        self.assertIn(str(Path.home() / '.local/bin'), calls[0][1]['env']['PATH'].split(backend.os.pathsep))
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
                self.rfile.read(int(self.headers.get("Content-Length", "0")))
                calls.append((self.path, dict(self.headers)))
                if self.path.endswith('/token'):
                    value, status = {'token': 'test-token'}, 200
                else:
                    value, status = {'error': 'denied'}, 403
                body = json.dumps(value).encode()
                self.send_response(status)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
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

class LocalValidationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.gateway = FakeGateway()
        self.inbox = backend.Inbox(self.root / 'data', self.gateway)
        self.item = {'id': 'a' * 64, 'repo': 'o/r', 'head': 'b' * 40, 'pr': 2, 'url': 'https://github.com/o/r/pull/2', 'workflow': 'test', 'scope': None}
        self.inbox.validations[self.item['id']] = {'status': 'preparing'}
        self.prompt_patch = patch.object(self.inbox, 'session_prompt', return_value='Shared task. Run focused tests')
        self.prompt_mock = self.prompt_patch.start()
        self.addCleanup(self.prompt_patch.stop)
        self.calls = []
        def gateway(path, payload=None):
            self.calls.append((path, payload))
            if path == '/api/chat/folders':
                return [{'id': 'parent', 'name': 'Parent', 'project_dir': str(self.root)}, {'id': 'child', 'parent_id': 'parent', 'name': 'Child'}]
            if path == '/api/chat/slots':
                return {'key': 'outrider-local-test'}
            return {'ok': True}
        self.gateway.call = gateway
        def git(cwd, *args):
            if args == ('rev-parse', '--show-toplevel'): return str(self.root)
            if args == ('remote',): return 'origin\nupstream'
            if args[:2] == ('remote', 'get-url'): return 'git@github.com:' + ('me/fork' if args[2] == 'origin' else 'o/r') + '.git'
            if args == ('rev-parse', 'refs/outrider/' + self.item['id']): return self.item['head']
            if args[:2] == ('worktree', 'list'): return 'worktree ' + str(self.root) + '\0\0'
            return ''
        self.git_patch = patch.object(self.inbox, 'git', side_effect=git)
        self.git = self.git_patch.start()
        self.addCleanup(self.git_patch.stop)

    def test_inherited_folder_checkout_and_crew_owned_settings(self):
        self.inbox.validate_local(self.item, 'child', 'upstream', {'workflows': [{'name': 'test', 'steps': ['Run focused tests']}]})
        result = self.inbox.validations[self.item['id']]
        self.assertEqual(result['status'], 'started')
        expected = str(self.root / '.claude/worktrees' / ('outrider-' + self.item['id'][:24]))
        self.assertEqual(result['worktree'], expected)
        self.git.assert_any_call(str(self.root), 'worktree', 'add', '-b', 'review/outrider-' + self.item['id'][:24], expected, self.item['head'])
        create = next(payload for path, payload in self.calls if path == '/api/chat/slots')
        self.assertEqual(create, {'name': 'outrider-local-' + self.item['id'][:24], 'folder_id': 'child', 'title': 'r#2 · PR review'})
        project = next(payload for path, payload in self.calls if path.endswith('/project'))
        self.assertEqual(project, {'project': expected})
        message = next(payload for path, payload in self.calls if path == '/api/chat?ws=1')
        self.assertEqual(set(message), {'slot', 'message'})
        self.assertIn('Run focused tests', message['message'])
        self.assertIn(self.item['head'], message['message'])

    def test_selected_member_and_session_trust_apply_before_send(self):
        self.inbox.config.update(reviewer_default='global', reviewers={'o/r': 'reviewer'}, session_trust_default='normal', session_trust={'o/r': 'trust_reads'})
        self.item['title'] = 'Fix transfer logs'
        self.item['workflow'] = ''
        self.inbox.validate_local(self.item, 'child', 'upstream', {})
        create = next(payload for path, payload in self.calls if path == '/api/chat/slots')
        self.assertEqual(create['agent'], 'reviewer')
        self.assertEqual(create['agent_kind'], 'member')
        self.assertEqual(create['title'], 'r#2 · Fix transfer logs')
        mode_index = next(i for i, (path, payload) in enumerate(self.calls) if path == '/api/chat/mode')
        send_index = next(i for i, (path, payload) in enumerate(self.calls) if path == '/api/chat?ws=1')
        self.assertLess(mode_index, send_index)
        self.assertEqual(self.calls[mode_index][1]['mode'], 'trust_reads')

    def test_retry_uses_new_chat_and_worktree(self):
        self.inbox.validations[self.item['id']]['attempt'] = 2
        self.item['workflow'] = ''
        self.inbox.validate_local(self.item, 'child', 'upstream', {})
        record = self.inbox.validations[self.item['id']]
        self.assertEqual(record['status'], 'started')
        self.assertTrue(record['worktree'].endswith('-retry-2'))
        create = next(payload for path, payload in self.calls if path == '/api/chat/slots')
        self.assertTrue(create['name'].endswith('-retry-2'))

    def test_wrong_remote_never_creates_worktree_or_chat(self):
        self.inbox.validate_local(self.item, 'child', 'origin', {})
        self.assertEqual(self.inbox.validations[self.item['id']]['status'], 'failed')
        self.assertFalse(any(path == '/api/chat/slots' for path, _ in self.calls))
        self.assertFalse(any(c.args[1:3] == ('worktree', 'add') for c in self.git.call_args_list))

    def test_moved_head_never_dispatches(self):
        original = self.git.side_effect
        self.git.side_effect = lambda cwd, *args: 'c' * 40 if args == ('rev-parse', 'refs/outrider/' + self.item['id']) else original(cwd, *args)
        self.inbox.validate_local(self.item, 'child', 'upstream', {})
        self.assertEqual(self.inbox.validations[self.item['id']]['status'], 'failed')
        self.assertFalse(any(path == '/api/chat/slots' for path, _ in self.calls))

    def test_ambiguous_send_survives_restart_without_retry(self):
        original = self.gateway.call
        def call(path, payload=None):
            if path == '/api/chat?ws=1': raise TimeoutError('ambiguous')
            return original(path, payload)
        self.gateway.call = call
        self.inbox.validate_local(self.item, 'child', 'upstream', {})
        self.assertEqual(self.inbox.validations[self.item['id']]['status'], 'uncertain')
        restarted = backend.Inbox(self.root / 'data', self.gateway)
        self.assertEqual(restarted.validations[self.item['id']]['status'], 'uncertain')
        self.assertEqual(restarted.validations[self.item['id']]['slot'], 'outrider-local-test')

    def test_real_worktree_preserves_dirty_checkout(self):
        self.verify_real_worktree(detached=False)

    def test_real_worktree_preserves_detached_checkout(self):
        self.verify_real_worktree(detached=True)

    def verify_real_worktree(self, detached):
        source = backend.ROOT.parents[1]
        checkout = self.root / 'fork'
        backend.subprocess.run(['git', 'clone', '--shared', str(source), str(checkout)], check=True, capture_output=True)
        real_git = lambda cwd, *args: backend.Inbox.git(self.inbox, cwd, *args)
        if detached:
            real_git(checkout, 'switch', '--detach')
        else:
            real_git(checkout, 'switch', '-c', 'validation-fixture')
        self.item['head'] = real_git(checkout, 'rev-parse', 'HEAD')
        real_git(checkout, 'remote', 'add', 'upstream', 'git@github.com:o/r.git')
        (checkout / 'uncommitted-validation-test.txt').write_text('keep this untouched')
        before = real_git(checkout, 'status', '--porcelain')
        # PR checkouts in CI are detached; preserve that state as well as named branches.
        branch = real_git(checkout, 'rev-parse', '--abbrev-ref', 'HEAD')
        old_root = self.root
        self.root = checkout
        def git(cwd, *args):
            if args[0] == 'fetch':
                # Exercise real fetch/worktree commands with local transport, no GitHub writes.
                return real_git(cwd, 'fetch', '--no-tags', str(source), self.item['head'] + ':refs/outrider/' + self.item['id'])
            return real_git(cwd, *args)
        self.git.side_effect = git
        try:
            self.inbox.validate_local(self.item, 'child', 'upstream', {'workflows': [{'name': 'test', 'steps': ['Inspect code']}]})
            record = self.inbox.validations[self.item['id']]
            self.assertEqual(record['status'], 'started', record)
            self.assertEqual(real_git(checkout, 'rev-parse', '--abbrev-ref', 'HEAD'), branch)
            self.assertEqual(real_git(checkout, 'rev-parse', 'HEAD'), self.item['head'])
            # Ignore only the newly created .claude directory, if not globally ignored.
            after = real_git(checkout, 'status', '--porcelain')
            self.assertEqual(after.replace('?? .claude/\n', '').replace('\n?? .claude/', ''), before)
            self.assertEqual((checkout / 'uncommitted-validation-test.txt').read_text(), 'keep this untouched')
            self.assertEqual(real_git(record['worktree'], 'rev-parse', 'HEAD'), self.item['head'])
            real_git(checkout, 'worktree', 'remove', record['worktree'])
        finally:
            self.root = old_root

    def test_session_prompt_passes_settings_and_legacy_scope(self):
        self.prompt_patch.stop()
        legacy = dict(self.item)
        del legacy['scope']
        legacy['prompt'] = 'Comment scope (null = whole PR): ["https://github.com/o/r/pull/2#issuecomment-1"]\n'
        options = {'others_prs': {'allow_push': False}, 'push': 'never'}
        result = type('Result', (), {'returncode': 0, 'stdout': '{"prompt":"shared prompt"}'})()
        with patch.object(backend.subprocess, 'run', return_value=result) as run, patch.object(backend, 'find_scanner', return_value='/outrider'):
            self.assertEqual(self.inbox.session_prompt(legacy, options, 'upstream', 'me/fork', 'review/local'), 'shared prompt')
            payload = json.loads(run.call_args.kwargs['input'])
            self.assertEqual(payload['settings'], options)
            self.assertEqual(payload['item']['scope'], ['https://github.com/o/r/pull/2#issuecomment-1'])
            self.assertEqual(payload['origin'], 'me/fork')
            legacy['prompt'] = ''
            with self.assertRaisesRegex(ValueError, 'no reliable comment scope'):
                self.inbox.session_prompt(legacy, options, 'upstream', 'me/fork', 'review/local')
