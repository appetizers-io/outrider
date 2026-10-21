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
  newRemote,
  setNewRemote,
  newContext,
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
        disabled: busy || !!newRemote,
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
        disabled: busy,
        onChange: (e) => setNewFolder(e.target.value),
      }),
    ),
    newContext &&
      h(
        "label",
        null,
        "Watch remote",
        h(
          "select",
          {
            style: input,
            value: newRemote,
            disabled: busy,
            onChange: (e) => {
              const remote = newContext.remotes.find(
                (r) => r.name === e.target.value,
              );
              if (remote) {
                setNewRemote(remote.name);
                setNewRepo(remote.repo);
              }
            },
          },
          h("option", { value: "" }, "Select remote"),
          ...newContext.remotes.map((r) =>
            h(
              "option",
              { key: r.name, value: r.name },
              r.name + " · " + r.repo,
            ),
          ),
        ),
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
