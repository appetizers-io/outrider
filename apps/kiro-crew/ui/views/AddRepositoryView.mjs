import { FolderSelect } from "../components/SessionControls.mjs";
import { createElement as h } from "react";
import { box, input, button } from "../styles.mjs";

export function AddRepositoryView({
  addRepository,
  newRepo,
  setNewRepo,
  folders,
  newFolder,
  setNewFolder,
  busy,
  setView,
}) {
  return h(
    "form",
    { style: { ...box, maxWidth: 650 }, onSubmit: addRepository },
    h(
      "label",
      null,
      "Repository",
      h("input", {
        style: input,
        required: true,
        autoFocus: true,
        value: newRepo,
        placeholder: "owner/repo or https://github.com/owner/repo",
        onChange: (e) => setNewRepo(e.target.value),
      }),
    ),
    h(
      "label",
      null,
      "Review folder",
      h(FolderSelect, {
        folders,
        value: newFolder,
        onChange: (e) => setNewFolder(e.target.value),
      }),
    ),
    h(
      "p",
      { style: { opacity: 0.7 } },
      "The first scan finds existing activity. You choose when to start each review. Triggers and workflows can be configured next.",
    ),
    h(
      "div",
      { style: { display: "flex", gap: 8 } },
      h(
        "button",
        { style: button, disabled: busy, type: "submit" },
        "Add repository",
      ),
      h(
        "button",
        {
          style: button,
          disabled: busy,
          type: "button",
          onClick: () => setView("repositories"),
        },
        "Cancel",
      ),
    ),
  );
}
