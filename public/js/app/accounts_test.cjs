'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const script=fs.readFileSync(__dirname+'/accounts.js','utf8');
function control(value=''){return {value,disabled:false,required:false,hidden:false,dataset:{},events:{},addEventListener(name,fn){this.events[name]=fn;},classList:{remove(){}}};}
test('installer database selection exposes only applicable fields and preserves entered values',()=>{
 const db=control('SQLite3'),host=control('custom-host'),user=control('dbuser'),name=control('dbname'),path=control('db.sqlite'),ssl=control('verify-full');
 const fields={db_host:host,db_user:user,db_name:name,db_path:path};
 const section=(inputs)=>({hidden:false,classList:{remove(){}},querySelectorAll(){return inputs;}});
 const sections={'.server-sql':section([host,user,name]),'.pgsql-setting':section([ssl]),'.sqlite-setting':section([path])};
 const root={querySelector(s){return s==='#install-database'?db:fields[/name="([^"]+)"/.exec(s)?.[1]]||null;},querySelectorAll(s){return sections[s]?[sections[s]]:[];}};
 vm.runInNewContext(script,{document:{querySelector(){return root;}}});
 assert.equal(path.disabled,false);assert.equal(path.required,true);assert.equal(host.disabled,true);assert.equal(ssl.disabled,true);
 db.value='PostgreSQL';db.events.change();assert.equal(path.disabled,true);assert.equal(host.required,true);assert.equal(ssl.disabled,false);assert.equal(ssl.value,'verify-full');
 db.value='MySQL';db.events.change();assert.equal(ssl.disabled,true);assert.equal(host.value,'custom-host');assert.equal(path.value,'db.sqlite');
});
test('team repository search uses real full_name DTO with subURL/owner scope',async()=>{
 const input=control('sample');input.dataset={accountSearch:'/code/api/v1/repos/search',owner:'7'};input.getAttribute=()=> 'results';
 const list={options:[],replaceChildren(...options){this.options=options;}};let timer,request;
 const root={querySelector(){return null;},querySelectorAll(s){return s==='[data-account-search]'?[input]:[];}};
 vm.runInNewContext(script,{document:{querySelector(){return root;},getElementById(){return list;},createElement(){return {};}},location:{href:'http://fixture.invalid/code/org/lab/teams/readers'},URL,AbortController,setTimeout(fn){timer=fn;},clearTimeout(){},fetch:async(url)=>{request=url;return {ok:true,redirected:false,json:async()=>({ok:true,data:[{full_name:'lab/sample'}]})};}});
 input.events.input();await timer();assert.equal(request.pathname,'/code/api/v1/repos/search');assert.equal(request.searchParams.get('uid'),'7');assert.equal(list.options[0].value,'lab/sample');
});
test('cancelled account confirmation permits retry and duplicate submission is blocked',()=>{
 const handlers=[],global={},button={setAttribute(){},removeAttribute(){}};let confirmed=false;
 const form={dataset:{confirmMessage:'Delete?'},elements:{},closest(selector){return selector==='.hg-account-page'?{}:this;},setAttribute(){},removeAttribute(){},querySelectorAll(){return [button];}};
 const root={dataset:{},addEventListener(name,fn,capture){if(name==='submit')handlers.push({fn,capture});},querySelectorAll(){return [form];}};
 vm.runInNewContext(fs.readFileSync(__dirname+'/forms.js','utf8'),{document:{querySelector(){return root;}},window:{confirm(){return confirmed;}},addEventListener(name,fn){global[name]=fn;}});
 const submit=()=>{const event={target:form,submitter:{dataset:{}},preventDefault(){this.prevented=true;},stopImmediatePropagation(){this.stopped=true;}};for(const h of handlers){h.fn(event);if(event.stopped)break;}return event;};
 assert.equal(submit().prevented,true);assert.equal(form.dataset.submitting,undefined);confirmed=true;assert.equal(submit().prevented,undefined);assert.equal(form.dataset.submitting,'true');assert.equal(submit().prevented,true);global.pageshow();assert.equal(form.dataset.submitting,undefined);
});
