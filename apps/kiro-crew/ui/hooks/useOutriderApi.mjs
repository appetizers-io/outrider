import { useCallback, useEffect, useState } from "react";
import { useAppApi } from "@kirocrew/app-sdk";
import { base } from "../api.mjs";

export function useOutriderApi() {
  const api = useAppApi();
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const [connectionError, setConnectionError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = useCallback(async () => {
    try {
      const next = await api.get(base + "/status");
      setData(next);
      setConnectionError("");
      return next;
    } catch (e) {
      setConnectionError(e.message || "Could not reach Outrider");
    }
  }, [api]);
  useEffect(() => {
    refresh();
    const timer = setInterval(refresh, 4000);
    return () => clearInterval(timer);
  }, [refresh]);
  async function action(path, payload = {}) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await api.post(base + path, payload);
      await refresh();
      return true;
    } catch (e) {
      setError(e.message || "Request failed");
      return false;
    } finally {
      setBusy(false);
    }
  }

  return {
    api,
    data,
    error,
    setError,
    connectionError,
    notice,
    setNotice,
    busy,
    setBusy,
    refresh,
    action,
  };
}
