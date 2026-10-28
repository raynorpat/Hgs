(() => {
  'use strict';
  const root = document.querySelector('[data-hg-shell]');
  if (!root || root.dataset.draftsReady) return;
  root.dataset.draftsReady = 'true';
  const fields = {
    issue_new: ['assigneeid', 'milestoneid'], issue_edit: [], issue_comment: [],
    pull_new: ['source_repository', 'source_kind', 'source_name', 'source_head', 'target_kind', 'target_name', 'target_head', 'reviewers'],
    pull_edit: [], pull_comment: ['comparison_id', 'path', 'side', 'line'], pull_review: ['comparison_id', 'state']
  };
  const states = [];
  const csrf = () => document.querySelector('meta[name=_csrf]')?.content || '';
  const refreshCSRF = token => {
    const meta = document.querySelector('meta[name=_csrf]');
    if (meta) meta.content = token;
    document.querySelectorAll('input[name=_csrf]').forEach(input => { input.value = token; });
  };
  function say(s, key) { s.status.textContent = s.controls.dataset[key] || s.controls.dataset.failed; }
  function timers(s) { clearTimeout(s.idle); clearTimeout(s.maximum); s.idle = s.maximum = null; }
  function busy(s, value) {
    s.submitting = value;
    if (value) { s.form.dataset.submitting = 'true'; s.form.setAttribute('aria-busy', 'true'); }
    else { delete s.form.dataset.submitting; s.form.removeAttribute('aria-busy'); }
    s.form.querySelectorAll('button:not([type]),button[type=submit],input[type=submit]').forEach(button => {
      if (value) button.setAttribute('aria-disabled', 'true'); else button.removeAttribute('aria-disabled');
    });
  }
  async function request(s, path, body) {
    const response = await fetch(s.api + path, {
      method: body === undefined ? 'GET' : 'POST', credentials: 'same-origin', cache: 'no-store', redirect: 'error',
      headers: { 'Content-Type': 'application/json', 'X-Csrf-Token': csrf(), 'X-Hgs-Editor-Account': s.actor },
      body: body === undefined ? undefined : JSON.stringify(body)
    });
    if (!response.headers.get('Content-Type')?.includes('application/json')) throw new Error('Invalid response');
    const result = await response.json();
    if (typeof result.code !== 'string') throw new Error('Invalid response');
    return result;
  }
  function disposeGrant(s, grant) {
    if (!grant) return;
    const body = JSON.stringify({ DisposalToken: grant.DisposalToken });
    fetch(s.api + '/' + grant.PageID + '/discard', { method: 'POST', credentials: 'same-origin', redirect: 'error', keepalive: true,
      headers: { 'Content-Type': 'application/json' }, body }).catch(() => {});
  }
  function dispose(s) {
    ++s.epoch; timers(s); s.opening = s.saving = s.checking = false;
    const grant = s.grant; s.grant = null;
    disposeGrant(s, grant);
  }
  function reset(s, sensitive = false) {
    for (const input of s.inputs) {
      if (sensitive) input.value = '';
      else if (input.name === 'title') input.value = s.form.dataset.draftTitle || '';
      else if (input.name === 'content') input.value = s.form.dataset.draftBody || '';
      else if (s.kind === 'pull_review') input.value = s.initial.get(input);
      else if (input.tagName === 'SELECT') input.selectedIndex = 0;
      else input.value = '';
      input.setCustomValidity('');
    }
    s.form.querySelectorAll('input[type=file]').forEach(input => { input.value = ''; });
    s.form.dispatchEvent(new CustomEvent('hg:draft-clear', { bubbles: true }));
    ++s.generation; s.acknowledged = s.generation; s.dirtySince = 0;
  }
  function denied(s, all) {
    for (const state of all ? states : [s]) {
      dispose(state); reset(state, true); state.stopped = state.denied = true; state.blocked = true;
      delete state.form.dataset.draftTitle; delete state.form.dataset.draftBody;
      state.inputs.forEach(input => state.initial.set(input, ''));
      state.form.querySelectorAll('input,textarea,select').forEach(input => { input.disabled = true; });
      state.auth.hidden = true; busy(state, false); say(state, 'cleared');
    }
  }
  function failure(s, code) {
    if (code === 'account_changed') { denied(s, true); return; }
    if (code === 'forbidden') { denied(s, false); return; }
    if (code === 'unauthenticated' || code === 'csrf_invalid') {
      s.blocked = true; s.auth.hidden = false; timers(s); say(s, 'expired'); return;
    }
    if (code === 'expired' || code === 'discarded') { dispose(s); s.auth.hidden = false; say(s, 'failed'); return; }
    if (code === 'limit_exceeded' || code === 'recovery_unavailable') { s.unavailable = true; say(s, code === 'limit_exceeded' ? 'limit' : 'unavailable'); return; }
    if (code === 'stale_version') s.auth.hidden = false;
    say(s, code === 'invalid_payload' || code === 'validation' ? 'invalid' : 'failed');
  }
  function payload(s) {
    const metadata = {};
    for (const name of fields[s.kind]) { const input = s.form.elements.namedItem(name); if (input) metadata[name] = input.value; }
    return { Title: s.form.elements.namedItem('title')?.value || '', Body: s.form.elements.namedItem('content')?.value || '', Metadata: metadata };
  }
  function bounded(s) {
    const p = payload(s), title = s.form.elements.namedItem('title'), body = s.form.elements.namedItem('content');
    const invalidTitle = Array.from(p.Title).length > 255, invalidBody = new TextEncoder().encode(p.Body).length > 65536;
    title?.setCustomValidity(invalidTitle ? s.controls.dataset.invalid : '');
    body?.setCustomValidity(invalidBody ? s.controls.dataset.invalid : '');
    if (invalidTitle || invalidBody) { say(s, 'invalid'); return false; }
    return true;
  }
  function schedule(s) {
    if (s.stopped || s.blocked || s.unavailable || s.unknown || s.submitting || s.generation === s.acknowledged) return;
    if (!s.grant) { open(s); return; }
    clearTimeout(s.idle);
    s.idle = setTimeout(() => save(s), Math.max(0, s.lastInput + 1000 - Date.now()));
    if (!s.maximum) s.maximum = setTimeout(() => save(s), Math.max(0, s.dirtySince + 5000 - Date.now()));
  }
  async function open(s) {
    if (s.opening || s.grant || s.stopped || s.unavailable || s.blocked) return;
    const useLock = typeof navigator.locks?.request === 'function';
    if (!useLock && s.controls.dataset.draftSessionEstablished !== 'true') { failure(s, 'recovery_unavailable'); return; }
    const epoch = s.epoch; s.opening = true;
    const exchange = async () => {
      if (epoch !== s.epoch) return;
      const result = await request(s, '/open', { Target: s.target, CollectionMode: useLock ? 'random' : 'session' });
      if (epoch !== s.epoch) { disposeGrant(s, result.grant); return; }
      if (result.code !== 'ok') { failure(s, result.code); return; }
      const grant = result.grant;
      const session = await request(s, '/session');
      if (epoch !== s.epoch) { disposeGrant(s, grant); return; }
      if (session.code !== 'ok') { disposeGrant(s, grant); failure(s, session.code); return; }
      if (!session.collectionPresent) { disposeGrant(s, grant); failure(s, 'recovery_unavailable'); return; }
      refreshCSRF(session.csrf); s.grant = grant; s.version = 0;
    };
    try {
      if (useLock) await navigator.locks.request('hgs-editor-draft-cookie:' + s.api, exchange); else await exchange();
    } catch (_) { if (epoch === s.epoch) say(s, 'failed'); }
    finally { if (epoch === s.epoch) { s.opening = false; if (s.grant) schedule(s); } }
  }
  async function save(s) {
    timers(s);
    if (s.saving || s.checking || !s.grant || s.stopped || s.blocked || s.unavailable || s.submitting || s.unknown || s.generation === s.acknowledged || !bounded(s)) return;
    const epoch = s.epoch, generation = s.generation, grant = s.grant;
    s.saving = true; say(s, 'saving'); let saved = false;
    try {
      const result = await request(s, '/' + grant.PageID + '/save', { PageSecret: grant.PageSecret, Version: s.version, Payload: payload(s) });
      if (epoch !== s.epoch) return;
      if (result.code !== 'ok') { failure(s, result.code); return; }
      s.version = result.snapshot.Version; s.acknowledged = generation; saved = true;
      if (generation === s.generation) { s.dirtySince = 0; say(s, 'saved'); }
    } catch (_) { if (epoch === s.epoch) say(s, 'failed'); }
    finally { if (epoch === s.epoch) { s.saving = false; if (saved && s.generation > s.acknowledged) schedule(s); } }
  }
  async function check(s) {
    if (s.checking || s.saving || s.opening || s.submitting || s.stopped || s.unknown) return;
    const epoch = s.epoch, generation = s.generation; s.checking = true;
    try {
      const session = await request(s, '/session');
      if (epoch !== s.epoch) return;
      if (session.code !== 'ok') { failure(s, session.code); return; }
      if (String(session.actorID) !== s.actor) { denied(s, true); return; }
      refreshCSRF(session.csrf);
      if (s.grant) {
        const result = await request(s, '/' + s.grant.PageID + '/restore', { PageSecret: s.grant.PageSecret });
        if (epoch !== s.epoch) return;
        if (result.code !== 'ok') { failure(s, result.code); return; }
        // Local text is authoritative whenever this document has edited it.
        if (generation === s.generation && s.generation === 0 && result.snapshot.Version > 0) {
          for (const input of s.inputs) {
            const p = result.snapshot.Payload;
            input.value = input.name === 'title' ? p.Title : input.name === 'content' ? p.Body : (p.Metadata?.[input.name] || '');
          }
        }
        s.version = result.snapshot.Version;
      }
      s.blocked = false; s.auth.hidden = true;
      if (s.generation > s.acknowledged) schedule(s);
      else if (s.grant && s.version > 0) say(s, 'saved');
      else if (!s.unavailable) { s.status.textContent = ''; open(s); }
    } catch (_) { if (epoch === s.epoch) say(s, 'failed'); }
    finally { if (epoch === s.epoch) s.checking = false; }
  }
  function resultLink(s, destination) {
    const url = new URL(destination, location.href), api = new URL(s.api, location.href);
    const prefix = api.pathname.slice(0, -'/user/drafts'.length) + '/';
    if (url.origin !== location.origin || !url.pathname.startsWith(prefix)) throw new Error('Invalid destination');
    s.result.href = url.href; s.result.target = '_blank'; s.result.rel = 'noopener'; s.result.hidden = false;
    return url.href;
  }
  root.querySelectorAll('form[data-editor-draft]').forEach(form => {
    const controls = form.querySelector('[data-draft-controls]'), kind = form.dataset.editorDraft;
    if (!controls || !fields[kind]) return;
    const inputs = Array.from(form.querySelectorAll('input,textarea,select')).filter(input => ['title', 'content', ...fields[kind]].includes(input.name));
    const initial = new Map(inputs.map(input => [input, input.tagName === 'SELECT' ? (Array.from(input.options).find(option => option.defaultSelected)?.value || input.options[0]?.value || '') : input.defaultValue]));
    const s = { form, controls, kind, inputs, initial, api: controls.dataset.draftApi, actor: controls.dataset.draftActor,
      status: controls.querySelector('[data-draft-status]'), auth: controls.querySelector('[data-draft-auth]'), result: controls.querySelector('[data-draft-result]'),
      target: { RepositoryID: Number(controls.dataset.draftRepository), Kind: kind, ItemID: Number(form.dataset.draftItem) },
      epoch: 0, generation: 0, acknowledged: 0, dirtySince: 0, lastInput: 0, version: 0 };
    states.push(s);
    for (const input of inputs) { input.value = initial.get(input); input.autocomplete = 'off'; if (input.name === 'title') input.removeAttribute('maxlength'); }
    form.addEventListener('input', event => {
      if (!inputs.includes(event.target) || s.stopped) return;
      ++s.generation; s.lastInput = Date.now(); if (!s.dirtySince) s.dirtySince = s.lastInput;
      if (bounded(s)) schedule(s);
    });
    controls.querySelector('[data-draft-check]').addEventListener('click', () => check(s));
    controls.querySelector('[data-draft-cancel]').addEventListener('click', () => {
      if (s.submitting || s.stopped) return;
      dispose(s); reset(s); if (!s.stopped && !s.unknown) { busy(s, false); say(s, 'cancelled'); }
    });
    form.addEventListener('submit', async event => {
      event.preventDefault();
      if (s.submitting || s.unknown || s.stopped) return;
      if (s.blocked) { say(s, 'expired'); return; }
      if (!bounded(s) || !form.reportValidity()) return;
      const epoch = s.epoch; busy(s, true); timers(s);
      const data = new FormData(form);
      if (event.submitter?.name) data.append(event.submitter.name, event.submitter.value);
      try {
        const response = await fetch(form.action, { method: 'POST', credentials: 'same-origin', redirect: 'error',
          headers: { 'X-Hgs-Editor-Submit': '1', 'X-Hgs-Editor-Account': s.actor, 'X-Csrf-Token': csrf() },
          body: s.kind.startsWith('issue_') ? data : new URLSearchParams(data) });
        if (epoch !== s.epoch) return;
        if (!response.headers.get('Content-Type')?.includes('application/json')) throw new Error('Invalid response');
        const result = await response.json();
        if (epoch !== s.epoch) return;
        if (result.code === 'saved' && result.committed) { const destination = resultLink(s, result.destination); dispose(s); location.assign(destination); return; }
        if (result.code === 'partial' && result.committed) {
          resultLink(s, result.destination); dispose(s); if (!result.unsaved) reset(s);
          s.unknown = true; say(s, 'partial'); return;
        }
        if (['validation', 'unauthenticated', 'account_changed', 'forbidden', 'csrf_invalid'].includes(result.code)) { busy(s, false); failure(s, result.code); return; }
        throw new Error('Unknown outcome');
      } catch (_) { if (epoch === s.epoch) { s.unknown = true; say(s, 'unknown'); if (form.dataset.draftDestination) resultLink(s, form.dataset.draftDestination); } }
    });
    open(s);
  });
  addEventListener('pagehide', () => states.forEach(s => { dispose(s); reset(s); s.stopped = true; }));
  addEventListener('pageshow', event => states.forEach(s => {
    if (event.persisted) { dispose(s); reset(s, s.denied); if (s.denied) return; s.stopped = false; s.checking = false; s.unknown = false; s.blocked = false; busy(s, false); open(s); }
    else for (const input of s.inputs) input.value = s.initial.get(input);
  }));
  addEventListener('focus', () => states.forEach(check));
})();
