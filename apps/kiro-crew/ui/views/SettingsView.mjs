import { MemberOptions, TrustOptions } from "../components/SessionControls.mjs";
import { createElement as h } from "react";
import { box, input, button } from "../styles.mjs";

export function SettingsView({
  saveGlobal,
  automaticDefault,
  setAutomaticDefault,
  reviewerDefault,
  setReviewerDefault,
  members,
  trustDefault,
  setTrustDefault,
  lookback,
  setLookback,
  maxSessions,
  setMaxSessions,
  interval,
  setIntervalValue,
  busy,
}) {
  return h(
    "form",
    { onSubmit: saveGlobal },
    h(
      "section",
      { style: box },
      h("h2", null, "Global session defaults"),
      h(
        "label",
        null,
        h("input", {
          type: "checkbox",
          checked: automaticDefault,
          onChange: (e) => setAutomaticDefault(e.target.checked),
        }),
        " Automatically start sessions",
      ),
      h(
        "label",
        null,
        "Reviewer Crew member",
        h(
          "select",
          {
            style: input,
            value: reviewerDefault,
            onChange: (e) => setReviewerDefault(e.target.value),
          },
          h(MemberOptions, { members }),
        ),
      ),
      h(
        "label",
        null,
        "Session trust",
        h(
          "select",
          {
            style: input,
            value: trustDefault,
            onChange: (e) => setTrustDefault(e.target.value),
          },
          h(TrustOptions),
        ),
      ),
      h(
        "p",
        null,
        "Repositories inherit these defaults unless overridden in the Repositories view. Trust changes apply to new Outrider sessions and require Crew’s session approval grant. Existing sessions keep their settings.",
      ),
    ),

    h(
      "details",
      { style: box, open: true },
      h(
        "summary",
        { style: { cursor: "pointer" } },
        "Polling and session limits · all repositories",
      ),
      h(
        "label",
        null,
        "Lookback hours",
        h("input", {
          style: { ...input, maxWidth: 180 },
          type: "number",
          min: 1,
          value: lookback,
          onChange: (e) => setLookback(e.target.value),
        }),
      ),
      h(
        "p",
        null,
        "GitHub notification window and candidate retention. Changing it does not clear existing queued work.",
      ),
      h(
        "label",
        null,
        "Maximum active sessions",
        h("input", {
          style: { ...input, maxWidth: 180 },
          type: "number",
          min: 1,
          max: 20,
          value: maxSessions,
          onChange: (e) => setMaxSessions(e.target.value),
        }),
      ),
      h(
        "label",
        null,
        "Poll interval in seconds",
        h("input", {
          style: { ...input, maxWidth: 180 },
          type: "number",
          min: 30,
          max: 3600,
          value: interval,
          onChange: (e) => setIntervalValue(e.target.value),
        }),
      ),
    ),
    h(
      "button",
      { style: button, type: "submit", disabled: busy },
      busy ? "Saving…" : "Save global settings",
    ),
  );
}
