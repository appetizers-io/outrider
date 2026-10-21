import { createElement as h } from "react";
import { box, input, button } from "../styles.mjs";

export function AISetupPanel({
  intent,
  setIntent,
  busy,
  setup,
  generateSetup,
  selectedRepo,
  useProposal,
}) {
  return h(
    "details",
    { style: box },
    h("summary", { style: { cursor: "pointer" } }, "Set up with AI"),
    h(
      "p",
      null,
      "Describe what you want to watch. Crew drafts settings for you to review before saving.",
    ),
    h(
      "label",
      null,
      "What should Outrider do?",
      h("textarea", {
        style: input,
        rows: 3,
        maxLength: 6000,
        value: intent,
        placeholder:
          "Review dependency updates when CI passes; ignore bot comments.",
        onChange: (e) => setIntent(e.target.value),
      }),
    ),
    h(
      "button",
      {
        style: button,
        type: "button",
        disabled:
          busy ||
          !intent.trim() ||
          ["running", "dispatching"].includes(setup.status),
        onClick: generateSetup,
      },
      setup.status === "running" ? "Drafting…" : "Generate draft",
    ),
    setup.error && h("p", { role: "alert" }, setup.error),
    setup.status === "uncertain" &&
      h(
        "p",
        null,
        "The request may still be running. Check Crew tasks before generating again.",
      ),
    setup.status === "completed" &&
      h(
        "div",
        null,
        h("p", null, "Draft for " + setup.repo),
        h("pre", { style: { whiteSpace: "pre-wrap" } }, setup.proposal.yaml),
        h(
          "button",
          {
            style: button,
            type: "button",
            disabled: busy || setup.repo !== selectedRepo,
            onClick: useProposal,
          },
          "Use draft",
        ),
      ),
  );
}
