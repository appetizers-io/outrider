import { createElement as h, useCallback, useEffect, useState } from 'react';
import { useAppApi } from '@kirocrew/app-sdk';

const base = '/apps/outrider/api';
const box = { border: '1px solid var(--border, #8885)', borderRadius: 12, padding: 20, marginBottom: 16 };
const button = { padding: '8px 14px', border: '1px solid var(--border, #8886)', borderRadius: 8, cursor: 'pointer', background: 'transparent', color: 'inherit' };
const input = { display: 'block', width: '100%', background: 'transparent', color: 'inherit', border: '1px solid #8886', borderRadius: 8, padding: 10, boxSizing: 'border-box', margin: '8px 0 12px' };

export default function Outrider() {
  const api = useAppApi();
  const [data, setData] = useState(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);
  const [repos, setRepos] = useState('');
  const [interval, setIntervalValue] = useState(60);
  const refresh = useCallback(async () => {
    try { setData(await api.get(base + '/status')); setError(''); }
    catch (e) { setError(e.message || 'Could not reach Outrider'); }
  }, [api]);
  useEffect(() => { refresh(); const timer = setInterval(refresh, 4000); return () => clearInterval(timer); }, [refresh]);
  async function action(path, payload = {}) {
    setBusy(true);
    try { await api.post(base + path, payload); await refresh(); return true; }
    catch (e) { setError(e.message || 'Request failed'); return false; }
    finally { setBusy(false); }
  }
  function configure() {
    setRepos(data.config.repos.join('\n'));
    setIntervalValue(data.config.interval);
    setEditing(true);
  }
  const items = data?.items || [];
  const active = items.some(x => ['requested', 'dispatching', 'running'].includes(x.run.status));
  return h('main', { style: { padding: 28, overflowY: 'auto', maxWidth: 1100, width: '100%', boxSizing: 'border-box', margin: '0 auto', color: 'inherit' } },
    h('header', { style: { display: 'flex', gap: 20, justifyContent: 'space-between', alignItems: 'start', flexWrap: 'wrap', marginBottom: 24 } },
      h('div', null, h('h1', { style: { margin: '0 0 6px', fontSize: 28 } }, 'Outrider'), h('p', { style: { margin: 0, opacity: .7 } }, 'Your PR review inbox. Findings stay here.')),
      data && h('div', { style: { display: 'flex', gap: 8 } },
        h('button', { style: button, onClick: configure }, 'Repositories'),
        h('button', { style: button, disabled: busy || data.scanning || !data.config.repos.length, onClick: () => action('/scan') }, data.scanning ? 'Scanning…' : 'Scan now'))),
    (error || data?.error) && h('p', { role: 'alert', style: { ...box, borderColor: '#c66' } }, error || data.error),
    !data && !error && h('p', { role: 'status' }, 'Loading review inbox…'),
    data && (editing || !data.config.repos.length) && h('form', { style: box, onSubmit: async e => { e.preventDefault(); if (await action('/config', { repos: repos.split(/\s+/).filter(Boolean), interval: Number(interval) })) setEditing(false); } },
      h('h2', { style: { marginTop: 0 } }, 'Choose repositories'),
      h('p', null, 'Enter repositories you want to watch. Existing actionable activity will be added on the first scan. Reviews start when you click Review snapshot.'),
      h('label', null, 'Repositories, one owner/repo per line', h('textarea', { style: input, rows: 3, value: repos, placeholder: 'your-org/your-repo', onChange: e => setRepos(e.target.value) })),
      h('label', null, 'Poll interval in seconds', h('input', { style: { ...input, maxWidth: 180 }, type: 'number', min: 30, max: 3600, value: interval, onChange: e => setIntervalValue(e.target.value) })),
      h('button', { style: button, type: 'submit', disabled: busy }, 'Save repositories'),
      editing && h('button', { style: { ...button, marginLeft: 8 }, type: 'button', onClick: () => setEditing(false) }, 'Cancel')),
    data && h('p', { style: { opacity: .65, fontSize: 13 } }, `${items.length} snapshots · ${data.config.repos.length} repositories · ${data.last_scan ? 'Last scan ' + new Date(data.last_scan * 1000).toLocaleTimeString() : 'Waiting for first scan'}`),
    data && !items.length && h('section', { style: { ...box, padding: 36, textAlign: 'center' } }, h('h2', null, 'Nothing queued yet'), h('p', null, data.config.repos.length ? 'Actionable PR activity will appear here after scanning.' : 'Add a repository to start watching.')),
    ...items.map(item => h('article', { key: item.id, style: box },
      h('div', { style: { display: 'flex', gap: 12, justifyContent: 'space-between', flexWrap: 'wrap' } },
        h('div', null, h('a', { href: item.url, target: '_blank', rel: 'noopener noreferrer', style: { color: 'inherit', fontWeight: 650, fontSize: 17 } }, item.title), h('p', { style: { opacity: .7, fontSize: 13, margin: '6px 0' } }, `${item.repo} #${item.pr} · ${item.head.slice(0, 8)} · ${item.trigger}`)),
        h('span', { style: { fontSize: 13, opacity: .75 } }, item.run.status)),
      h('p', { style: { fontSize: 13, opacity: .7 } }, item.truncated ? 'Partial snapshot: some context was truncated. No tests run.' : 'Static diff and PR context review. No checkout or tests.'),
      item.run.awaiting_approval && h('p', null, 'Crew is waiting for approval. Open Crew tasks to inspect it.'),
      item.run.error && h('p', { role: 'alert', style: { color: '#c66' } }, item.run.error),
      item.run.result && h('details', { open: true }, h('summary', { style: { cursor: 'pointer', marginBottom: 12 } }, 'Findings'), h('pre', { style: { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontFamily: 'inherit', lineHeight: 1.65, fontSize: 14 } }, item.run.result)),
      item.run.status === 'pending' && h('button', { style: button, disabled: busy || active, onClick: () => action('/review', { id: item.id }) }, 'Review snapshot'),
      ['failed', 'completed', 'uncertain'].includes(item.run.status) && h('button', { style: button, disabled: busy || active, onClick: () => { if (item.run.status !== 'uncertain' || window.confirm('Crew may already be running this review. Check Crew tasks first. Start another review?')) action('/review', { id: item.id, retry: true }); } }, 'Retry review'),
      item.run.task_id && h('p', { style: { fontSize: 12, opacity: .55 } }, 'Crew task: ' + item.run.task_id)
    ))
  );
}
