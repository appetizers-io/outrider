import { createElement as h } from "react";

export function AppStyles() {
  return h(
    "style",
    null,
    `.outrider-app { line-height: 1.6; } .outrider-header { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 20px; align-items: start; margin-bottom: 24px; } .outrider-heading { min-width: 0; overflow-wrap: anywhere; } .outrider-header nav { justify-self: end; justify-content: flex-end; } @container (max-width: 760px) { .outrider-header { grid-template-columns: minmax(0, 1fr); } .outrider-header nav { grid-row: 1; max-width: 100%; } .outrider-heading { grid-row: 2; } } .outrider-app p { margin: 12px 0 20px; } .outrider-app h2, .outrider-app h3 { line-height: 1.3; margin: 24px 0 16px; } .outrider-app label { display: block; margin-bottom: 16px; } .outrider-app details[open] > summary { margin-bottom: 20px; } .outrider-app button + p { margin-top: 18px; } .outrider-app section > :last-child { margin-bottom: 0; } .outrider-app legend { padding: 0 8px; } .outrider-app button + button { margin-left: 8px; }`,
  );
}
