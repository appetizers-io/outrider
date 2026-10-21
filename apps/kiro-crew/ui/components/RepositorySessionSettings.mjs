import {
  MemberOptions,
  TrustOptions,
  FolderSelect,
} from "./SessionControls.mjs";
import { createElement as h } from "react";
import { box, input, button } from "../styles.mjs";
import { base } from "../api.mjs";

export function RepositorySessionSettings({
  busy,
  selectedRepo,
  selectRepo,
  data,
  folders,
  mapping,
  setMapping,
  setCheckoutContext,
  validationRemotes,
  setValidationRemotes,
  automatic,
  setAutomatic,
  automaticDefault,
  reviewers,
  override,
  setReviewers,
  members,
  trustOverrides,
  setTrustOverrides,
  setBusy,
  setError,
  api,
  checkoutContext,
}) {
  return h(
    "section",
    { style: box },
    h(
      "label",
      null,
      "Repository",
      h(
        "select",
        {
          style: input,
          disabled: busy,
          value: selectedRepo,
          onChange: (e) => selectRepo(e.target.value),
        },
        ...data.config.repos.map((repo) =>
          h("option", { key: repo, value: repo }, repo),
        ),
      ),
    ),
    h(
      "label",
      null,
      "Crew folder",
      h(FolderSelect, {
        folders,
        value: mapping[selectedRepo] || "",
        onChange: (e) => {
          setMapping({ ...mapping, [selectedRepo]: e.target.value });
          setCheckoutContext(null);
          const next = { ...validationRemotes };
          delete next[selectedRepo];
          setValidationRemotes(next);
        },
      }),
    ),
    h("h3", null, "Automatic sessions"),
    h(
      "label",
      null,
      "Automatic sessions for this repository",
      h(
        "select",
        {
          style: input,
          value: Object.hasOwn(automatic, selectedRepo)
            ? String(automatic[selectedRepo])
            : "__global",
          onChange: (e) => {
            const next = { ...automatic };
            if (e.target.value === "__global") delete next[selectedRepo];
            else next[selectedRepo] = e.target.value === "true";
            setAutomatic(next);
          },
        },
        h(
          "option",
          { value: "__global" },
          "Use global setting (" + (automaticDefault ? "On" : "Off") + ")",
        ),
        h("option", { value: "true" }, "On"),
        h("option", { value: "false" }, "Off"),
      ),
    ),
    h(
      "label",
      null,
      "Reviewer Crew member",
      h(
        "select",
        {
          style: input,
          value: reviewers[selectedRepo] ?? "__global",
          onChange: (e) => override(reviewers, setReviewers, e.target.value),
        },
        h("option", { value: "__global" }, "Use global reviewer"),
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
          value: trustOverrides[selectedRepo] ?? "__global",
          onChange: (e) =>
            override(trustOverrides, setTrustOverrides, e.target.value),
        },
        h("option", { value: "__global" }, "Use global trust setting"),
        h(TrustOptions),
      ),
    ),
    h(
      "p",
      null,
      "Enabled: new queued work starts automatically, up to the session limit. Disabled: keep work in the queue for manual review. Requires a Crew folder and matching remote below. Existing backlog is not launched when enabling.",
    ),
    h("h3", null, "Local validation"),
    h(
      "p",
      null,
      "Watch this GitHub repository and validate PRs in an isolated worktree of the Crew folder’s checkout. Crew controls the agent, model and permissions.",
    ),
    h(
      "button",
      {
        style: button,
        type: "button",
        disabled: busy || !mapping[selectedRepo],
        onClick: async () => {
          setBusy(true);
          setError("");
          try {
            setCheckoutContext(
              await api.post(base + "/checkout", {
                folder_id: mapping[selectedRepo],
              }),
            );
          } catch (e) {
            setError(e.message);
          } finally {
            setBusy(false);
          }
        },
      },
      "Detect checkout and remotes",
    ),
    checkoutContext &&
      h("p", null, "Local checkout: " + checkoutContext.checkout),
    checkoutContext &&
      h(
        "label",
        null,
        "Remote to watch",
        h(
          "select",
          {
            style: input,
            value: validationRemotes[selectedRepo] || "",
            onChange: (e) => {
              const next = { ...validationRemotes };
              if (e.target.value) next[selectedRepo] = e.target.value;
              else delete next[selectedRepo];
              setValidationRemotes(next);
            },
          },
          h("option", { value: "" }, "Snapshot reviews only"),
          ...checkoutContext.remotes
            .filter((r) => r.repo.toLowerCase() === selectedRepo.toLowerCase())
            .map((r) =>
              h(
                "option",
                { key: r.name, value: r.name },
                r.name + " · " + r.repo,
              ),
            ),
        ),
      ),
    checkoutContext &&
      !checkoutContext.remotes.some(
        (r) => r.repo.toLowerCase() === selectedRepo.toLowerCase(),
      ) &&
      h(
        "p",
        { role: "alert" },
        "No GitHub remote matches this watched repository. Add its upstream remote to the checkout, or select the correct repository.",
      ),
    validationRemotes[selectedRepo] &&
      h(
        "p",
        null,
        "Local validation remote: " +
          validationRemotes[selectedRepo] +
          ". Save settings to apply. Automatic sessions use this checkout; you can also start queued work manually.",
      ),
  );
}
