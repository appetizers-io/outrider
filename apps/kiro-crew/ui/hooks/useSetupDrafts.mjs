import { useState, useEffect } from "react";
import { base } from "../api.mjs";
export function useSetupDrafts({
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
  setImportedPolling,
}) {
  const [setup, setSetup] = useState({});
  const [configSource, setConfigSource] = useState("");
  const [cliConfig, setCliConfig] = useState("");
  const [importPreview, setImportPreview] = useState(null);
  const [importError, setImportError] = useState("");
  const [intent, setIntent] = useState("");
  useEffect(() => {
    if (setup.status !== "running") return;
    let disposed = false;
    const poll = async () => {
      try {
        const next = await api.get(base + "/setup");
        if (!disposed) setSetup(next);
      } catch (e) {
        if (!disposed) setError(e.message);
      }
    };
    poll();
    const timer = setInterval(poll, 4000);
    return () => {
      disposed = true;
      clearInterval(timer);
    };
  }, [api, setup.status]);
  async function loadDefaultConfig() {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const result = await api.post(base + "/settings/default", {});
      if (!result.found) {
        setError(
          "No CLI config found at " +
            result.path +
            ". Choose a file or paste YAML below.",
        );
        return;
      }
      setCliConfig(result.content);
      setConfigSource(result.path);
      setImportPreview(null);
      setImportError("");
      setNotice("CLI config loaded for preview. Nothing has been applied.");
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  async function previewImport() {
    setBusy(true);
    setError("");
    setImportPreview(null);
    setImportError("");
    try {
      setImportPreview(
        await api.post(base + "/settings/import", {
          repo: selectedRepo,
          content: cliConfig,
          profile: "preserve",
        }),
      );
    } catch (e) {
      setImportError(e.message);
    } finally {
      setBusy(false);
    }
  }
  function useImport() {
    setImportedPolling((previous) => ({
      ...previous,
      [selectedRepo]: importPreview.polling || {},
    }));
    setAdvanced(JSON.stringify(importPreview.settings, null, 2));
    setYaml(importPreview.yaml);
    setRepoSettings({
      ...repoSettings,
      [selectedRepo]: importPreview.settings,
    });
    setEditor("yaml");
    setImportPreview(null);
    setImportError("");
    setCliConfig(importPreview.yaml);
    setConfigSource("Imported repository settings (Crew-owned fields removed)");
    setNotice(
      "Imported settings loaded. Agent, mode and other execution settings were removed; Crew trust and session settings are unchanged. Review and save to apply.",
    );
  }
  async function generateSetup() {
    setBusy(true);
    setError("");
    try {
      const repo = view === "setup" ? newRepo : selectedRepo;
      const current = view === "setup" ? "{}" : JSON.stringify(await draft());
      setSetup(await api.post(base + "/setup", { repo, current, intent }));
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  function useProposal() {
    setAdvanced(JSON.stringify(setup.proposal.settings, null, 2));
    setYaml(setup.proposal.yaml);
    setRepoSettings({
      ...repoSettings,
      [selectedRepo]: setup.proposal.settings,
    });
    setEditor("yaml");
    setSetup({});
    setNotice("Draft loaded. Review the YAML and save settings to apply it.");
  }
  return {
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
  };
}
