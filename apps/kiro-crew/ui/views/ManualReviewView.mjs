import { createElement as h, useState } from "react";
import { box, button, input } from "../styles.mjs";
export function ManualReviewView({ data, action, busy, setView }) {
  const [url, setUrl] = useState("");
  const job = data.manual || {};
  return h(
    "section",
    { style: box },
    h("h2", null, "Review a pull request"),
    h(
      "p",
      null,
      "Paste a PR URL to start a review in its configured Crew folder. No reaction or notification is needed. Matching opt-in workflows apply; Crew keeps your reviewer, trust and session limits.",
    ),
    h(
      "form",
      {
        onSubmit: async (e) => {
          e.preventDefault();
          await action("/manual", { url });
        },
      },
      h(
        "label",
        null,
        "GitHub PR URL",
        h("input", {
          style: input,
          type: "url",
          required: true,
          value: url,
          placeholder: "https://github.com/owner/repo/pull/123",
          onChange: (e) => setUrl(e.target.value),
        }),
      ),
      h(
        "button",
        {
          style: button,
          disabled: busy || job.status === "running",
          type: "submit",
        },
        job.status === "running" ? "Preparing review…" : "Review PR",
      ),
    ),
    job.status &&
      h(
        "div",
        {
          style: { marginTop: 24 },
          role: job.status === "failed" ? "alert" : "status",
        },
        h("p", null, `${job.repo} #${job.pr}`),
        h(
          "p",
          null,
          job.status === "running"
            ? "Fetching current PR context…"
            : job.status === "started"
              ? "Session requested. Follow preparation and progress in the queue."
              : job.status === "existing"
                ? "This captured PR already has a session. Open it from the queue or use its explicit retry action."
                : job.status === "queued"
                  ? "Added to the queue. Start it from the queue when capacity is available; automatic starts follow your repository settings."
                  : job.error,
        ),
        job.id &&
          h(
            "button",
            { style: button, onClick: () => setView("inbox") },
            "Open queue",
          ),
      ),
  );
}
