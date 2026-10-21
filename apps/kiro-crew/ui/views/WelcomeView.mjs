import { createElement as h, useEffect, useState } from "react";
import { base } from "../api.mjs";
import { box, button, input } from "../styles.mjs";
import { DoctorView } from "./DoctorView.mjs";
import {
  FolderSelect,
  MemberOptions,
  TrustOptions,
} from "../components/SessionControls.mjs";
import { ImportPreviewDialog } from "../components/ImportPreviewDialog.mjs";

export function WelcomeView({ api, data, onDone, previewOnly = false }) {
  const [step, setStep] = useState(0),
    [draft, setDraft] = useState({
      repo: "",
      folder: "",
      remote: "",
      automatic: false,
      trust: "inherit",
      reviewer: "",
      maximum: 1,
    }),
    [loaded, setLoaded] = useState(false);
  const [folders, setFolders] = useState([]),
    [members, setMembers] = useState([]),
    [context, setContext] = useState(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [preview, setPreview] = useState(null);
  const edit = (patch) => setDraft((d) => ({ ...d, ...patch }));
  useEffect(() => {
    let live = true;
    Promise.all([
      previewOnly ? Promise.resolve({}) : api.get(base + "/onboarding"),
      api.get(base + "/folders"),
      api.get(base + "/members"),
    ])
      .then(([saved, f, m]) => {
        if (!live) return;
        setDraft((d) => ({ ...d, ...saved.draft }));
        setStep(saved.step || 0);
        setFolders(f);
        setMembers(Array.isArray(m) ? m : m.members || m.agents || []);
        setLoaded(true);
      })
      .catch((e) => setError(e.message));
    return () => {
      live = false;
    };
  }, [api, previewOnly]);
  async function move(next) {
    setBusy(true);
    setError("");
    try {
      if (!previewOnly)
        await api.post(base + "/onboarding", { draft, step: next });
      setStep(next);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  async function folderChanged(id) {
    edit({ folder: id, remote: "", repo: "" });
    setContext(null);
    if (!id) return;
    setBusy(true);
    setError("");
    try {
      const c = await api.post(base + "/checkout", { folder_id: id });
      setContext(c);
      const r =
        c.remotes.find((r) => r.name === "upstream") ||
        c.remotes.find((r) => r.name === "origin") ||
        c.remotes[0];
      if (r) edit({ repo: r.repo, remote: r.name });
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  async function importConfig() {
    setBusy(true);
    setError("");
    try {
      const config = await api.post(base + "/settings/default", {});
      if (!config.found)
        throw Error(
          "No CLI configuration found at " +
            config.path +
            ". You can import a file later under Repositories.",
        );
      setPreview(
        await api.post(base + "/settings/import", {
          repo: draft.repo,
          content: config.content,
          profile: "preserve",
        }),
      );
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  async function finish() {
    setBusy(true);
    setError("");
    try {
      if (!previewOnly) await api.post(base + "/onboarding/finish", { draft });
      await onDone();
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  const steps = ["Readiness", "Repository", "Sessions", "Finish"];
  return h(
    "section",
    null,
    h("h2", null, previewOnly ? "Preview setup wizard" : "Set up Outrider"),
    previewOnly &&
      h(
        "p",
        { role: "status" },
        "Preview only. Your settings stay unchanged; this wizard will not install a runtime, start polling, or launch sessions.",
      ),
    h(
      "p",
      null,
      steps.map((s, i) => `${i === step ? "●" : "○"} ${s}`).join("  →  "),
    ),
    error && h("p", { role: "alert" }, error),
    !loaded && h("p", null, "Loading setup…"),
    loaded &&
      step === 0 &&
      h(DoctorView, {
        api,
        readOnly: previewOnly,
        autoProvision: !previewOnly,
        onReady: () => move(1),
      }),
    loaded &&
      step === 1 &&
      h(
        "section",
        { style: box },
        h("h3", null, "Watch a repository from a Crew folder"),
        h(
          "label",
          null,
          "Crew folder",
          h(FolderSelect, {
            folders,
            disabled: busy,
            value: draft.folder,
            onChange: (e) => folderChanged(e.target.value),
          }),
        ),
        h(
          "p",
          null,
          "For forks, upstream is suggested as the watched repository. Reviews use the folder’s local checkout.",
        ),
        context &&
          h(
            "label",
            null,
            "Watch remote",
            h(
              "select",
              {
                style: input,
                value: draft.remote,
                onChange: (e) => {
                  const r = context.remotes.find(
                    (r) => r.name === e.target.value,
                  );
                  if (r) edit({ remote: r.name, repo: r.repo });
                },
              },
              ...context.remotes.map((r) =>
                h(
                  "option",
                  { key: r.name, value: r.name },
                  `${r.name} · ${r.repo}`,
                ),
              ),
            ),
          ),
        h(
          "label",
          null,
          "GitHub repository",
          h("input", {
            style: input,
            value: draft.repo,
            disabled: !!draft.remote,
            onChange: (e) => edit({ repo: e.target.value }),
            placeholder: "owner/repo",
          }),
        ),
        draft.remote && h("p", null, `Watch ${draft.remote} · ${draft.repo}`),
        h(
          "button",
          {
            style: button,
            disabled: busy || !draft.repo,
            onClick: importConfig,
          },
          "Preview existing CLI configuration",
        ),
        h(
          "p",
          null,
          draft.settings
            ? "Imported task settings are staged. Crew owns session settings."
            : "Defaults work without a CLI config. Triggers and workflows can be edited later.",
        ),
      ),
    loaded &&
      step === 2 &&
      h(
        "section",
        { style: box },
        h("h3", null, "Crew sessions"),
        h(
          "label",
          null,
          h("input", {
            type: "checkbox",
            checked: draft.automatic,
            disabled: !draft.folder || !draft.remote,
            onChange: (e) => edit({ automatic: e.target.checked }),
          }),
          " Automatically start sessions",
        ),
        h(
          "p",
          null,
          "Automatic sessions need a Crew folder and matching remote. These become global defaults; repositories can override them.",
        ),
        h(
          "label",
          null,
          "Reviewer",
          h(
            "select",
            {
              style: input,
              value: draft.reviewer,
              onChange: (e) => edit({ reviewer: e.target.value }),
            },
            h(MemberOptions, { members }),
          ),
        ),
        h(
          "label",
          null,
          "Trust",
          h(
            "select",
            {
              style: input,
              value: draft.trust,
              onChange: (e) => edit({ trust: e.target.value }),
            },
            h(TrustOptions),
          ),
        ),
        h(
          "label",
          null,
          "Maximum concurrent sessions",
          h("input", {
            style: input,
            type: "number",
            min: 1,
            max: 20,
            value: draft.maximum,
            onChange: (e) => edit({ maximum: Number(e.target.value) }),
          }),
        ),
      ),
    loaded &&
      step === 3 &&
      h(
        "section",
        { style: box },
        h("h3", null, "Ready to start"),
        h("p", null, `Watch ${draft.repo} · ${draft.remote || "queue only"}`),
        h(
          "p",
          null,
          `Automatic sessions ${draft.automatic ? "on" : "off"} · maximum ${draft.maximum} · trust ${draft.trust} · reviewer ${draft.reviewer || "Crew default"}`,
        ),
        h(
          "p",
          null,
          previewOnly
            ? "Close the preview to return to your queue. Nothing will be saved."
            : "Finishing saves your configuration and starts polling.",
        ),
      ),
    loaded &&
      step > 0 &&
      h(
        "div",
        { style: { display: "flex", gap: 12 } },
        h(
          "button",
          { style: button, disabled: busy, onClick: () => move(step - 1) },
          "Back",
        ),
        h(
          "button",
          {
            style: button,
            disabled: busy || !draft.repo,
            onClick: () => (step === 3 ? finish() : move(step + 1)),
          },
          step === 3
            ? previewOnly
              ? "Close preview"
              : "Save and start polling"
            : "Continue",
        ),
      ),
    preview &&
      h(ImportPreviewDialog, {
        preview,
        onDismiss: () => setPreview(null),
        onUse: () => {
          edit({ settings: preview.settings, polling: preview.polling });
          setPreview(null);
        },
      }),
  );
}
