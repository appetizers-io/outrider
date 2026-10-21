import { createElement as h } from "react";
import { box, button, input } from "../styles.mjs";

export function CLIImportPanel({
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
}) {
  return h(
    "details",
    { style: box },
    h("summary", { style: { cursor: "pointer" } }, "Import CLI configuration"),
    h(
      "p",
      null,
      "Load your default Outrider config, choose another file, or paste YAML. The preview contains only supported Outrider settings. Agent, mode and other session settings are removed; Crew controls trust and execution.",
    ),
    h(
      "button",
      {
        style: button,
        type: "button",
        disabled: busy,
        onClick: loadDefaultConfig,
      },
      "Load default config",
    ),
    h(
      "p",
      { style: { opacity: 0.7 } },
      "Normally ~/.config/outrider/config.yaml. Respects OUTRIDER_CONFIG, the legacy config variable, and XDG_CONFIG_HOME as seen by Crew.",
    ),
    configSource &&
      h(
        "p",
        { style: { overflowWrap: "anywhere" } },
        "Loaded from: " + configSource,
      ),
    h(
      "label",
      null,
      "CLI config file",
      h("input", {
        style: input,
        type: "file",
        accept: ".yaml,.yml,.json",
        onChange: async (e) => {
          const file = e.target.files?.[0];
          if (!file) return;
          if (file.size > 100000) {
            setError("Choose a config smaller than 100 KB");
            return;
          }
          try {
            setCliConfig(await file.text());
            setConfigSource(file.name);
            setImportPreview(null);
            setImportError("");
          } catch (e) {
            setError(e.message);
          }
        },
      }),
    ),
    h(
      "label",
      null,
      "CLI YAML",
      h("textarea", {
        style: { ...input, fontFamily: "monospace" },
        rows: 8,
        value: cliConfig,
        onChange: (e) => {
          setCliConfig(e.target.value);
          setImportPreview(null);
          setImportError("");
        },
      }),
    ),
    h(
      "p",
      { style: { opacity: 0.7 } },
      "Own-PR and other-PR behavior is preserved automatically, including triggers and workflows. Crew keeps its session settings.",
    ),
    h(
      "button",
      {
        style: button,
        type: "button",
        disabled: busy || !cliConfig.trim(),
        onClick: previewImport,
      },
      busy ? "Preparing preview…" : "Preview import",
    ),
    importError &&
      h(
        "p",
        { role: "alert", style: { ...box, borderColor: "#c66" } },
        importError,
      ),
  );
}
