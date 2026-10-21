import { createElement as h } from "react";
import { box, button } from "../styles.mjs";
import { RepositorySessionSettings } from "../components/RepositorySessionSettings.mjs";
import { CLIImportPanel } from "../components/CLIImportPanel.mjs";
import { AISetupPanel } from "../components/AISetupPanel.mjs";
import { RepositoryEditor } from "../components/RepositoryEditor.mjs";

export function RepositoriesView({
  data,
  configure,
  save,
  busy,
  session,
  importer,
  assistant,
  editor,
}) {
  return !data.config.repos.length
    ? h(
        "section",
        { style: box },
        h("h2", null, "No repositories yet"),
        h("p", null, "Add a repository to configure its review activity."),
        h(
          "button",
          { style: button, onClick: () => configure("setup") },
          "Add repository",
        ),
      )
    : h(
        "form",
        { onSubmit: save },
        h(RepositorySessionSettings, session),
        h(CLIImportPanel, importer),
        h(AISetupPanel, assistant),
        h(RepositoryEditor, editor),
        h(
          "button",
          { style: button, type: "submit", disabled: busy },
          busy ? "Saving…" : "Save settings",
        ),
      );
}
