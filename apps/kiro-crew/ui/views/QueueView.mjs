import { createElement as h } from "react";
import { box, input, button } from "../styles.mjs";
import { QueueEntry } from "../components/QueueEntry.mjs";

export function QueueView({
  queueRepo,
  setQueueRepo,
  data,
  busy,
  action,
  setNotice,
  pendingCount,
  showHistory,
  setShowHistory,
  items,
  queueState,
  active,
  setError,
}) {
  return h(
    "div",
    null,
    h(
      "section",
      { style: box },
      h(
        "label",
        null,
        "Filter by repository",
        h(
          "select",
          {
            style: input,
            value: queueRepo,
            onChange: (e) => setQueueRepo(e.target.value),
          },
          h("option", { value: "" }, "All repositories"),
          ...data.config.repos.map((repo) =>
            h("option", { key: repo, value: repo }, repo),
          ),
        ),
      ),
      h(
        "button",
        {
          style: button,
          disabled: busy,
          onClick: async () => {
            if (
              window.confirm(
                "Dismiss all pending items " +
                  (queueRepo ? "in " + queueRepo : "across all repositories") +
                  "? Running sessions and history are kept. Future activity can queue new work.",
              )
            ) {
              if (
                await action(
                  "/queue/clear",
                  queueRepo ? { repo: queueRepo } : {},
                )
              )
                setNotice("Pending queue cleared. Running sessions were kept.");
            }
          },
        },
        "Clear pending queue",
      ),
      h(
        "p",
        { style: { opacity: 0.7 } },
        "Queue count = pending PRs. The Crew sidebar badge counts unread notifications, including session starts and errors.",
      ),
    ),
    h(
      "p",
      { style: { opacity: 0.65, fontSize: 13 } },
      `${pendingCount} pending PRs · ${queueRepo || "all repositories"}`,
    ),
    h(
      "label",
      null,
      h("input", {
        type: "checkbox",
        checked: showHistory,
        onChange: (e) => setShowHistory(e.target.checked),
      }),
      " Show completed, dismissed and older activity",
    ),
    !items.length &&
      h(
        "section",
        { style: { ...box, padding: 36, textAlign: "center" } },
        h("h2", null, "Nothing queued yet"),
        h(
          "p",
          null,
          data.config.repos.length
            ? "Actionable PR activity will appear here after scanning."
            : "Add a repository to start watching.",
        ),
      ),
    ...items.map((item) =>
      h(QueueEntry, {
        key: item.id,
        item,
        queueState,
        data,
        busy,
        action,
        setNotice,
        active,
        setError,
      }),
    ),
  );
}
