(() => {
  'use strict';
  const root = document.querySelector('[data-hg-shell]');
  if (!root || root.dataset.markdownReady) return;
  root.dataset.markdownReady = 'true';
  const requests = new WeakMap();
  root.addEventListener('click', async e => {
    const button = e.target.closest('[data-markdown-preview],[data-markdown-write]');
    if (!button) return;
    const editor = button.closest('[data-markdown-editor]');
    if (!editor) return;
    const input = editor.querySelector('textarea');
    const preview = editor.querySelector('[data-markdown-output]');
    const status = editor.querySelector('[data-markdown-status]');
    if (!input || !preview) return;
    function selectPreview(selected) {
      input.hidden = selected;
      preview.hidden = !selected;
      editor.querySelector('[data-markdown-write]')?.setAttribute('aria-pressed', String(!selected));
      editor.querySelector('[data-markdown-preview]')?.setAttribute('aria-pressed', String(selected));
    }
    if (button.hasAttribute('data-markdown-write')) {
      requests.get(editor)?.abort();
      selectPreview(false);
      input.focus();
      return;
    }
    requests.get(editor)?.abort();
    const request = new AbortController();
    requests.set(editor, request);
    preview.setAttribute('aria-busy', 'true');
    try {
      const url = new URL(editor.dataset.markdownUrl, location.href);
      if (url.origin !== location.origin) throw new Error('Invalid preview URL');
      const response = await fetch(url, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          'Content-Type': 'application/x-www-form-urlencoded',
          'X-Csrf-Token': document.querySelector('meta[name=_csrf]')?.content || ''
        },
        body: new URLSearchParams({ text: input.value, mode: 'gfm', context: editor.dataset.markdownContext || '' }),
        signal: request.signal
      });
      if (!response.ok) throw new Error('Preview failed');
      const html = await response.text();
      if (requests.get(editor) !== request) return;
      // Only the existing server-sanitized Markdown endpoint supplies preview HTML.
      preview.innerHTML = html;
      selectPreview(true);
      if (status) status.textContent = '';
    } catch (error) {
      if (error.name !== 'AbortError' && status) status.textContent = editor.dataset.previewError;
    } finally {
      if (requests.get(editor) === request) preview.removeAttribute('aria-busy');
    }
  });
})();
