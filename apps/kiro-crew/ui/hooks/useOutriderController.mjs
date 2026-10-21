import { base } from "../api.mjs";
import { useSetupDrafts } from "./useSetupDrafts.mjs";
import { useRepositoryDraft } from "./useRepositoryDraft.mjs";
import { selectQueue } from "../queue.mjs";
import { useState, useEffect } from "react";
import { useOutriderApi } from "./useOutriderApi.mjs";
export function useOutriderController() {
  const {
    api,
    data,
    error,
    setError,
    connectionError,
    notice,
    setNotice,
    busy,
    setBusy,
    refresh,
    action,
  } = useOutriderApi();
  const [view, setView] = useState("inbox");
  const [checkedStartup, setCheckedStartup] = useState(false);
  useEffect(() => {
    if (!data || checkedStartup) return;
    setCheckedStartup(true);
    api
      .get(base + "/doctor")
      .then((report) => {
        if (!report.complete && !data.config.repos.length) setView("welcome");
        else if (!report.ready) setView("doctor");
      })
      .catch((e) => setError(e.message));
  }, [data, checkedStartup, api]);
  const [initialized, setInitialized] = useState(false);
  const [folders, setFolders] = useState([]);
  const [mapping, setMapping] = useState({});
  const [validationRemotes, setValidationRemotes] = useState({});
  const [queueRepo, setQueueRepo] = useState("");
  const [importedPolling, setImportedPolling] = useState({});
  const [candidateLimit, setCandidateLimit] = useState(50);
  const [lookback, setLookback] = useState(168);
  const [automatic, setAutomatic] = useState({});
  const [automaticDefault, setAutomaticDefault] = useState(false);
  const [trustDefault, setTrustDefault] = useState("inherit");
  const [trustOverrides, setTrustOverrides] = useState({});
  const [reviewerDefault, setReviewerDefault] = useState("");
  const [reviewers, setReviewers] = useState({});
  const [members, setMembers] = useState([]);
  const [maxSessions, setMaxSessions] = useState(1);
  const [showHistory, setShowHistory] = useState(false);
  const [checkoutContext, setCheckoutContext] = useState(null);

  const [newRepo, setNewRepo] = useState("");
  const [newFolder, setNewFolder] = useState("");
  const [newRemote, setNewRemote] = useState("");
  const [newContext, setNewContext] = useState(null);
  async function chooseNewFolder(id) {
    setNewFolder(id);
    setNewRemote("");
    setNewContext(null);
    if (!id) return;
    setBusy(true);
    setError("");
    try {
      const context = await api.post(base + "/checkout", { folder_id: id });
      setNewContext(context);
      const remote =
        context.remotes.find((r) => r.name === "upstream") ||
        context.remotes.find((r) => r.name === "origin") ||
        context.remotes[0];
      if (remote) {
        setNewRepo(remote.repo);
        setNewRemote(remote.name);
      }
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  const [interval, setIntervalValue] = useState(60);

  const {
    repoSettings,
    setRepoSettings,
    selectedRepo,
    setSelectedRepo,
    advanced,
    setAdvanced,
    editor,
    setEditor,
    yaml,
    setYaml,
    draft,
    switchEditor,
    editOptions,
    editWorkflow,
    selectRepo,
  } = useRepositoryDraft({
    api,
    setBusy,
    setError,
    setNotice,
    setCheckoutContext,
  });
  const {
    setup,
    setSetup,
    configSource,
    setConfigSource,
    cliConfig,
    setCliConfig,
    importPreview,
    setImportPreview,
    importError,
    setImportError,
    intent,
    setIntent,
    loadDefaultConfig,
    previewImport,
    useImport,
    generateSetup,
    useProposal,
  } = useSetupDrafts({
    setImportedPolling,
    api,
    setBusy,
    setError,
    setNotice,
    selectedRepo,
    view,
    newRepo,
    draft,
    setAdvanced,
    setYaml,
    setRepoSettings,
    repoSettings,
    setEditor,
  });
  async function configure(target = "repositories") {
    if (!initialized) {
      setMapping(data.config.folders || {});
      setValidationRemotes(data.config.validation_remotes || {});
      setAutomatic(data.config.automatic || {});
      setAutomaticDefault(data.config.automatic_default || false);
      setTrustDefault(data.config.session_trust_default || "inherit");
      setTrustOverrides(data.config.session_trust || {});
      setReviewerDefault(data.config.reviewer_default || "");
      setReviewers(data.config.reviewers || {});
      setMaxSessions(data.config.max_sessions || 1);
      setRepoSettings(data.config.repo_settings || {});
      const first = data.config.repos[0] || "";
      setSelectedRepo(first);
      setAdvanced(
        JSON.stringify(data.config.repo_settings?.[first] || {}, null, 2),
      );
      setIntervalValue(data.config.interval);
      setLookback(data.config.lookback_hours || 168);
      setCandidateLimit(data.config.candidate_limit || 50);
      setInitialized(true);
    }
    setView(target);
    setError("");
    setNotice("");
    const results = await Promise.allSettled([
      api.get(base + "/folders"),
      api.get(base + "/setup"),
      api.get(base + "/members"),
    ]);
    if (results[0].status === "fulfilled") setFolders(results[0].value);
    else setError(results[0].reason.message);
    if (results[2].status === "fulfilled")
      setMembers(results[2].value.agents || []);
    else setError("Could not load Crew members: " + results[2].reason.message);
    if (results[1].status === "fulfilled") setSetup(results[1].value);
    else setError(results[1].reason.message);
  }
  async function addRepository(e) {
    e.preventDefault();
    setError("");
    try {
      let repo = newRepo.trim();
      if (repo.startsWith("https://")) {
        const url = new URL(repo);
        if (
          url.hostname !== "github.com" ||
          url.search ||
          url.hash ||
          url.username ||
          url.password
        )
          throw new Error("Enter a GitHub repository URL");
        repo = url.pathname.replace(/^\/+|\/+$/g, "");
      }
      repo = repo.replace(/\.git$/, "");
      if (data.config.repos.some((r) => r.toLowerCase() === repo.toLowerCase()))
        throw new Error(
          "This repository is already added. Select it under Repositories.",
        );
      const saved = selectedRepo
        ? { ...repoSettings, [selectedRepo]: await draft() }
        : repoSettings;
      if (
        await action("/config", {
          ...data.config,
          repos: [...data.config.repos, repo],
          folders: { ...data.config.folders, [repo]: newFolder },
          automatic: {
            ...data.config.automatic,
            ...(!newFolder ? { [repo]: false } : {}),
          },
          validation_remotes: {
            ...data.config.validation_remotes,
            ...(newRemote ? { [repo]: newRemote } : {}),
          },
        })
      ) {
        setRepoSettings(saved);
        setMapping({ ...mapping, [repo]: newFolder });
        if (newRemote)
          setValidationRemotes({ ...validationRemotes, [repo]: newRemote });
        setSelectedRepo(repo);
        setAdvanced("{}");
        setYaml("{}\n");
        setEditor("form");
        setNewRepo("");
        setNewFolder("");
        setNewRemote("");
        setNewContext(null);
        setView("repositories");
        setNotice(
          "Repository added. Configure its triggers and workflows below.",
        );
      }
    } catch (e) {
      setError(e.message);
    }
  }
  async function saveGlobal(e) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (
        await action("/config", {
          ...data.config,
          interval: Number(interval),
          lookback_hours: Number(lookback),
          candidate_limit: Number(candidateLimit),
          automatic_default: automaticDefault,
          session_trust_default: trustDefault,
          reviewer_default: reviewerDefault,
          max_sessions: Number(maxSessions),
        })
      )
        setNotice("Global settings saved.");
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  async function save(e) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const settings = { ...repoSettings, [selectedRepo]: await draft() };
      const polling = importedPolling[selectedRepo] || {};
      if (
        await action("/config", {
          ...data.config,
          folders: mapping,
          repo_settings: settings,
          ...(polling.interval_seconds !== undefined
            ? { interval: polling.interval_seconds }
            : {}),
          ...(polling.lookback_hours !== undefined
            ? { lookback_hours: polling.lookback_hours }
            : {}),
          ...(polling.candidate_limit !== undefined
            ? { candidate_limit: polling.candidate_limit }
            : {}),
          validation_remotes: validationRemotes,
          automatic,
          session_trust: trustOverrides,
          reviewers,
        })
      ) {
        setRepoSettings(settings);
        if (polling.interval_seconds !== undefined)
          setIntervalValue(polling.interval_seconds);
        if (polling.lookback_hours !== undefined)
          setLookback(polling.lookback_hours);
        if (polling.candidate_limit !== undefined)
          setCandidateLimit(polling.candidate_limit);
        setImportedPolling((previous) => {
          const next = { ...previous };
          delete next[selectedRepo];
          return next;
        });
        setNotice("Repository settings saved.");
      }
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  const override = (values, setter, value) => {
    const next = { ...values };
    if (value === "__global") delete next[selectedRepo];
    else next[selectedRepo] = value;
    setter(next);
  };
  const { items, pendingCount, queueState } = selectQueue(
    data,
    queueRepo,
    showHistory,
  );
  const elapsed = data?.scan_started
    ? Math.max(0, Math.floor(Date.now() / 1000 - data.scan_started))
    : 0;
  const scanLabel = connectionError
    ? "○ Disconnected"
    : data?.scanning
      ? `● ${data.config.paused ? "Finishing scan" : "Scanning"} · ${elapsed}s`
      : data?.scan_requested
        ? "◷ Scan queued"
        : data?.config.paused
          ? "Ⅱ Polling paused"
          : data?.error
            ? "✕ Scan failed"
            : data?.scan_partial
              ? "◷ Progress saved · more to scan"
              : !data?.config.repos.length
                ? "○ No repositories"
                : data?.last_scan
                  ? "✓ Watching"
                  : "◷ Waiting for first scan";
  const scanColor =
    connectionError || data?.error
      ? "#e59a9a"
      : data?.scanning || data?.scan_requested || data?.scan_partial
        ? "#c5b4ff"
        : data?.config.paused || !data?.last_scan
          ? "inherit"
          : "#75d6a4";
  const active =
    (data?.active_sessions || 0) >= (data?.config.max_sessions || 1);

  return {
    manual: { data, action, busy, setView },
    doctor: {
      api,
      onSetup: () => setView("welcome"),
      setupLabel: data?.config.repos.length
        ? "Preview setup wizard"
        : "Open setup wizard",
    },
    welcome: {
      previewOnly: !!data?.config.repos.length,
      api,
      data,
      onDone: async () => {
        await refresh();
        setInitialized(false);
        setView("inbox");
        setNotice(
          data?.config.repos.length
            ? "Setup preview closed. Settings unchanged."
            : "Setup saved. Polling is active.",
        );
      },
    },
    header: {
      view,
      data,
      busy,
      setView,
      setNotice,
      setError,
      pendingCount,
      configure,
      connectionError,
      action,
    },
    feedback: {
      error,
      view,
      data,
      notice,
      setNotice,
      connectionError,
      refresh,
    },
    activity: { scanColor, scanLabel, data },
    add: {
      addRepository,
      newRepo,
      setNewRepo,
      folders,
      newFolder,
      setNewFolder: chooseNewFolder,
      newRemote,
      setNewRemote,
      newContext,
      busy,
      setView,
    },
    settings: {
      saveGlobal,
      automaticDefault,
      setAutomaticDefault,
      reviewerDefault,
      setReviewerDefault,
      members,
      trustDefault,
      setTrustDefault,
      candidateLimit,
      setCandidateLimit,
      lookback,
      setLookback,
      maxSessions,
      setMaxSessions,
      interval,
      setIntervalValue,
      busy,
    },
    queue: {
      queueRepo,
      setQueueRepo,
      data,
      busy,
      action,
      setNotice,
      pendingCount,
      showHistory,
      setShowHistory,
      items,
      queueState,
      active,
      setError,
    },
    repository: {
      data,
      configure,
      save,
      busy,
      session: {
        busy,
        selectedRepo,
        selectRepo,
        data,
        folders,
        mapping,
        setMapping,
        setCheckoutContext,
        validationRemotes,
        setValidationRemotes,
        automatic,
        setAutomatic,
        automaticDefault,
        reviewers,
        override,
        setReviewers,
        members,
        trustOverrides,
        setTrustOverrides,
        setBusy,
        setError,
        api,
        checkoutContext,
      },
      importer: {
        busy,
        loadDefaultConfig,
        configSource,
        setError,
        setCliConfig,
        setConfigSource,
        setImportPreview,
        setImportError,
        cliConfig,
        previewImport,
        importError,
      },
      assistant: {
        intent,
        setIntent,
        busy,
        setup,
        generateSetup,
        selectedRepo,
        useProposal,
      },
      editor: {
        advanced,
        editor,
        busy,
        switchEditor,
        yaml,
        setYaml,
        setNotice,
        repoSettings,
        selectedRepo,
        editOptions,
        editWorkflow,
      },
    },
    view,
    data,
    importPreview,
    setImportPreview,
    useImport,
  };
}
