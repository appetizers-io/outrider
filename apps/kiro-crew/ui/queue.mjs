export function selectQueue(data, queueRepo, showHistory) {
  const allItems = (data?.items || []).filter(
    (item) => !queueRepo || item.repo === queueRepo,
  );
  const newest = new Map();
  for (const item of allItems)
    if (!newest.has(item.repo + "#" + item.pr))
      newest.set(item.repo + "#" + item.pr, item);
  const queueState = (item) =>
    item.dismissed
      ? "dismissed"
      : item.validation?.status ||
        (item.run.status === "pending" ? "queued" : item.run.status);
  const items = showHistory
    ? allItems
    : allItems.filter(
        (item) =>
          !item.dismissed &&
          ([
            "preparing",
            "dispatching",
            "started",
            "running",
            "requested",
            "uncertain",
          ].includes(queueState(item)) ||
            (newest.get(item.repo + "#" + item.pr) === item &&
              !["completed", "superseded"].includes(queueState(item)))),
      );
  const pendingCount = [...newest.values()].filter(
    (item) => !item.dismissed && queueState(item) === "queued",
  ).length;

  return { items, pendingCount, queueState };
}
