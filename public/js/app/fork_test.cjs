'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const flush=()=>new Promise(setImmediate);
function fixture(status) {
 const message={textContent:'Preparing'};const events={};const requests=[];const timers=[];const redirects=[];
 const panel={dataset:{statusUrl:'/code/repo/forks/4/status',unavailable:'Unavailable',pending:'Still provisioning'},querySelector(){return message;}};
 const location={href:'https://fixture.test/code/repo/forks/4',origin:'https://fixture.test',replace(url){redirects.push(url);}};
 vm.runInNewContext(fs.readFileSync(__dirname+'/fork.js','utf8'),{document:{querySelector(){return panel;}},window:{location,addEventListener(name,fn){events[name]=fn;}},URL,AbortController,fetch:async(url,options)=>{requests.push({url,options});return {ok:true,json:async()=>status};},setTimeout(fn,delay){timers.push({fn,delay});return timers.length;},clearTimeout(){timers.length=0;}});
 return {message,events,requests,timers,redirects};
}
test('provisioning polls only current status with bounded backoff then offers manual GET refresh',async()=>{
 const f=fixture({state:'provisioning'});await flush();
 while(f.timers.length){const next=f.timers.shift();assert.ok(next.delay>=1000&&next.delay<=8000);next.fn();await flush();}
 assert.equal(f.requests.length,30);assert.equal(f.message.textContent,'Still provisioning');
 for(const r of f.requests){assert.equal(r.url,'/code/repo/forks/4/status');assert.equal(r.options.mode,'same-origin');assert.equal(r.options.cache,'no-store');assert.equal(r.options.credentials,'same-origin');assert.equal(r.options.method,undefined);}
});
test('ready status preserves subURL; untrusted external destinations never navigate',async()=>{
 const f=fixture({state:'ready',url:'/code/user/fork'});await flush();assert.deepEqual(f.redirects,['https://fixture.test/code/user/fork']);
 const bad=fixture({state:'ready',url:'https://external.invalid/fork'});await flush();assert.deepEqual(bad.redirects,[]);assert.equal(bad.message.textContent,'Unavailable');
});
test('navigation stops polling and aborts its request without creating another fork',async()=>{
 const f=fixture({state:'provisioning'});await flush();f.events.pagehide();assert.equal(f.timers.length,0);assert.equal(f.requests[0].options.signal.aborted,true);assert.equal(f.requests.length,1);
});
