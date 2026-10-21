import { createElement as h } from "react";
import { selectedButton, button, box, input, reactions } from "../styles.mjs";

export function RepositoryEditor({
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
}) {
  return h(
    "div",
    null,
    JSON.parse(advanced).overrides?.length > 0 &&
      h(
        "p",
        null,
        "Ownership overrides are preserved. The form edits base settings; use YAML to edit own-PR and other-PR overrides.",
      ),
    h(
      "div",
      {
        role: "group",
        "aria-label": "Configuration editor",
        style: { display: "flex", gap: 8, marginBottom: 16 },
      },
      ...[
        ["form", "Form"],
        ["yaml", "YAML"],
      ].map(([mode, label]) =>
        h(
          "button",
          {
            key: mode,
            style: editor === mode ? selectedButton : button,
            type: "button",
            disabled: busy,
            "aria-pressed": editor === mode,
            onClick: () => switchEditor(mode),
          },
          label,
        ),
      ),
    ),
    editor === "yaml"
      ? h(
          "section",
          { style: box },
          h(
            "label",
            null,
            "Repository YAML",
            h("textarea", {
              style: { ...input, fontFamily: "monospace" },
              rows: 18,
              value: yaml,
              spellCheck: false,
              onChange: (e) => {
                setYaml(e.target.value);
                setNotice("");
              },
            }),
          ),
          h(
            "p",
            { style: { opacity: 0.7 } },
            "Uses the standalone configuration fields for this repository. Switching to Form validates and reformats YAML; comments are not preserved.",
          ),
        )
      : h(
          "section",
          { style: box },
          h("h2", { style: { marginTop: 0 } }, "Triggers"),
          ...[
            ["own_prs", "My PR activity"],
            ["opt_in", "Reaction opt-in"],
            ["review_replies", "Replies to my reviews"],
            ["mentions", "Mentions"],
          ].map(([key, label]) =>
            h(
              "div",
              { key, style: { marginBottom: 12 } },
              h(
                "label",
                { style: { display: "block" } },
                h("input", {
                  type: "checkbox",
                  checked:
                    repoSettings[selectedRepo]?.triggers?.[key]?.enabled !==
                    false,
                  onChange: (e) => {
                    editOptions((current) => ({
                      ...current,
                      triggers: {
                        ...current.triggers,
                        [key]: {
                          ...current.triggers?.[key],
                          enabled: e.target.checked,
                        },
                      },
                    }));
                  },
                }),
                " " + label,
              ),
              key === "opt_in" &&
                h(
                  "div",
                  { style: { margin: "10px 0 18px 24px" } },
                  h(
                    "label",
                    null,
                    "Opt-in reaction",
                    h(
                      "select",
                      {
                        style: { ...input, maxWidth: 260 },
                        disabled:
                          repoSettings[selectedRepo]?.triggers?.opt_in
                            ?.enabled === false,
                        value:
                          repoSettings[selectedRepo]?.triggers?.opt_in
                            ?.reaction || "eyes",
                        onChange: (e) =>
                          editOptions((current) => ({
                            ...current,
                            triggers: {
                              ...current.triggers,
                              opt_in: {
                                ...current.triggers?.opt_in,
                                reaction: e.target.value,
                              },
                            },
                          })),
                      },
                      ...reactions.map(([value, name]) =>
                        h("option", { key: value, value }, name),
                      ),
                    ),
                  ),
                  h(
                    "p",
                    { style: { opacity: 0.7, margin: 0 } },
                    "Your selected reaction opts someone else’s PR into watching. Removing it stops the watch.",
                  ),
                ),
            ),
          ),
          h("h2", { style: { marginTop: 28 } }, "Workflows"),
          ...(Array.isArray(repoSettings[selectedRepo]?.workflows)
            ? repoSettings[selectedRepo].workflows
            : []
          ).map((workflow, index) =>
            h(
              "fieldset",
              { style: box, key: index },
              h("legend", null, "Workflow " + (index + 1)),
              h(
                "label",
                null,
                "Name",
                h("input", {
                  style: input,
                  value: workflow?.name || "",
                  onChange: (e) =>
                    editWorkflow(index, { name: e.target.value }),
                }),
              ),
              h(
                "label",
                null,
                "Trigger condition",
                h(
                  "select",
                  {
                    style: input,
                    value: workflow?.on || "activity",
                    onChange: (e) =>
                      editWorkflow(index, { on: e.target.value }),
                  },
                  ...[
                    ["activity", "PR activity"],
                    ["ci_passed", "CI becomes green"],
                    ["discussions_resolved", "Discussions become resolved"],
                  ].map(([value, label]) =>
                    h("option", { value, key: value }, label),
                  ),
                ),
              ),
              h(
                "label",
                null,
                "Steps, one per line",
                h("textarea", {
                  style: input,
                  rows: 4,
                  value: Array.isArray(workflow?.steps)
                    ? workflow.steps.join("\n")
                    : "",
                  onChange: (e) =>
                    editWorkflow(index, { steps: e.target.value.split("\n") }),
                }),
              ),
              h(
                "button",
                {
                  style: button,
                  type: "button",
                  onClick: () =>
                    editOptions((current) => ({
                      ...current,
                      workflows: current.workflows.filter(
                        (_, i) => i !== index,
                      ),
                    })),
                },
                "Remove workflow",
              ),
            ),
          ),
          h(
            "button",
            {
              style: button,
              type: "button",
              onClick: () =>
                editOptions((current) => ({
                  ...current,
                  workflows: [
                    ...(current.workflows || []),
                    {
                      name: "review-" + ((current.workflows || []).length + 1),
                      match: [{}],
                      on: "activity",
                      steps: [
                        "Inspect the changed code for correctness.",
                        "Summarize findings and remaining uncertainties.",
                      ],
                    },
                  ],
                })),
            },
            "Add workflow",
          ),

          h(
            "p",
            { style: { opacity: 0.7 } },
            "Snapshot reviews use captured context. Local validation can run workflow steps with Crew’s tools and permissions.",
          ),
        ),
  );
}
