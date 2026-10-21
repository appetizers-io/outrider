import { createElement as h, useCallback, useEffect, useState } from 'react';
import { useAppApi } from '@kirocrew/app-sdk';

const base = '/apps/outrider/api';
const box = { border: '1px solid var(--border, #8885)', borderRadius: 12, padding: 20, marginBottom: 16 };
const button = { padding: '8px 14px', border: '1px solid var(--border, #8886)', borderRadius: 8, cursor: 'pointer', background: 'transparent', color: 'inherit' };
const selectedButton = { ...button, background: '#b4a0ff', color: '#211b32', borderColor: '#b4a0ff', fontWeight: 650 };
const reactions = [['eyes', '👀 Eyes'], ['+1', '👍 Thumbs up'], ['-1', '👎 Thumbs down'], ['laugh', '😄 Laugh'], ['confused', '😕 Confused'], ['heart', '❤️ Heart'], ['hooray', '🎉 Hooray'], ['rocket', '🚀 Rocket']];
const input = { display: 'block', width: '100%', background: 'transparent', color: 'inherit', border: '1px solid #8886', borderRadius: 8, padding: 10, boxSizing: 'border-box', margin: '8px 0 12px' };

export default function Outrider() {
  const api = useAppApi();
  const [data, setData] = useState(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [view, setView] = useState('inbox');
  const [initialized, setInitialized] = useState(false);
  const [folders, setFolders] = useState([]);
  const [mapping, setMapping] = useState({});
  const [repoSettings, setRepoSettings] = useState({});
  const [selectedRepo, setSelectedRepo] = useState('');
  const [advanced, setAdvanced] = useState('{}');
  const [editor, setEditor] = useState('form');
  const [yaml, setYaml] = useState('');
  const [setup, setSetup] = useState({});
  const [configSource, setConfigSource] = useState('');
  const [cliConfig, setCliConfig] = useState('');
  const [importProfile, setImportProfile] = useState('common');
  const [importPreview, setImportPreview] = useState(null);
  const [intent, setIntent] = useState('');
  const [newRepo, setNewRepo] = useState('');
  const [newFolder, setNewFolder] = useState('');
  const [interval, setIntervalValue] = useState(60);
  const refresh = useCallback(async () => {
    try { const next = await api.get(base + '/status'); setData(next); return next; }
    catch (e) { setError(e.message || 'Could not reach Outrider'); }
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
      setCliConfig(result.content); setConfigSource(result.path); setImportPreview(null);
      setNotice('CLI config loaded for preview. Nothing has been applied.');
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  async function previewImport() {
    setBusy(true); setError(''); setImportPreview(null);
    try { setImportPreview(await api.post(base + '/settings/import', { repo: selectedRepo, content: cliConfig, profile: importProfile })); }
    catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  function useImport() {
    setAdvanced(JSON.stringify(importPreview.settings, null, 2)); setYaml(importPreview.yaml);
    setRepoSettings({ ...repoSettings, [selectedRepo]: importPreview.settings }); setEditor('yaml'); setImportPreview(null);
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
      setMapping(data.config.folders || {}); setRepoSettings(data.config.repo_settings || {});
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
      if (await action('/config', { ...data.config, interval: Number(interval), folders: mapping, repo_settings: settings })) {
        setRepoSettings(settings); setNotice('Repository settings saved.');
      }
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  const items = data?.items || [];
  const active = items.some(x => ['requested', 'dispatching', 'running'].includes(x.run.status));
  const folderSelect = (value, onChange) => h('select', { style: input, value, onChange }, h('option', { value: '' }, 'Inbox only'), ...folders.map(f => h('option', { key: f.id, value: f.id }, f.name)));
  return h('main', { style: { padding: 28, overflowY: 'auto', maxWidth: 1100, width: '100%', boxSizing: 'border-box', margin: '0 auto', color: 'inherit' } },
    h('header', { style: { display: 'flex', gap: 20, justifyContent: 'space-between', alignItems: 'start', flexWrap: 'wrap', marginBottom: 24 } },
      h('div', null, h('img', { src: '/apps/outrider/ui/logo.svg', alt: 'Outrider', width: 44, height: 44, style: { float: 'left', marginRight: 14, padding: 4, boxSizing: 'border-box', borderRadius: 10, background: '#102027' } }), h('h1', { style: { margin: '0 0 6px', fontSize: 28 } }, view === 'setup' ? 'Add repository' : view === 'repositories' ? 'Repositories' : 'Outrider'), h('p', { style: { margin: 0, opacity: .7 } }, view === 'inbox' ? 'Your PR review inbox.' : view === 'setup' ? 'Choose a repository to watch and where its reviews belong.' : 'Configure triggers and workflows for each repository.')),
      data && h('nav', { 'aria-label': 'Outrider views', style: { display: 'flex', gap: 8, flexWrap: 'wrap' } },
        h('button', { style: view === 'inbox' ? selectedButton : button, disabled: busy, 'aria-current': view === 'inbox' ? 'page' : undefined, onClick: () => { setView('inbox'); setNotice(''); setError(''); } }, 'Inbox'),
        h('button', { style: view === 'repositories' ? selectedButton : button, disabled: busy, 'aria-current': view === 'repositories' ? 'page' : undefined, onClick: () => configure() }, 'Repositories'),
        h('button', { style: view === 'setup' ? selectedButton : button, 'aria-current': view === 'setup' ? 'page' : undefined, disabled: busy, onClick: () => configure('setup') }, 'Add repository'),
        h('button', { style: button, disabled: busy || data.scanning || !data.config.repos.length, onClick: () => action('/scan') }, data.scanning ? 'Scanning…' : 'Scan now'))),
    (error || (view === 'inbox' && data?.error)) && h('p', { role: 'alert', style: { ...box, borderColor: '#c66' } }, error || data.error),
    notice && h('p', { role: 'status' }, notice),
    !data && !error && h('p', { role: 'status' }, 'Loading…'),
    data && view === 'setup' && h('form', { style: { ...box, maxWidth: 650 }, onSubmit: addRepository },
      h('label', null, 'Repository', h('input', { style: input, required: true, autoFocus: true, value: newRepo, placeholder: 'owner/repo or https://github.com/owner/repo', onChange: e => setNewRepo(e.target.value) })),
      h('label', null, 'Review folder', folderSelect(newFolder, e => setNewFolder(e.target.value))),
      h('p', { style: { opacity: .7 } }, 'The first scan finds existing activity. You choose when to start each review. Triggers and workflows can be configured next.'),
      h('div', { style: { display: 'flex', gap: 8 } }, h('button', { style: button, disabled: busy, type: 'submit' }, 'Add repository'), h('button', { style: button, disabled: busy, type: 'button', onClick: () => setView('repositories') }, 'Cancel'))),
    data && view === 'repositories' && !data.config.repos.length && h('section', { style: box }, h('h2', null, 'No repositories yet'), h('p', null, 'Add a repository to configure its review activity.'), h('button', { style: button, onClick: () => configure('setup') }, 'Add repository')),
    data && view === 'repositories' && !!data.config.repos.length && h('form', { onSubmit: save },
      h('section', { style: box },
        h('label', null, 'Repository', h('select', { style: input, disabled: busy, value: selectedRepo, onChange: e => selectRepo(e.target.value) }, ...data.config.repos.map(repo => h('option', { key: repo, value: repo }, repo)))),
        h('label', null, 'Review folder', folderSelect(mapping[selectedRepo] || '', e => setMapping({ ...mapping, [selectedRepo]: e.target.value })))),
      h('details', { style: box }, h('summary', { style: { cursor: 'pointer' } }, 'Import CLI configuration'),
        h('p', null, 'Load your default Outrider config, choose another file, or paste YAML. The preview contains only supported Outrider settings. Agent, mode and other session settings are removed; Crew controls trust and execution.'),
        h('button', { style: button, type: 'button', disabled: busy, onClick: loadDefaultConfig }, 'Load default config'),
        h('p', { style: { opacity: .7 } }, 'Normally ~/.config/outrider/config.yaml. Respects OUTRIDER_CONFIG, the legacy config variable, and XDG_CONFIG_HOME as seen by Crew.'),
        configSource && h('p', { style: { overflowWrap: 'anywhere' } }, 'Loaded from: ' + configSource),
        h('label', null, 'CLI config file', h('input', { style: input, type: 'file', accept: '.yaml,.yml,.json', onChange: async e => { const file = e.target.files?.[0]; if (!file) return; if (file.size > 100000) { setError('Choose a config smaller than 100 KB'); return; } try { setCliConfig(await file.text()); setConfigSource(file.name); setImportPreview(null); } catch (e) { setError(e.message); } } })),
        h('label', null, 'CLI YAML', h('textarea', { style: { ...input, fontFamily: 'monospace' }, rows: 8, value: cliConfig, onChange: e => { setCliConfig(e.target.value); setImportPreview(null); } })),
        h('label', null, 'Settings to import', h('select', { style: input, value: importProfile, onChange: e => { setImportProfile(e.target.value); setImportPreview(null); } }, h('option', { value: 'common' }, 'Common to my PRs and other PRs'), h('option', { value: 'own' }, 'Use my-PR settings for this repository'), h('option', { value: 'others' }, 'Use other-PR settings for this repository'))),
        h('p', { style: { opacity: .7 } }, 'Imports apply repository-wide. Ownership-specific differences require an explicit profile choice. Crew keeps its session settings.'),
        h('button', { style: button, type: 'button', disabled: busy || !cliConfig.trim(), onClick: previewImport }, 'Preview import'),
        importPreview && h('div', null,
          h('h3', null, 'Import preview · ' + importPreview.repo),
          ...importPreview.warnings.map((warning, index) => h('p', { key: index }, warning)),
          importPreview.skipped.length > 0 && h('p', null, 'Removed from imported YAML (not applied): ' + importPreview.skipped.join(', ')),
          importPreview.applied_overrides.length > 0 && h('p', null, 'Applied CLI overrides: ' + importPreview.applied_overrides.join('; ')),
          h('pre', { style: { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' } }, importPreview.yaml),
          h('button', { style: button, type: 'button', disabled: busy || importPreview.repo !== selectedRepo, onClick: useImport }, 'Use import draft'))),
      h('details', { style: box }, h('summary', { style: { cursor: 'pointer' } }, 'Set up with AI'),
        h('p', null, 'Describe what you want to watch. Crew drafts settings for you to review before saving.'),
        h('label', null, 'What should Outrider do?', h('textarea', { style: input, rows: 3, maxLength: 6000, value: intent, placeholder: 'Review dependency updates when CI passes; ignore bot comments.', onChange: e => setIntent(e.target.value) })),
        h('button', { style: button, type: 'button', disabled: busy || !intent.trim() || ['running', 'dispatching'].includes(setup.status), onClick: generateSetup }, setup.status === 'running' ? 'Drafting…' : 'Generate draft'),
        setup.error && h('p', { role: 'alert' }, setup.error),
        setup.status === 'uncertain' && h('p', null, 'The request may still be running. Check Crew tasks before generating again.'),
        setup.status === 'completed' && h('div', null, h('p', null, 'Draft for ' + setup.repo), h('pre', { style: { whiteSpace: 'pre-wrap' } }, setup.proposal.yaml), h('button', { style: button, type: 'button', disabled: busy || setup.repo !== selectedRepo, onClick: useProposal }, 'Use draft'))),
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

        h('p', { style: { opacity: .7 } }, 'Workflows review captured snapshots. Steps requiring tools are reported as blocked. Crew owns session settings.')
      ),
      h('details', { style: box }, h('summary', { style: { cursor: 'pointer' } }, 'Polling settings · all repositories'), h('label', null, 'Poll interval in seconds', h('input', { style: { ...input, maxWidth: 180 }, type: 'number', min: 30, max: 3600, value: interval, onChange: e => setIntervalValue(e.target.value) }))),
      h('button', { style: button, type: 'submit', disabled: busy }, busy ? 'Saving…' : 'Save settings')),
    view === 'inbox' && data && h('p', { style: { opacity: .65, fontSize: 13 } }, `${items.length} snapshots · ${data.config.repos.length} repositories · ${data.last_scan ? 'Last scan ' + new Date(data.last_scan * 1000).toLocaleTimeString() : 'Waiting for first scan'}`),
    view === 'inbox' && data && !items.length && h('section', { style: { ...box, padding: 36, textAlign: 'center' } }, h('h2', null, 'Nothing queued yet'), h('p', null, data.config.repos.length ? 'Actionable PR activity will appear here after scanning.' : 'Add a repository to start watching.')),
    ...(view === 'inbox' ? items : []).map(item => h('article', { key: item.id, style: box },
      h('div', { style: { display: 'flex', gap: 12, justifyContent: 'space-between', flexWrap: 'wrap' } },
        h('div', null, h('a', { href: item.url, target: '_blank', rel: 'noopener noreferrer', style: { color: 'inherit', fontWeight: 650, fontSize: 17 } }, item.title), h('p', { style: { opacity: .7, fontSize: 13, margin: '6px 0' } }, `${item.repo} #${item.pr} · ${item.head.slice(0, 8)} · ${item.trigger}${item.workflow ? ' · ' + item.workflow : ''}`)),
        h('span', { style: { fontSize: 13, opacity: .75 } }, item.run.status)),
      h('p', { style: { fontSize: 13, opacity: .7 } }, item.truncated ? 'Partial snapshot: some context was truncated. No tests run.' : 'Static diff and PR context review. No checkout or tests.'),
      item.run.awaiting_approval && h('p', null, 'Crew is waiting for approval. Open Crew tasks to inspect it.'),
      item.run.error && h('p', { role: 'alert', style: { color: '#c66' } }, item.run.error),
      item.run.result && h('details', { open: true }, h('summary', { style: { cursor: 'pointer', marginBottom: 12 } }, 'Findings'), h('pre', { style: { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontFamily: 'inherit', lineHeight: 1.65, fontSize: 14 } }, item.run.result)),
      item.run.status === 'pending' && h('button', { style: button, disabled: busy || active, onClick: () => action('/review', { id: item.id }) }, 'Review snapshot'),
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
