// Behavioral controller unit tests with a deliberately small DOM/transport double.
// These do not prove actual browser timing, cookies, navigation, or bfcache.
const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../public/js/app/drafts.js'), 'utf8');

async function harness(outcome = 'unknown', kind = 'issue_comment', destination = '/code/a/b/issues/1#issue-comment-2') {
  const handlers = {}, formHandlers = {}, timers = new Map(), calls = [];
  const navigation = [];
  let href = 'http://host/code/a/b/issues/1';
  const location = {
    get href() { return href; }, origin: 'http://host',
    get hash() { return new URL(href).hash; },
    set hash(value) { const next = new URL(href); next.hash = value; href = next.href; navigation.push({kind: 'fragment', url: href}); },
    assign(value) {
      const next = new URL(value, href), prior = new URL(href);
      const fragmentOnly = next.origin === prior.origin && next.pathname === prior.pathname && next.search === prior.search && next.hash !== prior.hash;
      href = next.href; navigation.push({kind: fragmentOnly ? 'fragment' : 'GET', url: href});
    },
    reload() { navigation.push({kind: 'GET', url: href}); }
  };
  let timer = 0, sessionCode = 'ok', checkCode = 'ok', saveCode = 'ok', releaseSave = null, heldSave = null;
  const body = {name: 'content', tagName: 'TEXTAREA', defaultValue: '', value: '', setCustomValidity() {}, disabled: false};
  const selectedSource = {name: 'source_repository', tagName: 'INPUT', defaultValue: 'owner/source', value: 'owner/source', setCustomValidity() {}, disabled: false};
  const title = {name: 'title', tagName: 'INPUT', defaultValue: '', value: '', setCustomValidity() {}, removeAttribute() {}, disabled: false};
  const inputs = kind === 'pull_new' ? [body, title, selectedSource] : [body];
  const status = {textContent: ''}, auth = {hidden: true}, result = {hidden: true};
  const check = {}, cancel = {};
  for (const button of [check, cancel]) button.addEventListener = (event, fn) => { button[event] = fn; };
  const controls = {
    dataset: {draftApi: '/code/user/drafts', draftActor: '1', draftRepository: '2', draftSessionEstablished: 'true',
      saving: 'Saving', failed: 'Save failed', cleared: 'Cleared', unknown: 'Unknown', partial: 'Partial', expired: 'Expired', saved: 'Saved', invalid: 'Invalid', unavailable: 'Unavailable'},
    querySelector: q => ({'[data-draft-status]': status, '[data-draft-auth]': auth, '[data-draft-result]': result,
      '[data-draft-check]': check, '[data-draft-cancel]': cancel})[q]
  };
  const form = {
    dataset: {editorDraft: kind, draftItem: kind === 'pull_new' ? '0' : '3', draftDestination: '/code/a/b/issues/1'},
    action: 'http://host/code/a/b/comment/new',
    elements: {namedItem: name => inputs.find(input => input.name === name)},
    querySelector: () => controls,
    querySelectorAll: q => q === 'input,textarea,select' ? inputs : [],
    addEventListener: (event, fn) => { formHandlers[event] = fn; },
    setAttribute() {}, removeAttribute() {}, dispatchEvent() {}, reportValidity: () => true
  };
  const shell = {dataset: {}, querySelectorAll: () => [form]};
  const response = value => ({headers: {get: () => 'application/json'}, json: async () => value});
  const fetch = async (url, options) => {
    calls.push([url, options]);
    if (url === form.action) {
      if (outcome === 'unknown') throw Error('lost response');
      if (outcome === 'validation') return response({code: 'validation'});
      if (outcome === 'saved') return response({code: 'saved', committed: true, destination});
      return response({code: 'partial', committed: true, unsaved: true, destination: '/code/a/b/issues/1'});
    }
    if (url.endsWith('/session')) return response({code: sessionCode, actorID: 1, csrf: 'fresh', collectionPresent: true});
    if (url.endsWith('/open')) return response({code: 'ok', grant: {PageID: 'page', PageSecret: 'secret', DisposalToken: 'dispose'}});
    if (url.endsWith('/check')) return response({code: checkCode});
    if (url.endsWith('/restore')) return response({code: checkCode, snapshot: {Version: 1, Payload: {Body: 'OLD SERVER TEXT'}}});
    if (url.endsWith('/save')) { if (heldSave) await heldSave; return response({code: saveCode, snapshot: {Version: 1}}); }
    if (url.endsWith('/discard')) return response({code: 'ok'});
    throw Error('unexpected request');
  };
  vm.runInNewContext(source, {
    document: {querySelector: q => q === '[data-hg-shell]' ? shell : {content: 'csrf'}, querySelectorAll: () => []},
    navigator: {}, location,
    fetch, URL, URLSearchParams, TextEncoder, CustomEvent: class {},
    FormData: class {append() {}},
    addEventListener: (event, fn) => { handlers[event] = fn; },
    setTimeout: fn => { timers.set(++timer, fn); return timer; }, clearTimeout: id => timers.delete(id)
  });
  const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
  await flush();
  body.value = 'LATEST LOCAL TEXT'; formHandlers.input({target: body});
  return {body, status, calls, flush, navigation,
    edit: (name, value) => {const input = inputs.find(input => input.name === name); input.value = value; formHandlers.input({target: input});},
    holdSave: () => {heldSave = new Promise(resolve => {releaseSave = resolve;});},
    releaseSave: () => {releaseSave(); heldSave = null;},
    setSession: code => {sessionCode = code;}, setAccess: code => {checkCode = code;}, setSave: code => {saveCode = code;},
    submit: async () => {await formHandlers.submit({preventDefault() {}}); await flush();},
    check: async () => {await check.click(); await flush();},
    focus: async () => {handlers.focus(); await flush();},
    save: async () => {const fn = timers.values().next().value; if (fn) await fn(); await flush();}
  };
}
for (const destination of ['/code/a/b/issues/1#issue-comment-2', '/code/a/b/issues/2']) {
  test(`known success GETs canonical result ${destination} without replay`, async () => {
    const h = await harness('saved', 'issue_comment', destination); await h.submit();
    assert.equal(h.calls.filter(([url]) => url.endsWith('/comment/new')).length, 1);
    assert.equal(h.calls.filter(([url]) => url.endsWith('/discard')).length, 1);
    const gets = h.navigation.filter(event => event.kind === 'GET');
    assert.equal(gets.length, 1, 'fragment-only navigation must refresh the canonical document');
    assert.equal(gets[0].url, 'http://host' + destination);
  });
}
for (const field of ['title', 'content', 'source_repository']) {
  test(`unacknowledged ${field} immediately stops claiming Saved`, async () => {
    const h = await harness('unknown', 'pull_new'); await h.save();
    assert.equal(h.status.textContent, 'Saved');
    h.edit(field, 'new unsaved value');
    assert.equal(h.status.textContent, 'Saving');
    assert.equal(h.calls.filter(([url]) => url.endsWith('/save')).length, 1, 'typing must retain debounce');
  });
}
test('old in-flight acknowledgement cannot mark newer input Saved', async () => {
  const h = await harness(); await h.save();
  h.edit('content', 'version two'); h.holdSave();
  const pending = h.save(); await h.flush();
  h.edit('content', 'version three');
  assert.equal(h.status.textContent, 'Saving');
  await h.save();
  assert.equal(h.calls.filter(([url]) => url.endsWith('/save')).length, 2, 'single in-flight save');
  h.releaseSave(); await pending;
  assert.equal(h.status.textContent, 'Saving', 'old acknowledgement is not latest acknowledgement');
  await h.save(); assert.equal(h.status.textContent, 'Saved');
});
for (const [code, status] of [['save_failed', 'Save failed'], ['unauthenticated', 'Expired'], ['recovery_unavailable', 'Unavailable'], ['invalid_payload', 'Invalid']]) {
  test(`new typing preserves ${code} explanation`, async () => {
    const h = await harness(); h.setSave(code); await h.save();
    assert.equal(h.status.textContent, status);
    h.edit('content', 'new text while recovery is uncertain');
    assert.equal(h.status.textContent, status);
  });
}
for (const outcome of ['unknown', 'partial']) {
  for (const revoked of ['account', 'permission']) {
    test(`${outcome} blocks mutation retry but clears on ${revoked} loss`, async () => {
      const h = await harness(outcome); await h.submit();
      assert.equal(h.body.value, 'LATEST LOCAL TEXT');
      await h.check();
      assert.equal(h.body.value, 'LATEST LOCAL TEXT', 'terminal check must never restore');
      if (revoked === 'account') h.setSession('account_changed'); else h.setAccess('forbidden');
      await h.focus();
      assert.equal(h.body.value, ''); assert.equal(h.body.disabled, true);
      await h.submit();
      assert.equal(h.calls.filter(([url]) => url.endsWith('/comment/new')).length, 1);
      if (outcome === 'partial') assert.equal(h.calls.filter(([url]) => url.endsWith('/restore')).length, 0);
      assert.equal(h.calls.filter(([url]) => url.endsWith('/open')).length, 1, 'terminal checks must not reopen recovery');
    });
  }
}
test('operational save failure retains local fields; true permission loss clears', async () => {
  const h = await harness(); h.setSave('save_failed'); await h.save();
  assert.equal(h.body.value, 'LATEST LOCAL TEXT'); assert.equal(h.status.textContent, 'Save failed');
  h.setAccess('forbidden'); await h.check(); assert.equal(h.body.value, '');
});
test('terminal expiry/outage checks retain local text and never unlock mutation', async () => {
  const h = await harness('partial'); await h.submit();
  h.setSession('unauthenticated'); await h.check();
  assert.equal(h.body.value, 'LATEST LOCAL TEXT');
  h.setSession('ok'); h.setAccess('save_failed'); await h.check();
  assert.equal(h.body.value, 'LATEST LOCAL TEXT');
  h.setAccess('ok'); await h.check(); await h.submit();
  assert.equal(h.body.value, 'LATEST LOCAL TEXT');
  assert.equal(h.calls.filter(([url]) => url.endsWith('/comment/new')).length, 1);
});
for (const code of ['ok', 'forbidden', 'save_failed']) {
  test(`new PR validation checks prior source authority (${code}) without restoring`, async () => {
    const h = await harness('validation', 'pull_new'); h.setAccess(code); await h.submit();
    assert.equal(h.body.value, code === 'forbidden' ? '' : 'LATEST LOCAL TEXT');
    assert.equal(h.calls.filter(([url]) => url.endsWith('/restore')).length, 1);
    assert.equal(h.calls.filter(([url]) => url.endsWith('/comment/new')).length, 1);
    assert.equal(h.calls.filter(([url]) => url.endsWith('/open')).length, 1);
  });
}
