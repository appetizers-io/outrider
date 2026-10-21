import { createElement as h, useEffect, useState, useRef } from "react";
import { base } from "../api.mjs";
import { box, button } from "../styles.mjs";

export function DoctorView({
  api,
  onReady,
  onSetup,
  autoProvision = false,
  readOnly = false,
  setupLabel = "Open setup wizard",
}) {
  const attempted = useRef(false);
  const [report, setReport] = useState(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  async function check() {
    setBusy(true);
    setError("");
    try {
      setReport(await api.get(base + "/doctor"));
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    check();
  }, [api]);
  useEffect(() => {
    if (report?.installation?.status !== "running") return;
    const timer = setInterval(check, 2000);
    return () => clearInterval(timer);
  }, [report?.installation?.status, api]);
  useEffect(() => {
    if (
      autoProvision &&
      !attempted.current &&
      report?.runtime.status === "missing" &&
      report.runtime.download_available &&
      report.installation?.status === "idle"
    ) {
      attempted.current = true;
      install();
    }
  }, [report, autoProvision]);
  const installing = report?.installation?.status === "running";
  async function install() {
    if (readOnly) return;
    setBusy(true);
    setError("");
    try {
      await api.post(base + "/runtime/install", {
        allow_incompatible: report.runtime.status === "incompatible",
      });
      await check();
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  return h(
    "section",
    { style: box },
    h("h2", null, "Check your setup"),
    h(
      "p",
      null,
      "Checks run on the machine hosting Crew. Your standalone Outrider configuration stays separate.",
    ),
    error && h("p", { role: "alert" }, error),
    report?.installation?.status === "failed" &&
      h("p", { role: "alert" }, report.installation.error),
    installing &&
      h(
        "p",
        { role: "status" },
        "Downloading and verifying the private runtime…",
      ),
    busy &&
      h(
        "p",
        { role: "status" },
        "Checking or preparing your runtime… This may take a minute.",
      ),
    report &&
      h(
        "div",
        null,
        h("h3", null, "Outrider · " + report.runtime.status),
        report.runtime.status === "ready"
          ? h(
              "p",
              null,
              `${report.runtime.source === "host" ? "Host installation" : "Plugin-managed runtime"}: ${report.runtime.path}`,
            )
          : h(
              "div",
              null,
              h(
                "p",
                null,
                report.runtime.override
                  ? "The configured OUTRIDER_BIN is unavailable or incompatible. Fix or remove that override."
                  : report.runtime.status === "incompatible"
                    ? "The host installation is incompatible. You can use a private copy without changing it."
                    : "No host Outrider was found.",
              ),
              report.runtime.download_available
                ? h(
                    "button",
                    {
                      style: button,
                      onClick: install,
                      disabled: busy || installing || readOnly,
                    },
                    report.runtime.status === "incompatible"
                      ? "Use private runtime"
                      : "Install private runtime",
                  )
                : h(
                    "p",
                    null,
                    "This development build has no published runtime pinned for this platform. Install a compatible host build, or use a published plugin release.",
                  ),
            ),
        ...report.checks.map((c) =>
          h(
            "div",
            { key: c.name, style: { marginTop: 24 } },
            h("h3", null, `${c.ready ? "✓" : "○"} ${c.name}`),
            h("p", null, c.ready ? c.path : c.help),
          ),
        ),
        h(
          "div",
          { style: { display: "flex", gap: 12, marginTop: 24 } },
          h(
            "button",
            { style: button, disabled: busy || installing, onClick: check },
            "Check again",
          ),
          onReady &&
            h(
              "button",
              {
                style: button,
                disabled: busy || installing || !report.ready,
                onClick: onReady,
              },
              "Continue",
            ),
          onSetup &&
            h(
              "button",
              { style: button, disabled: busy || installing, onClick: onSetup },
              setupLabel,
            ),
        ),
      ),
  );
}
