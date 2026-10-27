(function () {
  'use strict';
  var panel = document.querySelector('[data-fork-progress]');
  if (!panel) return;
  var stopped = false;
  var timer;
  var attempts = 0;
  var controller = new AbortController();
  window.addEventListener('pagehide', function () {
    stopped = true;
    clearTimeout(timer);
    controller.abort();
  });
  function unavailable() {
    if (!stopped) panel.querySelector('[data-fork-message]').textContent = panel.dataset.unavailable;
  }
  function poll() {
    attempts += 1;
    fetch(panel.dataset.statusUrl, {credentials: 'same-origin', cache: 'no-store', mode: 'same-origin', signal: controller.signal, headers: {'Accept': 'application/json'}})
      .then(function (response) {if (!response.ok) throw new Error('unavailable'); return response.json();})
      .then(function (status) {
        if (stopped) return;
        if (status.state === 'ready' && status.url) {
          var destination = new URL(status.url, window.location.href);
          if (destination.origin !== window.location.origin) throw new Error('invalid destination');
          window.location.replace(destination.href);
        } else if (status.state === 'provisioning') {
          if (attempts < 30) timer = setTimeout(poll, Math.min(8000, 1000 * Math.pow(2, attempts - 1)));
          else panel.querySelector('[data-fork-message]').textContent = panel.dataset.pending;
        } else unavailable();
      }).catch(unavailable);
  }
  poll();
}());
