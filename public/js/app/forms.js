(() => {
  'use strict';
  const root = document.querySelector('[data-hg-shell]');
  if (!root || root.dataset.formsReady) return;
  root.dataset.formsReady = 'true';
  // Opt in only on forms whose native validation and response lifecycle support this.
  root.addEventListener('submit', e => {
    const form = e.target.closest('form[data-prevent-duplicate]');
    if (!form) return;
    if (form.dataset.submitting) {
      e.preventDefault();
      return;
    }
    form.dataset.submitting = 'true';
    form.setAttribute('aria-busy', 'true');
    // Preserve the submitter name/value for the browser's native form submission.
    form.querySelectorAll('[type=submit]').forEach(button => button.setAttribute('aria-disabled', 'true'));
  });
  addEventListener('pageshow', () => {
    root.querySelectorAll('form[data-prevent-duplicate]').forEach(form => {
      delete form.dataset.submitting;
      form.removeAttribute('aria-busy');
      form.querySelectorAll('[type=submit]').forEach(button => button.removeAttribute('aria-disabled'));
    });
  });
})();
