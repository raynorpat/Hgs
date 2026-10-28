// Behavioral controller unit tests with a deliberately small DOM/transport double.
// These do not prove actual browser timing, cookies, navigation, or bfcache.
const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../public/js/app/drafts.js'), 'utf8');
const attachmentsSource = fs.readFileSync(path.join(__dirname, '../public/js/app/issues.js'), 'utf8');

async function harness(outcome = 'unknown', kind = 'issue_comment', destination = '/code/a/b/issues/1#issue-comment-2', options = {}) {
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
  const assignee = {name: 'assigneeid', tagName: 'INPUT', defaultValue: '', value: '', setCustomValidity() {}, disabled: false};
  const milestone = {name: 'milestoneid', tagName: 'INPUT', defaultValue: '', value: '', setCustomValidity() {}, disabled: false};
  const inputs = kind === 'pull_new' ? [body, title, selectedSource] : kind === 'issue_new' ? [body, title, assignee, milestone] : [body];
  const csrfMeta = {content: 'csrf'}, csrfInput = {name: '_csrf', value: 'csrf'};
  const fileInput = {
    name: 'attachments', type: 'file', tagName: 'INPUT', files: [], dataset: {removeLabel: 'Remove attachment'},
    get value() { return this.files.length ? 'C:\\fakepath\\' + this.files[0].name : ''; },
    set value(value) { assert.equal(value, '', 'file selection can only be cleared programmatically'); this.files = []; },
    addEventListener(event, fn) { this[event] = fn; }
  };
  const node = tagName => ({tagName, children: [], append(...children) {this.children.push(...children);},
    replaceChildren(...children) {this.children = children;}, addEventListener(event, fn) {this[event] = fn;}});
  const attachmentList = node('UL');
  const allInputs = [...inputs, csrfInput, ...(options.attachments ? [fileInput] : [])];
  const status = {textContent: ''}, auth = {hidden: true}, result = {hidden: true};
  const check = {}, cancel = {};
  for (const button of [check, cancel]) button.addEventListener = (event, fn) => { button[event] = fn; };
  const controls = {
    dataset: {draftApi: '/code/user/drafts', draftActor: '1', draftRepository: '2', draftSessionEstablished: 'true',
      saving: 'Saving', failed: 'Save failed', cleared: 'Cleared', unknown: 'Unknown', partial: 'Partial', expired: 'Expired', saved: 'Saved', invalid: 'Invalid', unavailable: 'Unavailable',
      cancelled: 'Cancelled', reselect: 'Localized attachment reselection instruction'},
    querySelector: q => ({'[data-draft-status]': status, '[data-draft-auth]': auth, '[data-draft-result]': result,
      '[data-draft-check]': check, '[data-draft-cancel]': cancel})[q]
  };
  const form = {
    dataset: {editorDraft: kind, draftItem: kind === 'pull_new' ? '0' : '3', draftDestination: '/code/a/b/issues/1'},
    action: 'http://host/code/a/b/comment/new',
    elements: {namedItem: name => allInputs.find(input => input.name === name)},
    querySelector: () => controls,
    querySelectorAll: q => q === 'input,textarea,select' ? allInputs : q === 'input[type=file]' && options.attachments ? [fileInput] : [],
    addEventListener: (event, fn) => { formHandlers[event] = fn; },
    setAttribute() {}, removeAttribute() {}, dispatchEvent(event) {event.target ||= form; formHandlers[event.type]?.(event);}, reportValidity: () => true
  };
  const attachmentGroup = {querySelector: q => q === 'input[type=file]' ? fileInput : attachmentList, closest: () => form};
  const shell = {dataset: {}, querySelectorAll: q => q === 'form[data-editor-draft]' ? [form] : q === '[data-issue-attachments]' && options.attachments ? [attachmentGroup] : []};
  const response = value => ({headers: {get: () => 'application/json'}, json: async () => value});
  const fetch = async (url, requestOptions) => {
    calls.push([url, requestOptions]);
    if (url === form.action) {
      if (outcome === 'unknown') throw Error('lost response');
      if (['validation', 'unauthenticated', 'csrf_invalid', 'save_failed'].includes(outcome)) return response({code: outcome});
      if (outcome === 'saved') return response({code: 'saved', committed: true, destination});
      return response({code: 'partial', committed: true, unsaved: true, destination: '/code/a/b/issues/1'});
    }
    if (url.endsWith('/session')) return response({code: sessionCode, actorID: 1, csrf: 'fresh', collectionPresent: true});
    if (url.endsWith('/open')) return response({code: 'ok', grant: {PageID: 'page', PageSecret: 'secret', DisposalToken: 'dispose'}});
    if (url.endsWith('/check')) return response({code: checkCode});
    if (url.endsWith('/restore')) return response({code: checkCode, snapshot: {Version: options.restoreVersion ?? 1, Payload: {Body: 'OLD SERVER TEXT'}}});
    if (url.endsWith('/save')) { if (heldSave) await heldSave; return response({code: saveCode, snapshot: {Version: 1}}); }
    if (url.endsWith('/discard')) return response({code: 'ok'});
    throw Error('unexpected request');
  };
  const context = vm.createContext({
    document: {querySelector: q => q === '[data-hg-shell]' ? shell : csrfMeta, querySelectorAll: () => [csrfInput],
      createElement: node, createTextNode: textContent => ({textContent})},
    navigator: {}, location,
    fetch, URL, URLSearchParams, TextEncoder, CustomEvent: class {constructor(type, init) {this.type = type; Object.assign(this, init);}},
    DataTransfer: options.dataTransfer === false ? undefined : class {
      files = []; items = {add: file => {this.files.push(file);}};
    },
    FormData: class extends FormData {
      constructor() {
        super();
        for (const input of allInputs.filter(input => !input.disabled)) {
          if (input.type === 'file') for (const file of input.files.length ? input.files : [new File([], '')]) this.append(input.name, file);
          else this.append(input.name, input.value);
        }
      }
    },
    addEventListener: (event, fn) => { handlers[event] = fn; },
    setTimeout: fn => { timers.set(++timer, fn); return timer; }, clearTimeout: id => timers.delete(id)
  });
  vm.runInContext(source, context);
  if (options.attachments) vm.runInContext(attachmentsSource, context);
  const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
  await flush();
  if (!options.untouched) {body.value = 'LATEST LOCAL TEXT'; formHandlers.input({target: body});}
  return {body, title, assignee, milestone, status, calls, flush, navigation, fileInput,
    attachmentNames: () => attachmentList.children.map(row => row.children[0].textContent.trim()),
    selectFiles: (...files) => {fileInput.files = files; fileInput.change?.(); form.dispatchEvent({type: 'change', target: fileInput});},
    removeAttachment: index => attachmentList.children[index].children[1].click(),
    edit: (name, value) => {const input = inputs.find(input => input.name === name); input.value = value; formHandlers.input({target: input});},
    holdSave: () => {heldSave = new Promise(resolve => {releaseSave = resolve;});},
    releaseSave: () => {releaseSave(); heldSave = null;},
    setSession: code => {sessionCode = code;}, setAccess: code => {checkCode = code;}, setSave: code => {saveCode = code;},
    setOutcome: code => {outcome = code;},
    submit: async submitter => {await formHandlers.submit({preventDefault() {}, submitter}); await flush();},
    check: async () => {await check.click(); await flush();},
    cancel: async () => {cancel.click(); await flush();},
    focus: async () => {handlers.focus(); await flush();},
    save: async () => {const fn = timers.values().next().value; if (fn) await fn(); await flush();}
  };
}
for (const trigger of ['autosave', 'canonical submit']) {
  for (const code of ['unauthenticated', 'csrf_invalid']) {
    test(`attachment selection clears on ${trigger} ${code} and requires recovery reselection`, async () => {
      const h = await harness(code, 'issue_new', undefined, {attachments: true});
      h.edit('title', 'Retained title'); h.edit('assigneeid', '7'); h.edit('milestoneid', '9');
      h.selectFiles(new File(['old binary'], 'before-expiry.png', {type: 'image/png'}));
      assert.deepEqual(h.attachmentNames(), ['before-expiry.png']);
      await h.save();
      const firstSave = JSON.parse(h.calls.find(([url]) => url.endsWith('/save'))[1].body);
      assert.deepEqual(firstSave.Payload, {Title: 'Retained title', Body: 'LATEST LOCAL TEXT', Metadata: {assigneeid: '7', milestoneid: '9'}});
      h.edit('content', 'NEWER LOCAL TEXT');
      if (trigger === 'autosave') {h.setSave(code); await h.save();} else await h.submit();
      assert.equal(h.fileInput.value, '', 'expiry must clear the native file input');
      assert.deepEqual(h.attachmentNames(), [], 'expiry must clear the rendered attachment list');
      assert.equal(h.status.textContent, 'Expired Localized attachment reselection instruction');
      assert.equal(h.title.value, 'Retained title'); assert.equal(h.body.value, 'NEWER LOCAL TEXT');
      assert.equal(h.assignee.value, '7'); assert.equal(h.milestone.value, '9');
      const posts = h.calls.filter(([url]) => url.endsWith('/comment/new')).length;
      h.selectFiles(new File(['during recovery'], 'during-recovery.png'));
      await h.submit();
      assert.equal(h.calls.filter(([url]) => url.endsWith('/comment/new')).length, posts, 'expiry still blocks canonical submit');
      h.setSave('ok'); h.setOutcome('saved'); await h.check();
      assert.equal(h.fileInput.value, '', 'recovery must clear selections made while sign-in was blocked');
      assert.deepEqual(h.attachmentNames(), []);
      assert.equal(h.calls.filter(([url]) => url.endsWith('/comment/new')).length, posts, 'Check must not submit');
      assert.equal(h.calls.filter(([url]) => url.endsWith('/open')).length, 1, 'recovery must keep this page grant');
      assert.equal(h.calls.filter(([url]) => url.endsWith('/discard')).length, 0, 'recovery must not dispose the page');
      assert.equal(h.body.value, 'NEWER LOCAL TEXT', 'new local text must win over the server snapshot');
      await h.save();
      assert.equal(h.status.textContent, 'Saved Localized attachment reselection instruction', 'saving text does not reselect files');
      const lastSave = JSON.parse(h.calls.filter(([url]) => url.endsWith('/save')).at(-1)[1].body);
      assert.equal(lastSave.Version, 1); assert.equal(lastSave.PageSecret, 'secret');
      assert.deepEqual(lastSave.Payload, {Title: 'Retained title', Body: 'NEWER LOCAL TEXT', Metadata: {assigneeid: '7', milestoneid: '9'}});
      h.selectFiles(new File(['new binary'], 'reselected.png', {type: 'image/png'}));
      assert.deepEqual(h.attachmentNames(), ['reselected.png'], 'the private files list must not resurrect earlier selections');
      assert.equal(h.status.textContent, 'Saved');
      await h.submit({name: 'change_status', value: 'close'});
      const sent = h.calls.filter(([url]) => url.endsWith('/comment/new')).at(-1)[1];
      assert.ok(sent.body instanceof FormData);
      assert.deepEqual(sent.body.getAll('attachments').map(file => file.name), ['reselected.png']);
      assert.equal(await sent.body.get('attachments').text(), 'new binary');
      assert.equal(sent.body.get('content'), 'NEWER LOCAL TEXT');
      assert.equal(sent.body.get('title'), 'Retained title');
      assert.equal(sent.body.get('assigneeid'), '7'); assert.equal(sent.body.get('milestoneid'), '9');
      assert.equal(sent.body.get('_csrf'), 'fresh'); assert.equal(sent.body.get('change_status'), 'close');
      assert.equal(sent.headers['Content-Type'], undefined, 'native multipart must choose its own boundary');
    });
  }
}
test('attachment cannot upload after same-account Check without reselection', async () => {
  const h = await harness('unauthenticated', 'issue_comment', undefined, {attachments: true});
  h.selectFiles(new File(['old binary'], 'before-expiry.png'));
  await h.submit(); h.setOutcome('saved'); await h.check(); await h.submit();
  const posts = h.calls.filter(([url]) => url.endsWith('/comment/new'));
  assert.equal(posts.length, 2, 'only the two explicit submits may mutate');
  assert.deepEqual(posts[1][1].body.getAll('attachments').map(file => file.name).filter(Boolean), []);
  assert.equal(posts[1][1].body.get('content'), 'LATEST LOCAL TEXT');
});
test('attachment expiry clears the native input without DataTransfer support', async () => {
  const h = await harness('unauthenticated', 'issue_comment', undefined, {attachments: true, dataTransfer: false});
  h.selectFiles(new File(['old binary'], 'before-expiry.png'));
  await h.submit();
  assert.equal(h.fileInput.value, '');
  assert.equal(h.status.textContent, 'Expired Localized attachment reselection instruction');
});
test('attachment reselection instruction survives recovery of an untouched version-zero editor', async () => {
  const h = await harness('saved', 'issue_comment', undefined, {attachments: true, untouched: true, restoreVersion: 0});
  h.selectFiles(new File(['old binary'], 'before-expiry.png'));
  h.setSession('unauthenticated'); await h.focus();
  h.setSession('ok'); await h.check();
  assert.equal(h.body.value, ''); assert.equal(h.fileInput.value, '');
  assert.equal(h.status.textContent, 'Localized attachment reselection instruction');
  h.selectFiles(new File(['new binary'], 'reselected.png'));
  assert.equal(h.status.textContent, '', 'reselection must not resurrect an expired or failed status');
  assert.equal(h.calls.filter(([url]) => url.endsWith('/comment/new')).length, 0);
});
test('attachment Cancel clears native and private selections without disturbing later multipart', async () => {
  const h = await harness('saved', 'issue_comment', undefined, {attachments: true});
  h.selectFiles(new File(['old binary'], 'cancelled.png')); await h.cancel();
  assert.equal(h.body.value, ''); assert.equal(h.fileInput.value, ''); assert.deepEqual(h.attachmentNames(), []);
  assert.equal(h.status.textContent, 'Cancelled');
  assert.equal(h.calls.filter(([url]) => url.endsWith('/discard')).length, 1);
  h.edit('content', 'AFTER CANCEL'); h.selectFiles(new File(['new binary'], 'after-cancel.png'));
  assert.deepEqual(h.attachmentNames(), ['after-cancel.png']);
  await h.submit();
  const sent = h.calls.find(([url]) => url.endsWith('/comment/new'))[1].body;
  assert.deepEqual(sent.getAll('attachments').map(file => file.name), ['after-cancel.png']);
  assert.equal(sent.get('content'), 'AFTER CANCEL');
});
test('attachment ordinary session Check retains selection and native multipart submitter', async () => {
  const h = await harness('saved', 'issue_comment', undefined, {attachments: true});
  h.selectFiles(new File(['first binary'], 'first.png')); h.selectFiles(new File(['second binary'], 'second.png'));
  assert.deepEqual(h.attachmentNames(), ['first.png', 'second.png']);
  h.removeAttachment(0); await h.check();
  assert.deepEqual(h.attachmentNames(), ['second.png']);
  assert.equal(h.fileInput.files.length, 1);
  assert.equal(h.status.textContent.includes('Localized attachment reselection instruction'), false);
  await h.submit({name: 'change_status', value: 'close'});
  const sent = h.calls.find(([url]) => url.endsWith('/comment/new'))[1];
  assert.ok(sent.body instanceof FormData);
  assert.deepEqual(sent.body.getAll('attachments').map(file => file.name), ['second.png']);
  assert.equal(await sent.body.get('attachments').text(), 'second binary');
  assert.equal(sent.body.get('change_status'), 'close');
  assert.equal(sent.headers['X-Hgs-Editor-Submit'], '1'); assert.equal(sent.headers['X-Hgs-Editor-Account'], '1');
  assert.equal(sent.headers['Content-Type'], undefined);
});
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
