import { DoctorView } from "./views/DoctorView.mjs";
import { WelcomeView } from "./views/WelcomeView.mjs";
import { createElement as h } from "react";
import { useOutriderController } from "./hooks/useOutriderController.mjs";
import { AppHeader } from "./components/AppHeader.mjs";
import { Feedback } from "./components/Feedback.mjs";
import { ActivityView } from "./views/ActivityView.mjs";
import { AddRepositoryView } from "./views/AddRepositoryView.mjs";
import { SettingsView } from "./views/SettingsView.mjs";
import { QueueView } from "./views/QueueView.mjs";
import { RepositoriesView } from "./views/RepositoriesView.mjs";
import { ImportPreviewDialog } from "./components/ImportPreviewDialog.mjs";
import { AppFooter } from "./components/AppFooter.mjs";
import { AppStyles } from "./components/AppStyles.mjs";
export default function Outrider() {
  const state = useOutriderController();
  const { data, view } = state;
  return h(
    "main",
    {
      className: "outrider-app",
      style: {
        containerType: "inline-size",
        padding: 28,
        overflowY: "auto",
        maxWidth: 1100,
        width: "100%",
        boxSizing: "border-box",
        margin: "0 auto",
        color: "inherit",
      },
    },
    h(AppStyles),
    h(AppHeader, state.header),
    data &&
      h(
        "p",
        { style: { opacity: 0.7 } },
        `${data.config.paused ? "Polling inactive" : "Always-on polling · every " + data.config.interval + "s"} · ${data.active_sessions || 0}/${data.config.max_sessions || 1} sessions active. Status is restored when Crew starts.`,
      ),
    h(Feedback, state.feedback),
    state.importPreview &&
      h(ImportPreviewDialog, {
        preview: state.importPreview,
        onDismiss: () => state.setImportPreview(null),
        onUse: state.useImport,
      }),
    data && view === "doctor" && h(DoctorView, state.doctor),
    data && view === "welcome" && h(WelcomeView, state.welcome),
    data && view === "inbox" && h(QueueView, state.queue),
    data && view === "repositories" && h(RepositoriesView, state.repository),
    data && view === "settings" && h(SettingsView, state.settings),
    data && view === "setup" && h(AddRepositoryView, state.add),
    data && view === "activity" && h(ActivityView, state.activity),
    h(AppFooter),
  );
}
