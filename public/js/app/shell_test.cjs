'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
function fixture() {
 const events = {}, timers = new Map(), requests = []; let timerID = 0;
 const input = { value: '' }, results = { innerHTML: '', setAttribute() {}, removeAttribute() {}, replaceChildren() { this.innerHTML = ''; } }, status = {textContent:''};
 const form = { dataset: { searchUrl: '/code/explore/search', searchError: 'Search failed' }, addEventListener(k,fn) {events[k]=fn;}, querySelector(s) {return s==='[name="q"]'?input:s==='[data-search-results]'?results:status;} };
 const root = { dataset: {}, querySelector() {return null;}, querySelectorAll(s) {return s==='[data-repository-search]'?[form]:[];}, addEventListener() {} };
 const sandbox = { document: { querySelector() {return root;}, addEventListener() {} }, matchMedia() {return {addEventListener(){}};}, AbortController, URL, location:{href:'http://fixture.test/code/explore',origin:'http://fixture.test'}, setTimeout(fn,delay){assert.equal(delay,200);timers.set(++timerID,fn);return timerID;},clearTimeout(id){timers.delete(id);}, fetch(url,options) {let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});requests.push({url,options,resolve,reject});return promise;}};
 vm.runInNewContext(fs.readFileSync(__dirname+'/shell.js','utf8'),sandbox);
 return {input,results,status,requests,events,timers, type(value){input.value=value;events.input({target:input});},tick(){for(const [id,fn] of timers){timers.delete(id);fn();}}};
}
const response = html => ({ok:true,redirected:false,headers:{get(){return 'text/html';}},text:async()=>html});
const flush = () => new Promise(setImmediate);
test('search debounces 200ms, encodes subURL query, aborts and ignores out-of-order responses',async()=>{
 const f=fixture(); assert.equal(typeof f.events.input,'function','search input interaction is installed');
 f.type('old');assert.equal(f.requests.length,0);f.tick();assert.equal(f.requests.length,1);
 f.type('new &界');assert.equal(f.requests[0].options.signal.aborted,true);
 // Even a response arriving during the new debounce may not restore the old query.
 f.requests[0].resolve(response('STALE DURING DEBOUNCE'));await flush();assert.equal(f.results.innerHTML,'');
 f.tick();assert.equal(f.requests[1].url.pathname,'/code/explore/search');assert.equal(f.requests[1].url.searchParams.get('q'),'new &界');
 f.type('newest');f.tick();f.requests[2].resolve(response('NEWEST'));await flush();assert.equal(f.results.innerHTML,'NEWEST');
 f.requests[1].resolve(response('STALE LATE'));await flush();assert.equal(f.results.innerHTML,'NEWEST');
 assert.equal(f.requests[2].options.credentials,'same-origin');assert.equal(f.requests[2].options.cache,'no-store');
});
test('submit cancels debounce; stale failures and redirected HTML cannot replace results',async()=>{
 const f=fixture();f.type('one');f.tick();f.type('two');f.events.submit({preventDefault(){}});assert.equal(f.timers.size,0);assert.equal(f.requests.length,2);
 f.requests[1].resolve(response('TWO'));await flush();f.requests[0].reject(new Error('old'));await flush();assert.equal(f.status.textContent,'');assert.equal(f.results.innerHTML,'TWO');
 f.type('three');f.tick();f.requests[2].resolve({...response('LOGIN'),redirected:true});await flush();assert.equal(f.results.innerHTML,'');assert.equal(f.status.textContent,'Search failed');
});

test('null-relatedTarget pointer focusout preserves popout form; outside and Escape still dismiss',async()=>{
 const events={},keys={};let active={};const trigger={dataset:{popoutTarget:'panel'},hasAttribute(){return false;},setAttribute(){},getBoundingClientRect(){return {top:10};},focus(){active=this;}};
 const first={getClientRects(){return [{}];},focus(){active=this;}};
 const panel={hidden:true,style:{},offsetHeight:100,querySelector(){return first;},querySelectorAll(){return [first];},contains(target){return target===first;}};
 const root={dataset:{},contains(){return true;},querySelector(){return null;},querySelectorAll(){return [];},addEventListener(k,fn){events[k]=fn;}};
 const doc={querySelector(){return root;},getElementById(){return panel;},get activeElement(){return active;},addEventListener(k,fn){keys[k]=fn;}};
 vm.runInNewContext(fs.readFileSync(__dirname+'/shell.js','utf8'),{document:doc,queueMicrotask,innerHeight:720,matchMedia(){return {addEventListener(){}};}});
 const open=()=>events.click({target:{closest(selector){return selector==='[data-popout],[data-drawer]'?trigger:null;}}});
 open();assert.equal(panel.hidden,false);active={};events.focusout({relatedTarget:null});await flush();assert.equal(panel.hidden,false,'null focus target during pointer activation must not hide form before click');
 events.pointerdown?.({target:first});events.focusout({relatedTarget:{}});await flush();assert.equal(panel.hidden,false,'pointer interaction in the panel must survive transient outside focus');keys.pointerup?.();
 events.click({target:{closest(){return null;}}});assert.equal(panel.hidden,true,'outside click dismisses');
 open();keys.keydown({key:'Escape',preventDefault(){}});assert.equal(panel.hidden,true);assert.equal(active,trigger,'Escape restores trigger');
});

test('shared avatars retain their image box, try the local fallback once, then reveal initials',()=>{
 const events={};const img={dataset:{avatarFallback:'/code/img/avatar_default.jpg'},complete:false,naturalWidth:0,hidden:false,width:28,height:28,src:'/code/avatars/missing',getAttribute(){return this.src;},addEventListener(name,fn){events[name]=fn;}};
 const root={dataset:{},querySelector(){return null;},querySelectorAll(s){return s==='[data-avatar]'?[img]:[];},addEventListener(){}};
 vm.runInNewContext(fs.readFileSync(__dirname+'/shell.js','utf8'),{document:{querySelector(){return root;},addEventListener(){}},matchMedia(){return {addEventListener(){}};}});
 events.error();assert.equal(img.src,'/code/img/avatar_default.jpg');assert.equal(img.hidden,false);assert.equal(img.width,28);assert.equal(img.height,28);
 events.error();assert.equal(img.hidden,true);assert.equal(img.width,28);assert.equal(img.height,28);
});
