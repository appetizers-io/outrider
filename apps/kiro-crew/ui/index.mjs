import { createElement as h, useCallback, useEffect, useRef, useState } from 'react';
import { useAppApi } from '@kirocrew/app-sdk';

const base = '/apps/outrider/api';
const box = { border: '1px solid var(--border, #8885)', borderRadius: 12, padding: 24, marginBottom: 24 };
const button = { padding: '10px 16px', lineHeight: 1.4, border: '1px solid var(--border, #8886)', borderRadius: 8, cursor: 'pointer', background: 'transparent', color: 'inherit' };
const selectedButton = { ...button, background: '#b4a0ff', color: '#211b32', borderColor: '#b4a0ff', fontWeight: 650 };
const reactions = [['eyes', '👀 Eyes'], ['+1', '👍 Thumbs up'], ['-1', '👎 Thumbs down'], ['laugh', '😄 Laugh'], ['confused', '😕 Confused'], ['heart', '❤️ Heart'], ['hooray', '🎉 Hooray'], ['rocket', '🚀 Rocket']];
const input = { display: 'block', width: '100%', background: 'transparent', color: 'inherit', border: '1px solid #8886', borderRadius: 8, padding: 10, boxSizing: 'border-box', margin: '10px 0 20px' };

function ImportPreviewDialog({ preview, onDismiss, onUse }) {
  const dialog = useRef(null);
  useEffect(() => {
    const previous = document.activeElement;
    const element = dialog.current;
    element.showModal();
    return () => { element.close(); if (previous?.isConnected) previous.focus(); };
  }, []);
  return h('dialog', { ref: dialog, className: 'outrider-import-dialog', 'aria-labelledby': 'outrider-import-title', onCancel: e => { e.preventDefault(); onDismiss(); }, style: { padding: 0, width: 'min(900px, calc(100vw - 32px))', maxWidth: 'calc(100vw - 32px)', maxHeight: '85vh', border: '1px solid #8886', borderRadius: 16, background: 'var(--background, #1c1921)', color: 'var(--foreground, #eee)', boxShadow: '0 24px 80px #0008' } },
    h('style', null, '.outrider-import-dialog::backdrop { background: #0009; }'),
    h('div', { style: { display: 'flex', flexDirection: 'column', maxHeight: '85vh' } },
      h('header', { style: { padding: '20px 24px', borderBottom: '1px solid #8885' } }, h('h2', { id: 'outrider-import-title', style: { margin: '0 0 8px' } }, 'Preview import'), h('div', { style: { overflowWrap: 'anywhere' } }, preview.repo), h('p', { style: { marginBottom: 0, opacity: .7 } }, 'Review the settings below. Nothing is saved until you use this draft and save settings.')),
      h('div', { style: { padding: '16px 24px', overflowY: 'auto', minHeight: 0 } },
        h('p', null, `${preview.settings.workflows?.length || 0} base workflows · ${preview.settings.overrides?.length || 0} ownership overrides`),
        h('details', null, h('summary', { style: { cursor: 'pointer' } }, 'Import notes and removed settings'), ...preview.warnings.map((warning, index) => h('p', { key: index }, warning)), preview.skipped.length > 0 && h('p', null, 'Removed: ' + preview.skipped.join(', ')), preview.applied_overrides.length > 0 && h('p', null, 'Applied CLI overrides: ' + preview.applied_overrides.join('; '))),
        h('pre', { tabIndex: 0, 'aria-label': 'Imported YAML', style: { padding: 16, border: '1px solid #8885', borderRadius: 8, whiteSpace: 'pre', overflow: 'auto', maxHeight: '45vh', fontSize: 13, lineHeight: 1.6, background: '#0002' } }, preview.yaml)),
      h('footer', { style: { padding: '16px 24px', borderTop: '1px solid #8885', display: 'flex', justifyContent: 'flex-end', gap: 12 } }, h('button', { style: button, type: 'button', autoFocus: true, onClick: onDismiss }, 'Cancel'), h('button', { style: selectedButton, type: 'button', onClick: onUse }, 'Use import draft'))));
}

export default function Outrider() {
  const api = useAppApi();
  const [data, setData] = useState(null);
  const [error, setError] = useState('');
  const [connectionError, setConnectionError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [view, setView] = useState('inbox');
  const [initialized, setInitialized] = useState(false);
  const [folders, setFolders] = useState([]);
  const [mapping, setMapping] = useState({});
  const [validationRemotes, setValidationRemotes] = useState({});
  const [automatic, setAutomatic] = useState({});
  const [maxSessions, setMaxSessions] = useState(1);
  const [showHistory, setShowHistory] = useState(false);
  const [checkoutContext, setCheckoutContext] = useState(null);
  const [repoSettings, setRepoSettings] = useState({});
  const [selectedRepo, setSelectedRepo] = useState('');
  const [advanced, setAdvanced] = useState('{}');
  const [editor, setEditor] = useState('form');
  const [yaml, setYaml] = useState('');
  const [setup, setSetup] = useState({});
  const [configSource, setConfigSource] = useState('');
  const [cliConfig, setCliConfig] = useState('');
    const [importPreview, setImportPreview] = useState(null);
  const [importError, setImportError] = useState('');
  const [intent, setIntent] = useState('');
  const [newRepo, setNewRepo] = useState('');
  const [newFolder, setNewFolder] = useState('');
  const [interval, setIntervalValue] = useState(60);
  const refresh = useCallback(async () => {
    try { const next = await api.get(base + '/status'); setData(next); setConnectionError(''); return next; }
    catch (e) { setConnectionError(e.message || 'Could not reach Outrider'); }
  }, [api]);
  useEffect(() => { refresh(); const timer = setInterval(refresh, 4000); return () => clearInterval(timer); }, [refresh]);
  useEffect(() => {
    if (setup.status !== 'running') return;
    let disposed = false;
    const poll = async () => { try { const next = await api.get(base + '/setup'); if (!disposed) setSetup(next); } catch (e) { if (!disposed) setError(e.message); } };
    poll(); const timer = setInterval(poll, 4000);
    return () => { disposed = true; clearInterval(timer); };
  }, [api, setup.status]);
  async function loadDefaultConfig() {
    setBusy(true); setError(''); setNotice('');
    try {
      const result = await api.post(base + '/settings/default', {});
      if (!result.found) { setError('No CLI config found at ' + result.path + '. Choose a file or paste YAML below.'); return; }
      setCliConfig(result.content); setConfigSource(result.path); setImportPreview(null); setImportError('');
      setNotice('CLI config loaded for preview. Nothing has been applied.');
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  async function previewImport() {
    setBusy(true); setError(''); setImportPreview(null); setImportError('');
    try { setImportPreview(await api.post(base + '/settings/import', { repo: selectedRepo, content: cliConfig, profile: 'preserve' })); }
    catch (e) { setImportError(e.message); }
    finally { setBusy(false); }
  }
  function useImport() {
    setAdvanced(JSON.stringify(importPreview.settings, null, 2)); setYaml(importPreview.yaml);
    setRepoSettings({ ...repoSettings, [selectedRepo]: importPreview.settings }); setEditor('yaml'); setImportPreview(null); setImportError('');
    setCliConfig(importPreview.yaml); setConfigSource('Imported repository settings (Crew-owned fields removed)');
    setNotice('Imported settings loaded. Agent, mode and other execution settings were removed; Crew trust and session settings are unchanged. Review and save to apply.');
  }
  async function generateSetup() {
    setBusy(true); setError('');
    try {
      const repo = view === 'setup' ? newRepo : selectedRepo;
      const current = view === 'setup' ? '{}' : JSON.stringify(await draft());
      setSetup(await api.post(base + '/setup', { repo, current, intent }));
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  function useProposal() {
    setAdvanced(JSON.stringify(setup.proposal.settings, null, 2)); setYaml(setup.proposal.yaml);
    setRepoSettings({ ...repoSettings, [selectedRepo]: setup.proposal.settings }); setEditor('yaml'); setSetup({});
    setNotice('Draft loaded. Review the YAML and save settings to apply it.');
  }
  async function action(path, payload = {}) {
    setBusy(true); setError(''); setNotice('');
    try { await api.post(base + path, payload); await refresh(); return true; }
    catch (e) { setError(e.message || 'Request failed'); return false; }
    finally { setBusy(false); }
  }
  async function draft() {
    if (!selectedRepo) return {};
    if (editor === 'yaml') return (await api.post(base + '/settings/convert', { content: yaml })).settings;
    return JSON.parse(advanced);
  }
  async function switchEditor(next) {
    if (next === editor) return;
    setBusy(true); setError('');
    try {
      const converted = await api.post(base + '/settings/convert', { content: editor === 'yaml' ? yaml : advanced });
      setAdvanced(JSON.stringify(converted.settings, null, 2));
      setRepoSettings({ ...repoSettings, [selectedRepo]: converted.settings });
      setYaml(converted.yaml); setEditor(next);
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  function editOptions(change) {
    try {
      const next = change(JSON.parse(advanced));
      setRepoSettings({ ...repoSettings, [selectedRepo]: next });
      setAdvanced(JSON.stringify(next, null, 2)); setError(''); setNotice('');
    } catch (e) { setError(e.message); }
  }
  function editWorkflow(index, patch) {
    editOptions(current => ({ ...current, workflows: (current.workflows || []).map((workflow, i) => i === index ? { ...workflow, ...patch } : workflow) }));
  }
  async function selectRepo(repo) {
    setCheckoutContext(null);
    setBusy(true); setError(''); setNotice('');
    try {
      const next = selectedRepo ? { ...repoSettings, [selectedRepo]: await draft() } : repoSettings;
      const converted = await api.post(base + '/settings/convert', { content: JSON.stringify(next[repo] || {}) });
      setRepoSettings({ ...next, [repo]: converted.settings }); setSelectedRepo(repo);
      setAdvanced(JSON.stringify(converted.settings, null, 2)); setYaml(converted.yaml);
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  async function configure(target = 'repositories') {
    if (!initialized) {
      setMapping(data.config.folders || {}); setValidationRemotes(data.config.validation_remotes || {}); setAutomatic(data.config.automatic || {}); setMaxSessions(data.config.max_sessions || 1); setRepoSettings(data.config.repo_settings || {});
      const first = data.config.repos[0] || '';
      setSelectedRepo(first); setAdvanced(JSON.stringify(data.config.repo_settings?.[first] || {}, null, 2));
      setIntervalValue(data.config.interval); setInitialized(true);
    }
    setView(target); setError(''); setNotice('');
    const results = await Promise.allSettled([api.get(base + '/folders'), api.get(base + '/setup')]);
    if (results[0].status === 'fulfilled') setFolders(results[0].value); else setError(results[0].reason.message);
    if (results[1].status === 'fulfilled') setSetup(results[1].value); else setError(results[1].reason.message);
  }
  async function addRepository(e) {
    e.preventDefault(); setError('');
    try {
      let repo = newRepo.trim();
      if (repo.startsWith('https://')) { const url = new URL(repo); if (url.hostname !== 'github.com' || url.search || url.hash || url.username || url.password) throw new Error('Enter a GitHub repository URL'); repo = url.pathname.replace(/^\/+|\/+$/g, ''); }
      repo = repo.replace(/\.git$/, '');
      if (data.config.repos.some(r => r.toLowerCase() === repo.toLowerCase())) throw new Error('This repository is already added. Select it under Repositories.');
      const saved = selectedRepo ? { ...repoSettings, [selectedRepo]: await draft() } : repoSettings;
      if (await action('/config', { ...data.config, repos: [...data.config.repos, repo], folders: { ...data.config.folders, [repo]: newFolder } })) {
        setRepoSettings(saved); setMapping({ ...mapping, [repo]: newFolder });
        setSelectedRepo(repo); setAdvanced('{}'); setYaml('{}\n'); setEditor('form');
        setNewRepo(''); setNewFolder(''); setView('repositories'); setNotice('Repository added. Configure its triggers and workflows below.');
      }
    } catch (e) { setError(e.message); }
  }
  async function save(e) {
    e.preventDefault(); setBusy(true); setError('');
    try {
      const settings = { ...repoSettings, [selectedRepo]: await draft() };
      if (await action('/config', { ...data.config, interval: Number(interval), folders: mapping, repo_settings: settings, validation_remotes: validationRemotes, automatic, max_sessions: Number(maxSessions) })) {
        setRepoSettings(settings); setNotice('Repository settings saved.');
      }
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  const allItems = data?.items || [];
  const newest = new Map();
  for (const item of allItems) if (!newest.has(item.repo + '#' + item.pr)) newest.set(item.repo + '#' + item.pr, item);
  const queueState = item => item.dismissed ? 'dismissed' : item.validation?.status || (item.run.status === 'pending' ? 'queued' : item.run.status);
  const items = showHistory ? allItems : allItems.filter(item => !item.dismissed && (['preparing', 'dispatching', 'started', 'running', 'requested', 'uncertain'].includes(queueState(item)) || (newest.get(item.repo + '#' + item.pr) === item && !['completed', 'superseded'].includes(queueState(item)))));
  const pendingCount = [...newest.values()].filter(item => !item.dismissed && queueState(item) === 'queued').length;
  const elapsed = data?.scan_started ? Math.max(0, Math.floor(Date.now() / 1000 - data.scan_started)) : 0;
  const scanLabel = connectionError ? '○ Disconnected' : data?.scanning ? `● ${data.config.paused ? 'Finishing scan' : 'Scanning'} · ${elapsed}s` : data?.scan_requested ? '◷ Scan queued' : data?.config.paused ? 'Ⅱ Polling paused' : data?.error ? '✕ Scan failed' : data?.scan_partial ? '◷ Progress saved · more to scan' : !data?.config.repos.length ? '○ No repositories' : data?.last_scan ? '✓ Watching' : '◷ Waiting for first scan';
  const scanColor = connectionError || data?.error ? '#e59a9a' : data?.scanning || data?.scan_requested || data?.scan_partial ? '#c5b4ff' : data?.config.paused || !data?.last_scan ? 'inherit' : '#75d6a4';
  const active = (data?.active_sessions || 0) >= (data?.config.max_sessions || 1);
  const folderSelect = (value, onChange) => h('select', { style: input, value, onChange }, h('option', { value: '' }, 'Inbox only'), ...folders.map(f => h('option', { key: f.id, value: f.id }, f.name)));
  return h('main', { className: 'outrider-app', style: { padding: 28, overflowY: 'auto', maxWidth: 1100, width: '100%', boxSizing: 'border-box', margin: '0 auto', color: 'inherit' } },
    h('style', null, `.outrider-app { line-height: 1.6; } .outrider-app p { margin: 12px 0 20px; } .outrider-app h2, .outrider-app h3 { line-height: 1.3; margin: 24px 0 16px; } .outrider-app label { display: block; margin-bottom: 16px; } .outrider-app details[open] > summary { margin-bottom: 20px; } .outrider-app button + p { margin-top: 18px; } .outrider-app section > :last-child { margin-bottom: 0; } .outrider-app legend { padding: 0 8px; } .outrider-app button + button { margin-left: 8px; }`),
    h('header', { style: { display: 'flex', gap: 20, justifyContent: 'space-between', alignItems: 'start', flexWrap: 'wrap', marginBottom: 24 } },
      h('div', null, h('img', { src: '/apps/outrider/ui/logo.svg', alt: 'Outrider', width: 44, height: 44, style: { float: 'left', marginRight: 14, padding: 4, boxSizing: 'border-box', borderRadius: 10, background: '#102027' } }), h('h1', { style: { margin: '0 0 6px', fontSize: 28 } }, view === 'setup' ? 'Add repository' : view === 'repositories' ? 'Repositories' : 'Outrider'), h('p', { style: { margin: 0, opacity: .7 } }, view === 'inbox' ? 'Your PR work queue. Automatic sessions run under Crew’s settings.' : view === 'setup' ? 'Choose a repository to watch and where its reviews belong.' : 'Configure triggers and workflows for each repository.')),
      data && h('nav', { 'aria-label': 'Outrider views', style: { display: 'flex', gap: 8, flexWrap: 'wrap' } },
        h('button', { style: view === 'inbox' ? selectedButton : button, disabled: busy, 'aria-current': view === 'inbox' ? 'page' : undefined, onClick: () => { setView('inbox'); setNotice(''); setError(''); } }, 'Queue', pendingCount > 0 && h('span', { style: { marginLeft: 8, borderRadius: 12, padding: '2px 7px', background: '#0003' } }, pendingCount)),
        h('button', { style: view === 'repositories' ? selectedButton : button, disabled: busy, 'aria-current': view === 'repositories' ? 'page' : undefined, onClick: () => configure() }, 'Repositories'),
        h('button', { style: view === 'setup' ? selectedButton : button, 'aria-current': view === 'setup' ? 'page' : undefined, disabled: busy, onClick: () => configure('setup') }, 'Add repository'),
        h('button', { style: { ...button, borderColor: data.config.paused ? '#8886' : '#59b98a', color: data.config.paused ? 'inherit' : '#75d6a4' }, type: 'button', 'aria-pressed': !data.config.paused, disabled: busy || !!connectionError, onClick: async () => { const paused = !data.config.paused; if (await action('/polling', { paused })) setNotice(paused ? 'Polling and new automatic sessions are inactive. Running work will finish.' : 'Polling is active and resumes automatically when Crew starts.'); } }, data.config.paused ? '○ Inactive' : '● Active'))),
    data && h('p', { style: { opacity: .7 } }, `${data.config.paused ? 'Polling inactive' : 'Always-on polling · every ' + data.config.interval + 's'} · ${data.active_sessions || 0}/${data.config.max_sessions || 1} sessions active. Status is restored when Crew starts.`),
    (error || (view === 'inbox' && data?.error)) && h('p', { role: 'alert', style: { ...box, borderColor: '#c66' } }, error || data.error),
    notice && h('aside', { role: 'status', 'aria-live': 'polite', style: { position: 'fixed', bottom: 24, right: 24, zIndex: 1000, maxWidth: 'min(480px, calc(100vw - 48px))', display: 'flex', gap: 16, alignItems: 'center', padding: '16px 20px', borderRadius: 12, border: '1px solid #59b98a', background: '#173c2c', color: '#e8fff1', boxShadow: '0 8px 32px #0006' } }, h('span', null, '✓ ' + notice), h('button', { style: button, type: 'button', 'aria-label': 'Dismiss notification', onClick: () => setNotice('') }, '×')),
    importPreview && h(ImportPreviewDialog, { preview: importPreview, onDismiss: () => setImportPreview(null), onUse: useImport }),
    connectionError && h('p', { role: 'status', style: box }, 'Connection to Outrider interrupted. Retrying automatically… ', h('button', { style: button, onClick: refresh }, 'Retry now'), h('details', null, h('summary', null, 'Details'), connectionError)),
    data && h('details', { style: box }, h('summary', { style: { cursor: 'pointer' } }, 'Activity log'),
      h('p', null, scanLabel),
      h('p', { style: { opacity: .7 } }, data.scan_partial ? 'Progress saved; pending work continues on the next pass.' : data.next_scan && !data.config.paused ? 'Next pass in ' + Math.max(0, Math.ceil(data.next_scan - Date.now() / 1000)) + ' seconds.' : ''),
      h('div', { style: { maxHeight: 300, overflowY: 'auto', fontSize: 13 } }, ...(data.events || []).map((event, index) => h('p', { key: index, style: { margin: '8px 0', color: event.level === 'error' ? '#e59a9a' : 'inherit' } }, new Date(event.time * 1000).toLocaleTimeString() + ' · ' + event.message)), !(data.events || []).length && h('p', null, 'No activity recorded yet.'))),
    !data && !error && !connectionError && h('p', { role: 'status' }, 'Loading…'),
    data && view === 'setup' && h('form', { style: { ...box, maxWidth: 650 }, onSubmit: addRepository },
      h('label', null, 'Repository', h('input', { style: input, required: true, autoFocus: true, value: newRepo, placeholder: 'owner/repo or https://github.com/owner/repo', onChange: e => setNewRepo(e.target.value) })),
      h('label', null, 'Review folder', folderSelect(newFolder, e => setNewFolder(e.target.value))),
      h('p', { style: { opacity: .7 } }, 'The first scan finds existing activity. You choose when to start each review. Triggers and workflows can be configured next.'),
      h('div', { style: { display: 'flex', gap: 8 } }, h('button', { style: button, disabled: busy, type: 'submit' }, 'Add repository'), h('button', { style: button, disabled: busy, type: 'button', onClick: () => setView('repositories') }, 'Cancel'))),
    data && view === 'repositories' && !data.config.repos.length && h('section', { style: box }, h('h2', null, 'No repositories yet'), h('p', null, 'Add a repository to configure its review activity.'), h('button', { style: button, onClick: () => configure('setup') }, 'Add repository')),
    data && view === 'repositories' && !!data.config.repos.length && h('form', { onSubmit: save },
      h('section', { style: box },
        h('label', null, 'Repository', h('select', { style: input, disabled: busy, value: selectedRepo, onChange: e => selectRepo(e.target.value) }, ...data.config.repos.map(repo => h('option', { key: repo, value: repo }, repo)))),
        h('label', null, 'Crew folder', folderSelect(mapping[selectedRepo] || '', e => { setMapping({ ...mapping, [selectedRepo]: e.target.value }); setCheckoutContext(null); const next = { ...validationRemotes }; delete next[selectedRepo]; setValidationRemotes(next); })),
        h('h3', null, 'Automatic sessions'),
        h('label', null, h('input', { type: 'checkbox', checked: !!automatic[selectedRepo], onChange: e => setAutomatic({ ...automatic, [selectedRepo]: e.target.checked }) }), ' Automatically open a Crew session for new matching PR activity'),
        h('p', null, 'Enabled: new queued work starts automatically, up to the session limit. Disabled: keep work in the queue for manual review. Requires a Crew folder and matching remote below. Existing backlog is not launched when enabling.'),
        h('h3', null, 'Local validation'),
        h('p', null, 'Watch this GitHub repository and validate PRs in an isolated worktree of the Crew folder’s checkout. Crew controls the agent, model and permissions.'),
        h('button', { style: button, type: 'button', disabled: busy || !mapping[selectedRepo], onClick: async () => { setBusy(true); setError(''); try { setCheckoutContext(await api.post(base + '/checkout', { folder_id: mapping[selectedRepo] })); } catch (e) { setError(e.message); } finally { setBusy(false); } } }, 'Detect checkout and remotes'),
        checkoutContext && h('p', null, 'Local checkout: ' + checkoutContext.checkout),
        checkoutContext && h('label', null, 'Remote to watch', h('select', { style: input, value: validationRemotes[selectedRepo] || '', onChange: e => { const next = { ...validationRemotes }; if (e.target.value) next[selectedRepo] = e.target.value; else delete next[selectedRepo]; setValidationRemotes(next); } }, h('option', { value: '' }, 'Snapshot reviews only'), ...checkoutContext.remotes.filter(r => r.repo.toLowerCase() === selectedRepo.toLowerCase()).map(r => h('option', { key: r.name, value: r.name }, r.name + ' · ' + r.repo)))),
        checkoutContext && !checkoutContext.remotes.some(r => r.repo.toLowerCase() === selectedRepo.toLowerCase()) && h('p', { role: 'alert' }, 'No GitHub remote matches this watched repository. Add its upstream remote to the checkout, or select the correct repository.'),
        validationRemotes[selectedRepo] && h('p', null, 'Local validation remote: ' + validationRemotes[selectedRepo] + '. Save settings to apply. Automatic sessions use this checkout; you can also start queued work manually.')),
      h('details', { style: box }, h('summary', { style: { cursor: 'pointer' } }, 'Import CLI configuration'),
        h('p', null, 'Load your default Outrider config, choose another file, or paste YAML. The preview contains only supported Outrider settings. Agent, mode and other session settings are removed; Crew controls trust and execution.'),
        h('button', { style: button, type: 'button', disabled: busy, onClick: loadDefaultConfig }, 'Load default config'),
        h('p', { style: { opacity: .7 } }, 'Normally ~/.config/outrider/config.yaml. Respects OUTRIDER_CONFIG, the legacy config variable, and XDG_CONFIG_HOME as seen by Crew.'),
        configSource && h('p', { style: { overflowWrap: 'anywhere' } }, 'Loaded from: ' + configSource),
        h('label', null, 'CLI config file', h('input', { style: input, type: 'file', accept: '.yaml,.yml,.json', onChange: async e => { const file = e.target.files?.[0]; if (!file) return; if (file.size > 100000) { setError('Choose a config smaller than 100 KB'); return; } try { setCliConfig(await file.text()); setConfigSource(file.name); setImportPreview(null); setImportError(''); } catch (e) { setError(e.message); } } })),
        h('label', null, 'CLI YAML', h('textarea', { style: { ...input, fontFamily: 'monospace' }, rows: 8, value: cliConfig, onChange: e => { setCliConfig(e.target.value); setImportPreview(null); setImportError(''); } })),
        h('p', { style: { opacity: .7 } }, 'Own-PR and other-PR behavior is preserved automatically, including triggers and workflows. Crew keeps its session settings.'),
        h('button', { style: button, type: 'button', disabled: busy || !cliConfig.trim(), onClick: previewImport }, busy ? 'Preparing preview…' : 'Preview import'),
        importError && h('p', { role: 'alert', style: { ...box, borderColor: '#c66' } }, importError),
      ),
      h('details', { style: box }, h('summary', { style: { cursor: 'pointer' } }, 'Set up with AI'),
        h('p', null, 'Describe what you want to watch. Crew drafts settings for you to review before saving.'),
        h('label', null, 'What should Outrider do?', h('textarea', { style: input, rows: 3, maxLength: 6000, value: intent, placeholder: 'Review dependency updates when CI passes; ignore bot comments.', onChange: e => setIntent(e.target.value) })),
        h('button', { style: button, type: 'button', disabled: busy || !intent.trim() || ['running', 'dispatching'].includes(setup.status), onClick: generateSetup }, setup.status === 'running' ? 'Drafting…' : 'Generate draft'),
        setup.error && h('p', { role: 'alert' }, setup.error),
        setup.status === 'uncertain' && h('p', null, 'The request may still be running. Check Crew tasks before generating again.'),
        setup.status === 'completed' && h('div', null, h('p', null, 'Draft for ' + setup.repo), h('pre', { style: { whiteSpace: 'pre-wrap' } }, setup.proposal.yaml), h('button', { style: button, type: 'button', disabled: busy || setup.repo !== selectedRepo, onClick: useProposal }, 'Use draft'))),
      JSON.parse(advanced).overrides?.length > 0 && h('p', null, 'Ownership overrides are preserved. The form edits base settings; use YAML to edit own-PR and other-PR overrides.'),
      h('div', { role: 'group', 'aria-label': 'Configuration editor', style: { display: 'flex', gap: 8, marginBottom: 16 } }, ...[['form', 'Form'], ['yaml', 'YAML']].map(([mode, label]) => h('button', { key: mode, style: editor === mode ? selectedButton : button, type: 'button', disabled: busy, 'aria-pressed': editor === mode, onClick: () => switchEditor(mode) }, label))),
      editor === 'yaml' ? h('section', { style: box },
        h('label', null, 'Repository YAML', h('textarea', { style: { ...input, fontFamily: 'monospace' }, rows: 18, value: yaml, spellCheck: false, onChange: e => { setYaml(e.target.value); setNotice(''); } })),
        h('p', { style: { opacity: .7 } }, 'Uses the standalone configuration fields for this repository. Switching to Form validates and reformats YAML; comments are not preserved.')) : h('section', { style: box },
        h('h2', { style: { marginTop: 0 } }, 'Triggers'),
        ...[['own_prs', 'My PR activity'], ['opt_in', 'Reaction opt-in'], ['review_replies', 'Replies to my reviews'], ['mentions', 'Mentions']].map(([key, label]) => h('div', { key, style: { marginBottom: 12 } },
          h('label', { style: { display: 'block' } }, h('input', { type: 'checkbox', checked: repoSettings[selectedRepo]?.triggers?.[key]?.enabled !== false, onChange: e => {
            editOptions(current => ({ ...current, triggers: { ...current.triggers, [key]: { ...current.triggers?.[key], enabled: e.target.checked } } }));
          } }), ' ' + label),
          key === 'opt_in' && h('div', { style: { margin: '10px 0 18px 24px' } },
            h('label', null, 'Opt-in reaction', h('select', { style: { ...input, maxWidth: 260 }, disabled: repoSettings[selectedRepo]?.triggers?.opt_in?.enabled === false, value: repoSettings[selectedRepo]?.triggers?.opt_in?.reaction || 'eyes', onChange: e => editOptions(current => ({ ...current, triggers: { ...current.triggers, opt_in: { ...current.triggers?.opt_in, reaction: e.target.value } } })) }, ...reactions.map(([value, name]) => h('option', { key: value, value }, name)))),
            h('p', { style: { opacity: .7, margin: 0 } }, 'Your selected reaction opts someone else’s PR into watching. Removing it stops the watch.')))),
        h('h2', { style: { marginTop: 28 } }, 'Workflows'),
        ...(Array.isArray(repoSettings[selectedRepo]?.workflows) ? repoSettings[selectedRepo].workflows : []).map((workflow, index) => h('fieldset', { style: box, key: index },
          h('legend', null, 'Workflow ' + (index + 1)),
          h('label', null, 'Name', h('input', { style: input, value: workflow?.name || '', onChange: e => editWorkflow(index, { name: e.target.value }) })),
          h('label', null, 'Trigger condition', h('select', { style: input, value: workflow?.on || 'activity', onChange: e => editWorkflow(index, { on: e.target.value }) }, ...[['activity', 'PR activity'], ['ci_passed', 'CI becomes green'], ['discussions_resolved', 'Discussions become resolved']].map(([value, label]) => h('option', { value, key: value }, label)))),
          h('label', null, 'Steps, one per line', h('textarea', { style: input, rows: 4, value: Array.isArray(workflow?.steps) ? workflow.steps.join('\n') : '', onChange: e => editWorkflow(index, { steps: e.target.value.split('\n') }) })),
          h('button', { style: button, type: 'button', onClick: () => editOptions(current => ({ ...current, workflows: current.workflows.filter((_, i) => i !== index) })) }, 'Remove workflow')
        )),
        h('button', { style: button, type: 'button', onClick: () => editOptions(current => ({ ...current, workflows: [...(current.workflows || []), { name: 'review-' + ((current.workflows || []).length + 1), match: [{}], on: 'activity', steps: ['Inspect the changed code for correctness.', 'Summarize findings and remaining uncertainties.'] }] })) }, 'Add workflow'),

        h('p', { style: { opacity: .7 } }, 'Snapshot reviews use captured context. Local validation can run workflow steps with Crew’s tools and permissions.')
      ),
      h('details', { style: box }, h('summary', { style: { cursor: 'pointer' } }, 'Polling and session limits · all repositories'), h('label', null, 'Maximum active sessions', h('input', { style: { ...input, maxWidth: 180 }, type: 'number', min: 1, max: 20, value: maxSessions, onChange: e => setMaxSessions(e.target.value) })), h('label', null, 'Poll interval in seconds', h('input', { style: { ...input, maxWidth: 180 }, type: 'number', min: 30, max: 3600, value: interval, onChange: e => setIntervalValue(e.target.value) }))),
      h('button', { style: button, type: 'submit', disabled: busy }, busy ? 'Saving…' : 'Save settings')),
    view === 'inbox' && data && h('p', { style: { opacity: .65, fontSize: 13 } }, `${pendingCount} queued PRs · ${data.config.repos.length} repositories`),
    view === 'inbox' && data && h('label', null, h('input', { type: 'checkbox', checked: showHistory, onChange: e => setShowHistory(e.target.checked) }), ' Show completed, dismissed and older activity'),
    view === 'inbox' && data && !items.length && h('section', { style: { ...box, padding: 36, textAlign: 'center' } }, h('h2', null, 'Nothing queued yet'), h('p', null, data.config.repos.length ? 'Actionable PR activity will appear here after scanning.' : 'Add a repository to start watching.')),
    ...(view === 'inbox' ? items : []).map(item => h('article', { key: item.id, style: box },
      h('div', { style: { display: 'flex', gap: 12, justifyContent: 'space-between', flexWrap: 'wrap' } },
        h('div', null, h('a', { href: item.url, target: '_blank', rel: 'noopener noreferrer', style: { color: 'inherit', fontWeight: 650, fontSize: 17 } }, item.title), h('p', { style: { opacity: .7, fontSize: 13, margin: '6px 0' } }, `${item.repo} #${item.pr} · ${item.head.slice(0, 8)} · ${item.trigger}${item.workflow ? ' · ' + item.workflow : ''}`)),
        h('span', { style: { fontSize: 13, opacity: .75 } }, queueState(item))),
      h('p', { style: { fontSize: 13, opacity: .7 } }, item.truncated ? 'Captured context is partial. Local validation can inspect the full checkout.' : data.config.automatic?.[item.repo] ? 'Automatic processing enabled for this repository.' : 'Manual processing: start a Crew session or review the captured context.'),
      !item.dismissed && !['preparing', 'dispatching', 'started', 'running', 'requested', 'uncertain'].includes(queueState(item)) && h('button', { style: button, disabled: busy, onClick: async () => { if (await action('/dismiss', { id: item.id })) setNotice('Item dismissed. Future PR activity can still create new work; remove the reaction or disable its trigger to stop watching.'); } }, 'Dismiss'),
      item.run.awaiting_approval && h('p', null, 'Crew is waiting for approval. Open Crew tasks to inspect it.'),
      item.run.error && h('p', { role: 'alert', style: { color: '#c66' } }, item.run.error),
      item.run.result && h('details', { open: true }, h('summary', { style: { cursor: 'pointer', marginBottom: 12 } }, 'Findings'), h('pre', { style: { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontFamily: 'inherit', lineHeight: 1.65, fontSize: 14 } }, item.run.result)),
      !item.dismissed && data.config.validation_remotes?.[item.repo] && (!item.validation || (item.validation.status === 'failed' && !item.validation.worktree)) && h('button', { style: button, disabled: busy, onClick: () => action('/validate', { id: item.id }) }, 'Validate locally'),
      item.validation && h('div', { style: box }, h('p', null, 'Local validation: ' + item.validation.status + (item.validation.status === 'started' ? ' — follow progress and approvals in Crew; completion is reported in the chat.' : '')), item.validation.slot && h('p', null, 'Crew chat: ' + item.validation.slot), item.validation.worktree && h('p', { style: { overflowWrap: 'anywhere' } }, 'Worktree: ' + item.validation.worktree), item.validation.tracking_error && h('p', { role: 'status' }, item.validation.tracking_error), item.validation.error && h('p', { role: 'alert' }, item.validation.error)),
      !item.dismissed && item.run.status === 'pending' && h('button', { style: button, disabled: busy || active, onClick: () => action('/review', { id: item.id }) }, 'Review without tools'),
      ['failed', 'completed', 'uncertain'].includes(item.run.status) && h('button', { style: button, disabled: busy || active, onClick: () => { if (item.run.status !== 'uncertain' || window.confirm('Crew may already be running this review. Check Crew tasks first. Start another review?')) action('/review', { id: item.id, retry: true }); } }, 'Retry review'),
      item.run.result && h('button', { style: { ...button, marginLeft: 8 }, onClick: async () => {
        try { await navigator.clipboard.writeText(`Outrider review: ${item.repo} #${item.pr}\nSnapshot: ${item.head}\n${item.url}\n\n${item.run.result}`); setNotice('Findings copied. Paste them into your existing Crew chat.'); }
        catch { setError('Clipboard unavailable. Select and copy the findings above.'); }
      } }, 'Copy findings to existing chat'),
      item.run.status === 'completed' && data.config.folders?.[item.repo] && !item.run.delivery && h('button', { style: { ...button, marginLeft: 8 }, disabled: busy, onClick: () => action('/deliver', { id: item.id }) }, 'Save to new folder chat'),
      item.run.delivery && h('p', { role: 'status' }, item.run.delivery.status === 'saved' ? 'Findings saved in your Crew folder.' : 'Delivery may be incomplete. Check your Crew folder before creating another chat.'),
      item.run.task_id && h('p', { style: { fontSize: 12, opacity: .55 } }, 'Crew task: ' + item.run.task_id)
    ))
  );
}
