import { createElement as h, useEffect, useRef } from "react";
import { button, selectedButton } from "../styles.mjs";

export function ImportPreviewDialog({ preview, onDismiss, onUse }) {
  const dialog = useRef(null);
  useEffect(() => {
    const previous = document.activeElement;
    const element = dialog.current;
    element.showModal();
    return () => {
      element.close();
      if (previous?.isConnected) previous.focus();
    };
  }, []);
  return h(
    "dialog",
    {
      ref: dialog,
      className: "outrider-import-dialog",
      "aria-labelledby": "outrider-import-title",
      onCancel: (e) => {
        e.preventDefault();
        onDismiss();
      },
      style: {
        padding: 0,
        width: "min(900px, calc(100vw - 32px))",
        maxWidth: "calc(100vw - 32px)",
        maxHeight: "85vh",
        border: "1px solid #8886",
        borderRadius: 16,
        background: "var(--background, #1c1921)",
        color: "var(--foreground, #eee)",
        boxShadow: "0 24px 80px #0008",
      },
    },
    h(
      "style",
      null,
      ".outrider-import-dialog::backdrop { background: #0009; }",
    ),
    h(
      "div",
      {
        style: { display: "flex", flexDirection: "column", maxHeight: "85vh" },
      },
      h(
        "header",
        { style: { padding: "20px 24px", borderBottom: "1px solid #8885" } },
        h(
          "h2",
          { id: "outrider-import-title", style: { margin: "0 0 8px" } },
          "Preview import",
        ),
        h("div", { style: { overflowWrap: "anywhere" } }, preview.repo),
        h(
          "p",
          { style: { marginBottom: 0, opacity: 0.7 } },
          "Review the settings below. Nothing is saved until you use this draft and save settings.",
        ),
      ),
      h(
        "div",
        { style: { padding: "16px 24px", overflowY: "auto", minHeight: 0 } },
        h(
          "p",
          null,
          `${preview.settings.workflows?.length || 0} base workflows · ${preview.settings.overrides?.length || 0} ownership overrides`,
        ),
        h(
          "details",
          null,
          h(
            "summary",
            { style: { cursor: "pointer" } },
            "Import notes and removed settings",
          ),
          ...preview.warnings.map((warning, index) =>
            h("p", { key: index }, warning),
          ),
          preview.skipped.length > 0 &&
            h("p", null, "Removed: " + preview.skipped.join(", ")),
          preview.applied_overrides.length > 0 &&
            h(
              "p",
              null,
              "Applied CLI overrides: " + preview.applied_overrides.join("; "),
            ),
        ),
        h(
          "pre",
          {
            tabIndex: 0,
            "aria-label": "Imported YAML",
            style: {
              padding: 16,
              border: "1px solid #8885",
              borderRadius: 8,
              whiteSpace: "pre",
              overflow: "auto",
              maxHeight: "45vh",
              fontSize: 13,
              lineHeight: 1.6,
              background: "#0002",
            },
          },
          preview.yaml,
        ),
      ),
      h(
        "footer",
        {
          style: {
            padding: "16px 24px",
            borderTop: "1px solid #8885",
            display: "flex",
            justifyContent: "flex-end",
            gap: 12,
          },
        },
        h(
          "button",
          {
            style: button,
            type: "button",
            autoFocus: true,
            onClick: onDismiss,
          },
          "Cancel",
        ),
        h(
          "button",
          { style: selectedButton, type: "button", onClick: onUse },
          "Use import draft",
        ),
      ),
    ),
  );
}
