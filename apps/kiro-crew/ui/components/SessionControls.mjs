import { createElement as h } from "react";
import { input } from "../styles.mjs";

export function FolderSelect({ folders, value, onChange, disabled }) {
  return h(
    "select",
    { style: input, value, onChange, disabled },
    h("option", { value: "" }, "Inbox only"),
    ...folders.map((folder) =>
      h("option", { key: folder.id, value: folder.id }, folder.name),
    ),
  );
}

export function MemberOptions({ members }) {
  return [
    h("option", { key: "", value: "" }, "Crew default"),
    ...members.map((member) =>
      h("option", { key: member.name, value: member.name }, member.name),
    ),
  ];
}

export function TrustOptions() {
  return [
    ["inherit", "Inherit Crew"],
    ["normal", "Normal — ask before tools"],
    ["trust_reads", "Reads — approve reads automatically"],
    ["trust", "Trust — run tools without asking"],
  ].map(([value, label]) => h("option", { key: value, value }, label));
}
