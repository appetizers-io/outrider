import { createElement as h } from "react";
import { selectedButton, button } from "../styles.mjs";

export function AppHeader({
  view,
  data,
  busy,
  setView,
  setNotice,
  setError,
  pendingCount,
  configure,
  connectionError,
  action,
}) {
  return h(
    "header",
    { className: "outrider-header" },
    h(
      "div",
      { className: "outrider-heading" },
      h("img", {
        src: "/apps/outrider/ui/logo.svg",
        alt: "Outrider",
        width: 44,
        height: 44,
        style: {
          float: "left",
          marginRight: 14,
          padding: 4,
          boxSizing: "border-box",
          borderRadius: 10,
          background: "#102027",
        },
      }),
      h(
        "h1",
        { style: { margin: "0 0 6px", fontSize: 28 } },
        view === "doctor"
          ? "Doctor"
          : view === "welcome"
            ? "Welcome to Outrider"
            : view === "settings"
              ? "Settings"
              : view === "activity"
                ? "Activity log"
                : view === "setup"
                  ? "Add repository"
                  : view === "repositories"
                    ? "Repositories"
                    : "Outrider",
      ),
      h(
        "p",
        { style: { margin: 0, opacity: 0.7 } },
        view === "settings"
          ? "Global session defaults, polling, and limits."
          : view === "activity"
            ? "Polling, session progress, and errors."
            : view === "inbox"
              ? "Keep reviews moving while you focus on your work."
              : view === "setup"
                ? "Choose a repository to watch and where its reviews belong."
                : "Configure triggers and workflows for each repository.",
      ),
    ),
    data &&
      h(
        "nav",
        {
          "aria-label": "Outrider views",
          style: { display: "flex", gap: 8, flexWrap: "wrap" },
        },
        h(
          "button",
          {
            style: view === "inbox" ? selectedButton : button,
            disabled: busy,
            "aria-current": view === "inbox" ? "page" : undefined,
            onClick: () => {
              setView("inbox");
              setNotice("");
              setError("");
            },
          },
          "Queue",
          pendingCount > 0 &&
            h(
              "span",
              {
                style: {
                  marginLeft: 8,
                  borderRadius: 12,
                  padding: "2px 7px",
                  background: "#0003",
                },
              },
              pendingCount,
            ),
        ),
        h(
          "button",
          {
            style: view === "repositories" ? selectedButton : button,
            disabled: busy,
            "aria-current": view === "repositories" ? "page" : undefined,
            onClick: () => configure(),
          },
          "Repositories",
        ),
        h(
          "button",
          {
            style: view === "setup" ? selectedButton : button,
            "aria-current": view === "setup" ? "page" : undefined,
            disabled: busy,
            onClick: () => configure("setup"),
          },
          "Add repository",
        ),
        h(
          "button",
          {
            style: view === "settings" ? selectedButton : button,
            disabled: busy,
            "aria-current": view === "settings" ? "page" : undefined,
            onClick: () => configure("settings"),
          },
          "Settings",
        ),
        h(
          "button",
          {
            style: view === "activity" ? selectedButton : button,
            disabled: busy,
            "aria-current": view === "activity" ? "page" : undefined,
            onClick: () => {
              setView("activity");
              setNotice("");
              setError("");
            },
          },
          "Activity log",
        ),
        h(
          "button",
          {
            style: view === "doctor" ? selectedButton : button,
            "aria-current": view === "doctor" ? "page" : undefined,
            onClick: () => setView("doctor"),
          },
          "Doctor",
        ),
        h(
          "button",
          {
            style: {
              ...button,
              borderColor: data.config.paused ? "#8886" : "#59b98a",
              color: data.config.paused ? "inherit" : "#75d6a4",
            },
            type: "button",
            "aria-pressed": !data.config.paused,
            disabled: busy || !!connectionError,
            onClick: async () => {
              const paused = !data.config.paused;
              if (await action("/polling", { paused }))
                setNotice(
                  paused
                    ? "Polling and new automatic sessions are inactive. Running work will finish."
                    : "Polling is active and resumes automatically when Crew starts.",
                );
            },
          },
          data.config.paused ? "○ Inactive" : "● Active",
        ),
      ),
  );
}
