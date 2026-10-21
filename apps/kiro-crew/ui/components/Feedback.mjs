import { createElement as h } from "react";
import { box, button } from "../styles.mjs";

export function Feedback({
  error,
  view,
  data,
  notice,
  setNotice,
  connectionError,
  refresh,
}) {
  return h(
    "div",
    null,
    (error || (view === "inbox" && data?.error)) &&
      h(
        "p",
        { role: "alert", style: { ...box, borderColor: "#c66" } },
        error || data.error,
      ),
    notice &&
      h(
        "aside",
        {
          role: "status",
          "aria-live": "polite",
          style: {
            position: "fixed",
            bottom: 24,
            right: 24,
            zIndex: 1000,
            maxWidth: "min(480px, calc(100vw - 48px))",
            display: "flex",
            gap: 16,
            alignItems: "center",
            padding: "16px 20px",
            borderRadius: 12,
            border: "1px solid #59b98a",
            background: "#173c2c",
            color: "#e8fff1",
            boxShadow: "0 8px 32px #0006",
          },
        },
        h("span", null, "✓ " + notice),
        h(
          "button",
          {
            style: button,
            type: "button",
            "aria-label": "Dismiss notification",
            onClick: () => setNotice(""),
          },
          "×",
        ),
      ),
    connectionError &&
      h(
        "p",
        { role: "status", style: box },
        "Connection to Outrider interrupted. Retrying automatically… ",
        h("button", { style: button, onClick: refresh }, "Retry now"),
        h("details", null, h("summary", null, "Details"), connectionError),
      ),
    !data &&
      !error &&
      !connectionError &&
      h("p", { role: "status" }, "Loading…"),
  );
}
