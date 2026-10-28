(() => {
  "use strict";
  const link = document.querySelector("[data-pull-operation]");
  if (!link || !["pending", "recovering"].includes(link.dataset.operationState)) return;
  let timer, request, attempts = 0, stopped = false;
  const stop = () => { stopped = true; clearTimeout(timer); if (request) request.abort(); };
  const poll = async () => {
    if (stopped || attempts++ >= 8) return;
    request = new AbortController();
    const timeout = setTimeout(() => request.abort(), 10000);
    try {
      const response = await fetch(link.dataset.pullOperation, {headers: {Accept: "application/json"}, credentials: "same-origin", signal: request.signal, cache: "no-store"});
      if (!response.ok) { stop(); return; }
      const result = await response.json();
      if (["merged", "failed"].includes(result.State)) { stop(); window.location.assign(link.href); return; }
      if (!["pending", "recovering"].includes(result.State)) { stop(); return; }
    } catch (_) { stop(); return; }
    finally { clearTimeout(timeout); }
    if (!stopped) timer = setTimeout(poll, Math.min(1000 * 2 ** attempts, 15000));
  };
  window.addEventListener("pagehide", stop, {once: true});
  timer = setTimeout(poll, 1000);
})();
