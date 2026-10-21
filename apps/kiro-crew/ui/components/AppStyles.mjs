import { createElement as h } from "react";

export function AppStyles() {
  return h(
    "style",
    null,
    `.outrider-app { line-height: 1.6; }
    .outrider-header { display: grid; grid-template-columns: minmax(0, 1fr); gap: 20px; margin-bottom: 24px; }
    .outrider-heading { display: grid; grid-template-columns: 44px minmax(0, 1fr); column-gap: 14px; align-items: center; min-width: 0; }
    .outrider-heading img { grid-column: 1; grid-row: 1; }
    .outrider-heading h1 { grid-column: 2; grid-row: 1; overflow-wrap: normal; word-break: normal; }
    .outrider-heading p { grid-column: 1 / -1; padding-top: 8px; overflow-wrap: break-word; }
    .outrider-header nav { justify-self: end; justify-content: flex-end; width: 100%; max-width: 800px; min-width: 0; }
    .outrider-header nav button { flex: 0 0 auto; white-space: nowrap; }
    .outrider-app .outrider-header nav button + button { margin-left: 0; }
    .outrider-app p { margin: 12px 0 20px; }
    .outrider-app h2, .outrider-app h3 { line-height: 1.3; margin: 24px 0 16px; }
    .outrider-app label { display: block; margin-bottom: 16px; }
    .outrider-app details[open] > summary { margin-bottom: 20px; }
    .outrider-app button + p { margin-top: 18px; }
    .outrider-app section > :last-child { margin-bottom: 0; }
    .outrider-app legend { padding: 0 8px; }
    .outrider-app button + button { margin-left: 8px; }`,
  );
}
