import { createElement as h } from "react";
import { box, button } from "../styles.mjs";

export function QueueEntry({
  item,
  queueState,
  data,
  busy,
  action,
  setNotice,
  active,
  setError,
}) {
  return h(
    "article",
    { key: item.id, style: box },
    h(
      "div",
      {
        style: {
          display: "flex",
          gap: 12,
          justifyContent: "space-between",
          flexWrap: "wrap",
        },
      },
      h(
        "div",
        null,
        h(
          "a",
          {
            href: item.url,
            target: "_blank",
            rel: "noopener noreferrer",
            style: { color: "inherit", fontWeight: 650, fontSize: 17 },
          },
          item.title,
        ),
        h(
          "p",
          { style: { opacity: 0.7, fontSize: 13, margin: "6px 0" } },
          `${item.repo} #${item.pr} · ${item.head.slice(0, 8)} · ${item.trigger}${item.workflow ? " · " + item.workflow : ""}`,
        ),
      ),
      h("span", { style: { fontSize: 13, opacity: 0.75 } }, queueState(item)),
    ),
    h(
      "p",
      { style: { fontSize: 13, opacity: 0.7 } },
      item.truncated
        ? "Captured context is partial. Local validation can inspect the full checkout."
        : (data.config.automatic?.[item.repo] ?? data.config.automatic_default)
          ? "Automatic processing enabled for this repository."
          : "Manual processing: start a Crew session or review the captured context.",
    ),
    !item.dismissed &&
      ![
        "preparing",
        "dispatching",
        "started",
        "running",
        "requested",
        "uncertain",
      ].includes(queueState(item)) &&
      h(
        "button",
        {
          style: button,
          disabled: busy,
          onClick: async () => {
            if (await action("/dismiss", { id: item.id }))
              setNotice(
                "Item dismissed. Future PR activity can still create new work; remove the reaction or disable its trigger to stop watching.",
              );
          },
        },
        "Dismiss",
      ),
    item.run.awaiting_approval &&
      h(
        "p",
        null,
        "Crew is waiting for approval. Open Crew tasks to inspect it.",
      ),
    item.run.error &&
      h(
        "details",
        null,
        h("summary", null, "Previous text-only review failed"),
        h("p", { style: { color: "#c66" } }, item.run.error),
      ),
    item.run.result &&
      h(
        "details",
        { open: true },
        h(
          "summary",
          { style: { cursor: "pointer", marginBottom: 12 } },
          "Findings",
        ),
        h(
          "pre",
          {
            style: {
              whiteSpace: "pre-wrap",
              overflowWrap: "anywhere",
              fontFamily: "inherit",
              lineHeight: 1.65,
              fontSize: 14,
            },
          },
          item.run.result,
        ),
      ),
    !item.dismissed &&
      data.config.validation_remotes?.[item.repo] &&
      (!item.validation ||
        (item.validation.status === "failed" && !item.validation.worktree)) &&
      h(
        "button",
        {
          style: button,
          disabled: busy,
          onClick: () => action("/validate", { id: item.id }),
        },
        "Start session",
      ),
    !item.dismissed &&
      item.validation?.status === "stopped" &&
      h(
        "button",
        {
          style: button,
          disabled: busy || active,
          onClick: () => action("/validate", { id: item.id, retry: true }),
        },
        "Retry session",
      ),
    item.validation &&
      h(
        "div",
        { style: box },
        h(
          "p",
          null,
          "Local validation: " +
            item.validation.status +
            (item.validation.status === "started"
              ? " — open the session for progress, approvals, and findings."
              : ""),
        ),
        item.validation.slot &&
          !item.validation.session_missing &&
          h(
            "p",
            null,
            h(
              "a",
              {
                href: "/chat?sid=" + encodeURIComponent(item.validation.slot),
                style: {
                  ...button,
                  display: "inline-block",
                  textDecoration: "none",
                },
                "aria-label":
                  "Open Crew session for " + item.repo + " PR " + item.pr,
              },
              "Open Crew session ↗",
            ),
          ),
        !item.validation.slot &&
          item.validation.status === "preparing" &&
          h(
            "p",
            { style: { opacity: 0.7 } },
            "Preparing the checkout. The session link will appear here when Crew creates the chat.",
          ),
        item.validation.worktree &&
          h(
            "p",
            { style: { overflowWrap: "anywhere" } },
            "Worktree: " + item.validation.worktree,
          ),
        item.validation.tracking_error &&
          h("p", { role: "status" }, item.validation.tracking_error),
        item.validation.error &&
          h("p", { role: "alert" }, item.validation.error),
      ),
    h(
      "details",
      { style: { marginTop: 20 } },
      h("summary", null, "Advanced: review captured text without tools"),
      !item.dismissed &&
        item.run.status === "pending" &&
        h(
          "button",
          {
            style: button,
            disabled: busy || active,
            onClick: () => action("/review", { id: item.id }),
          },
          "Review without tools",
        ),
      ["failed", "completed", "uncertain"].includes(item.run.status) &&
        h(
          "button",
          {
            style: button,
            disabled: busy || active,
            onClick: () => {
              if (
                item.run.status !== "uncertain" ||
                window.confirm(
                  "Crew may already be running this review. Check Crew tasks first. Start another review?",
                )
              )
                action("/review", { id: item.id, retry: true });
            },
          },
          "Retry text-only review",
        ),
    ),
    item.run.result &&
      h(
        "button",
        {
          style: { ...button, marginLeft: 8 },
          onClick: async () => {
            try {
              await navigator.clipboard.writeText(
                `Outrider review: ${item.repo} #${item.pr}\nSnapshot: ${item.head}\n${item.url}\n\n${item.run.result}`,
              );
              setNotice(
                "Findings copied. Paste them into your existing Crew chat.",
              );
            } catch {
              setError(
                "Clipboard unavailable. Select and copy the findings above.",
              );
            }
          },
        },
        "Copy findings to existing chat",
      ),
    item.run.status === "completed" &&
      data.config.folders?.[item.repo] &&
      !item.run.delivery &&
      h(
        "button",
        {
          style: { ...button, marginLeft: 8 },
          disabled: busy,
          onClick: () => action("/deliver", { id: item.id }),
        },
        "Save to new folder chat",
      ),
    item.run.delivery &&
      h(
        "p",
        { role: "status" },
        item.run.delivery.status === "saved"
          ? "Findings saved in your Crew folder."
          : "Delivery may be incomplete. Check your Crew folder before creating another chat.",
      ),
    item.run.task_id &&
      h(
        "p",
        { style: { fontSize: 12, opacity: 0.55 } },
        "Crew task: " + item.run.task_id,
      ),
  );
}
