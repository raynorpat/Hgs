(() => {
 'use strict';
 const root=document.querySelector('[data-hg-shell]');if(!root||root.dataset.issuesReady)return;root.dataset.issuesReady='true';
 // Keep arbitrary supported label colors legible, without trusting CSS strings.
 root.querySelectorAll('[data-label-color]').forEach(label=>{
  const hex=label.dataset.labelColor;if(!/^#[0-9a-f]{6}$/i.test(hex))return;
  const rgb=[1,3,5].map(i=>parseInt(hex.slice(i,i+2),16)/255).map(c=>c<=0.04045?c/12.92:((c+0.055)/1.055)**2.4);
  const l=rgb[0]*0.2126+rgb[1]*0.7152+rgb[2]*0.0722;
  label.style.backgroundColor=hex;label.style.color=l>0.179?'#000000':'#ffffff';
 });
 // Preserve multiple file selections and allow removal before native multipart submission.
 root.querySelectorAll('[data-issue-attachments]').forEach(group=>{
  const input=group.querySelector('input[type=file]'),list=group.querySelector('[data-attachment-list]');
  if(!input||!list||typeof DataTransfer==='undefined')return;
  let files=[];
  group.closest('form').addEventListener('hg:draft-clear',()=>{files=[];sync();render()});
  function render(){list.replaceChildren();files.forEach((file,index)=>{const li=document.createElement('li'),button=document.createElement('button');li.append(document.createTextNode(file.name+' '));button.type='button';button.className='hg-button';button.textContent=input.dataset.removeLabel;button.addEventListener('click',()=>{files.splice(index,1);sync();render()});li.append(button);list.append(li)})}
  function sync(){const dt=new DataTransfer();files.forEach(file=>dt.items.add(file));input.files=dt.files}
  input.addEventListener('change',()=>{const incoming=Array.from(input.files);files.push(...incoming);sync();render()});
 });
})();
