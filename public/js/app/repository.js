(() => {
  'use strict';
  const root = document.querySelector('[data-hg-shell]');
  if (!root || root.dataset.repositoryReady) return;
  root.dataset.repositoryReady = 'true';
  const disclosures = [...root.querySelectorAll('.hg-clone,.hg-revision-selector')];
  disclosures.forEach(item => item.addEventListener('toggle', () => {
    if (item.open) disclosures.forEach(other => { if (other !== item) other.open = false; });
  }));
  root.addEventListener('keydown', e => {
    if (e.key === 'Escape') disclosures.forEach(item => {
      if (item.open) { item.open = false; item.querySelector('summary').focus(); }
    });
  });
  document.addEventListener('click', e => disclosures.forEach(item => {
    if (item.open && !item.contains(e.target)) item.open = false;
  }));
  root.querySelectorAll('[data-repository-controls]').forEach(controls => {
    const value = controls.querySelector('[data-clone-value]');
    const status = controls.querySelector('[data-clone-status]');
    value.addEventListener('click', () => value.select());
    controls.querySelectorAll('[data-clone-protocol]').forEach(button => button.addEventListener('click', () => {
      value.value = 'hg clone ' + button.dataset.cloneUrl;
      controls.querySelectorAll('[data-clone-protocol]').forEach(other => other.setAttribute('aria-pressed', String(other === button)));
      status.textContent = '';
    }));
    controls.querySelector('[data-clone-copy]').addEventListener('click', async e => {
      const button = e.currentTarget;
      let copied = false;
      try { if (navigator.clipboard) { await navigator.clipboard.writeText(value.value); copied = true; } } catch (_) { /* fallback below */ }
      if (!copied) { value.focus(); value.select(); try { copied = document.execCommand('copy'); } catch (_) { /* visible failure below */ } }
      status.textContent = copied ? button.dataset.copied : button.dataset.copyFailed;
    });
  });
  // Capture precedes shared duplicate-submit handling; cancellation never locks the form.
  root.addEventListener('submit', e => {
    const form = e.target.closest('form[data-confirm-message]');
    if (!form) return;
    const renamed = !form.hasAttribute('data-rename-form') || form.elements.repo_name.value !== form.dataset.originalName;
    if (renamed && !window.confirm(form.dataset.confirmMessage)) { e.preventDefault(); e.stopImmediatePropagation(); }
  }, true);
  root.querySelectorAll('[data-collaborator-search]').forEach(input => {
    const list = document.getElementById(input.getAttribute('list'));
    let timer, controller, sequence = 0;
    input.addEventListener('input', () => {
      clearTimeout(timer); if (controller) controller.abort(); const current = ++sequence;
      list.replaceChildren(); if (!input.value.trim()) return;
      timer = setTimeout(async () => {
        controller = new AbortController();
        try {
          const response = await fetch(input.dataset.collaboratorSearch + '?q=' + encodeURIComponent(input.value) + '&limit=10', { credentials: 'same-origin', cache: 'no-store', signal: controller.signal });
          if (!response.ok || response.redirected) return;
          const result = await response.json(); if (current !== sequence || !result.ok) return;
          list.replaceChildren(...result.data.map(user => { const option = document.createElement('option'); option.value = user.username; option.textContent = user.full_name || user.username; return option; }));
        } catch (_) { /* Plain username entry remains usable if search fails. */ }
      }, 200);
    });
  });
  const source = root.querySelector('[data-source-code]');
  if (source && window.prettyPrint) window.prettyPrint(() => {
    source.querySelectorAll('ol.linenums > li').forEach((line, i) => {
      line.id = 'L' + (i + 1);
      const anchor = document.createElement('a'); anchor.className = 'hg-line-number'; anchor.href = '#L' + (i + 1); anchor.textContent = String(i + 1);
      line.prepend(anchor);
      anchor.addEventListener('click', e => {
        if (e.shiftKey) { const first = /^#L(\d+)/.exec(location.hash); if (first) { e.preventDefault(); location.hash = 'L' + Math.min(Number(first[1]), i + 1) + '-L' + Math.max(Number(first[1]), i + 1); } }
      });
    });
    const selectLines = () => {
      const match = /^#L(\d+)(?:-L?(\d+))?$/.exec(location.hash);
      source.querySelectorAll('ol.linenums > li').forEach((line, i) => line.classList.toggle('hg-selected-line', !!match && i + 1 >= Number(match[1]) && i + 1 <= Number(match[2] || match[1])));
    };
    addEventListener('hashchange', selectLines); selectLines();
    if (location.hash) { const target = document.getElementById(location.hash.slice(1).split('-')[0]); if (target) target.scrollIntoView({ block: 'center' }); }
  }, source.parentElement);
  root.querySelectorAll('[data-diff]').forEach(pre => {
    const lines = pre.textContent.split('\n'); pre.textContent = '';
    lines.forEach((text, i) => {
      const line = document.createElement('span'); line.className = 'hg-diff-line'; line.id = 'diff-L' + (i + 1);
      if (text.startsWith('+') && !text.startsWith('+++')) line.classList.add('hg-diff-add');
      else if (text.startsWith('-') && !text.startsWith('---')) line.classList.add('hg-diff-remove');
      else if (text.startsWith('@@') || text.startsWith('diff ')) line.classList.add('hg-diff-hunk');
      const anchor = document.createElement('a'); anchor.href = '#diff-L' + (i + 1); anchor.className = 'hg-line-number'; anchor.textContent = String(i + 1);
      line.append(anchor, document.createTextNode(text + '\n')); pre.append(line);
    });
  });
})();
