import { createElement as h } from "react";
import { box } from "../styles.mjs";

export function ActivityView({ scanColor, scanLabel, data }) {
  return h(
    "section",
    { style: box, "aria-label": "Activity log" },
    h("p", { style: { color: scanColor } }, scanLabel),
    h(
      "p",
      { style: { opacity: 0.7 } },
      data.scan_partial
        ? "Progress saved; pending work continues on the next pass."
        : data.next_scan && !data.config.paused
          ? "Next pass in " +
            Math.max(0, Math.ceil(data.next_scan - Date.now() / 1000)) +
            " seconds."
          : "",
    ),
    h(
      "div",
      { style: { fontSize: 14, overflowWrap: "anywhere" } },
      ...(data.events || []).map((event, index) =>
        h(
          "p",
          {
            key: index,
            style: {
              margin: "8px 0",
              color: event.level === "error" ? "#e59a9a" : "inherit",
            },
          },
          new Date(event.time * 1000).toLocaleString() +
            " · " +
            event.level +
            " · " +
            event.message,
        ),
      ),
      !(data.events || []).length && h("p", null, "No activity recorded yet."),
    ),
  );
}
