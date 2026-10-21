import { useState } from "react";
import { base } from "../api.mjs";
export function useRepositoryDraft({
  api,
  setBusy,
  setError,
  setNotice,
  setCheckoutContext,
}) {
  const [repoSettings, setRepoSettings] = useState({});
  const [selectedRepo, setSelectedRepo] = useState("");
  const [advanced, setAdvanced] = useState("{}");
  const [editor, setEditor] = useState("form");
  const [yaml, setYaml] = useState("");
  async function draft() {
    if (!selectedRepo) return {};
    if (editor === "yaml")
      return (await api.post(base + "/settings/convert", { content: yaml }))
        .settings;
    return JSON.parse(advanced);
  }
  async function switchEditor(next) {
    if (next === editor) return;
    setBusy(true);
    setError("");
    try {
      const converted = await api.post(base + "/settings/convert", {
        content: editor === "yaml" ? yaml : advanced,
      });
      setAdvanced(JSON.stringify(converted.settings, null, 2));
      setRepoSettings({ ...repoSettings, [selectedRepo]: converted.settings });
      setYaml(converted.yaml);
      setEditor(next);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  function editOptions(change) {
    try {
      const next = change(JSON.parse(advanced));
      setRepoSettings({ ...repoSettings, [selectedRepo]: next });
      setAdvanced(JSON.stringify(next, null, 2));
      setError("");
      setNotice("");
    } catch (e) {
      setError(e.message);
    }
  }
  function editWorkflow(index, patch) {
    editOptions((current) => ({
      ...current,
      workflows: (current.workflows || []).map((workflow, i) =>
        i === index ? { ...workflow, ...patch } : workflow,
      ),
    }));
  }
  async function selectRepo(repo) {
    setCheckoutContext(null);
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const next = selectedRepo
        ? { ...repoSettings, [selectedRepo]: await draft() }
        : repoSettings;
      const converted = await api.post(base + "/settings/convert", {
        content: JSON.stringify(next[repo] || {}),
      });
      setRepoSettings({ ...next, [repo]: converted.settings });
      setSelectedRepo(repo);
      setAdvanced(JSON.stringify(converted.settings, null, 2));
      setYaml(converted.yaml);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  return {
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
  };
}
