import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";
import test from "node:test";
import { selectQueue } from "../ui/queue.mjs";

const root = fileURLToPath(new URL("../ui/", import.meta.url));
const fixture = () => ({
  config: {
    repos: ["o/r", "o/other"],
    interval: 60,
    max_sessions: 3,
    folders: { "o/r": "folder" },
    validation_remotes: { "o/r": "upstream" },
    repo_settings: {
      "o/r": {
        workflows: [
          { name: "review", on: "activity", steps: ["Inspect code"] },
        ],
      },
    },
  },
  items: [
    {
      id: "a",
      repo: "o/r",
      pr: 1,
      title: "Fix a bug",
      head: "12345678",
      run: { status: "pending" },
      validation: { status: "stopped", session_missing: true, slot: "closed" },
    },
    {
      id: "b",
      repo: "o/other",
      pr: 2,
      title: "Update docs",
      head: "abcdefgh",
      run: { status: "pending" },
    },
  ],
  events: [{ time: 1, level: "info", message: "Polling restored: active" }],
});

// Load the same build-free module graph as Crew. React/SDK are host-provided;
// this harness checks component contracts and actions, not browser layout.
async function mount(data = fixture()) {
  const values = [],
    requests = [],
    modules = new Map();
  let cursor = 0;
  const api = {
    get: async (url) =>
      url.endsWith("/status")
        ? data
        : url.endsWith("/folders")
          ? [{ id: "folder", name: "Project" }]
          : url.endsWith("/members")
            ? { agents: [{ name: "reviewer" }] }
            : {},
    post: async (url, payload) => {
      requests.push({ url, payload });
      if (url.endsWith("/config")) data.config = payload;
      return { settings: {}, yaml: "{}" };
    },
  };
  const react = {
    createElement: (type, props, ...children) => ({
      type,
      props: props || {},
      children,
    }),
    useState: (initial) => {
      const i = cursor++;
      if (!(i in values)) values[i] = initial;
      return [
        values[i],
        (next) => {
          values[i] = typeof next === "function" ? next(values[i]) : next;
        },
      ];
    },
    useCallback: (fn) => fn,
    useEffect: () => {},
    useRef: (value) => ({ current: value }),
  };
  const context = vm.createContext({
    URL,
    setInterval,
    clearInterval,
    console,
    window: { confirm: () => true },
    navigator: { clipboard: { writeText: async () => {} } },
  });
  for (const [name, exports] of [
    ["react", react],
    ["@kirocrew/app-sdk", { useAppApi: () => api }],
  ]) {
    modules.set(
      name,
      new vm.SyntheticModule(
        Object.keys(exports),
        function () {
          for (const [key, value] of Object.entries(exports))
            this.setExport(key, value);
        },
        { context },
      ),
    );
  }
  async function load(file) {
    if (!modules.has(file))
      modules.set(
        file,
        new vm.SourceTextModule(await readFile(file, "utf8"), {
          identifier: file,
          context,
        }),
      );
    return modules.get(file);
  }
  const entry = await load(path.join(root, "index.mjs"));
  await entry.link(
    (name, parent) =>
      modules.get(name) ||
      load(path.resolve(path.dirname(parent.identifier), name)),
  );
  await entry.evaluate();
  const controller = () => {
    cursor = 0;
    return modules
      .get(path.join(root, "hooks/useOutriderController.mjs"))
      .namespace.useOutriderController();
  };
  function expand(node) {
    if (Array.isArray(node)) return node.flatMap(expand);
    if (!node || typeof node !== "object") return node ? [node] : [];
    if (typeof node.type === "function")
      return expand(node.type({ ...node.props, children: node.children }));
    return [{ ...node, children: node.children.flatMap(expand) }];
  }
  const render = () => {
    cursor = 0;
    return expand(entry.namespace.default());
  };
  await controller().feedback.refresh();
  return { controller, render, requests, data };
}
function nodes(tree) {
  return tree.flatMap((n) =>
    typeof n === "object" ? [n, ...nodes(n.children)] : [],
  );
}
function text(tree) {
  return tree
    .map((n) => (typeof n === "object" ? text(n.children) : String(n)))
    .join(" ");
}

test("every view renders through the complete module graph", async () => {
  const app = await mount();
  for (const [view, expected] of [
    ["inbox", "Fix a bug"],
    ["repositories", "Import CLI configuration"],
    ["settings", "Global session defaults"],
    ["setup", "Review folder"],
    ["activity", "Polling restored: active"],
  ]) {
    await app.controller().header.configure(view);
    const tree = app.render();
    assert.ok(text(tree).includes(expected), view);
    assert.ok(text(tree).includes("Support / report an issue"));
    const selected = nodes(tree).filter(
      (n) => n.props["aria-current"] === "page",
    );
    assert.equal(selected.length, 1);
  }
});

test("queue filters, session links and explicit retry preserve their actions", async () => {
  const app = await mount();
  app.controller().queue.setQueueRepo("o/r");
  let tree = app.render();
  assert.ok(!text(tree).includes("Update docs"));
  const retry = nodes(tree).find(
    (n) => n.type === "button" && text(n.children) === "Retry session",
  );
  await retry.props.onClick();
  assert.equal(app.requests.at(-1).url, "/apps/outrider/api/validate");
  assert.equal(app.requests.at(-1).payload.retry, true);
  assert.equal(
    nodes(tree).filter(
      (n) => n.type === "a" && n.props.href?.startsWith("/chat?sid="),
    ).length,
    0,
  );
  app.data.items[0].validation = { status: "started", slot: "session/one" };
  tree = app.render();
  assert.ok(
    nodes(tree).some((n) => n.props.href === "/chat?sid=session%2Fone"),
  );
});

test("global save does not persist unsaved repository drafts", async () => {
  const app = await mount();
  await app.controller().header.configure("settings");
  app
    .controller()
    .repository.editor.editOptions((current) => ({
      ...current,
      ignore_authors: ["draft-only"],
    }));
  app.controller().settings.setLookback(4);
  await app.controller().settings.saveGlobal({ preventDefault() {} });
  const payload = app.requests.at(-1).payload;
  assert.equal(payload.lookback_hours, 4);
  assert.equal(payload.repo_settings["o/r"].ignore_authors, undefined);
});

test("queue selector keeps active older work and hides completed history", () => {
  const data = {
    items: [
      { id: "new", repo: "o/r", pr: 1, run: { status: "pending" } },
      {
        id: "active",
        repo: "o/r",
        pr: 1,
        run: { status: "pending" },
        validation: { status: "started" },
      },
      { id: "done", repo: "o/r", pr: 2, run: { status: "completed" } },
    ],
  };
  assert.deepEqual(
    selectQueue(data, "o/r", false).items.map((i) => i.id),
    ["new", "active"],
  );
  assert.equal(selectQueue(data, "", false).pendingCount, 1);
  assert.equal(selectQueue(data, "", true).items.length, 3);
});
