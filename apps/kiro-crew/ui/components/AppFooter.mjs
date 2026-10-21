import { createElement as h } from "react";

export function AppFooter({}) {
  return h(
    "footer",
    {
      style: {
        marginTop: 32,
        paddingTop: 20,
        borderTop: "1px solid #8884",
        display: "flex",
        gap: 20,
        flexWrap: "wrap",
      },
    },
    h(
      "a",
      {
        href: "https://github.com/appetizers-io/outrider",
        target: "_blank",
        rel: "noopener noreferrer",
        style: { color: "inherit" },
      },
      "Outrider on GitHub",
    ),
    h(
      "a",
      {
        href: "https://github.com/appetizers-io/outrider/issues",
        target: "_blank",
        rel: "noopener noreferrer",
        style: { color: "inherit" },
      },
      "Support / report an issue",
    ),
  );
}
