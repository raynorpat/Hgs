(() => {
  'use strict';
  const root = document.querySelector('[data-hg-shell]');
  if (!root) return;

  const show = (selector, visible) => root.querySelectorAll(selector).forEach(section => {
    section.hidden = !visible;
    section.classList.remove('hide', 'hidden');
    section.querySelectorAll('input,select').forEach(input => { input.disabled = !visible; });
  });
  const db = root.querySelector('#install-database');
  if (db) {
    const update = () => {
      show('.server-sql', db.value !== 'SQLite3');
      show('.pgsql-setting', db.value === 'PostgreSQL');
      show('.sqlite-setting', db.value === 'SQLite3');
      ['db_host', 'db_user', 'db_name', 'db_path'].forEach(name => {
        const input = root.querySelector('[name="' + name + '"]');
        input.required = !input.disabled;
      });
    };
    db.addEventListener('change', update);
    update();
  }
  const auth = root.querySelector('#auth-type');
  if (auth) {
    const update = () => {
      show('.ldap', auth.value === '2');
      show('.smtp', auth.value === '3');
    };
    auth.addEventListener('change', update);
    update();
  }
  const login = root.querySelector('#login-type');
  if (login) {
    const update = () => {
      const local = login.value === '0-0';
      show('.auth-name', !local);
      // New-user binding retains the existing initial-password requirement.
      if (!root.querySelector('[data-account-create]')) show('.pwd', local);
    };
    login.addEventListener('change', update);
    update();
  }

  root.querySelectorAll('[data-account-search]').forEach(input => {
    const list = document.getElementById(input.getAttribute('list'));
    if (!list) return;
    let timer, controller, sequence = 0;
    input.addEventListener('input', () => {
      clearTimeout(timer);
      controller?.abort();
      const current = ++sequence;
      list.replaceChildren();
      if (!input.value.trim()) return;
      timer = setTimeout(async () => {
        controller = new AbortController();
        try {
          const url = new URL(input.dataset.accountSearch, location.href);
          url.searchParams.set('q', input.value);
          url.searchParams.set('limit', '10');
          if (input.dataset.owner) url.searchParams.set('uid', input.dataset.owner);
          const response = await fetch(url, { credentials: 'same-origin', cache: 'no-store', signal: controller.signal });
          if (!response.ok || response.redirected) return;
          const result = await response.json();
          if (current !== sequence || !result.ok) return;
          list.replaceChildren(...result.data.map(item => {
            const option = document.createElement('option');
            option.value = item.username || item.name || item.full_name;
            option.textContent = item.full_name || option.value;
            return option;
          }));
        } catch (_) { /* Exact-name entry remains functional when search fails. */ }
      }, 200);
    });
  });
})();
