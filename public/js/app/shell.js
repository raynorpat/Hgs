(() => {
  'use strict';
  const root = document.querySelector('[data-hg-shell]');
  if (!root || root.dataset.shellReady) return;
  root.dataset.shellReady = 'true';
  let opened = null;
  let inertSiblings = [];
  let previousOverflow = '';
  const backdrop = root.querySelector('[data-drawer-backdrop]');
  const tooltip = root.querySelector('[data-utility-tooltip]');
  let tooltipTrigger = null;

  function hideTooltip() {
    if (!tooltip) return;
    tooltip.hidden = true;
    tooltipTrigger?.removeAttribute('aria-describedby');
    tooltipTrigger = null;
  }
  function showTooltip(target) {
    const control = target.closest('.hg-utility[aria-label]');
    if (!tooltip || !control) return;
    hideTooltip();
    tooltipTrigger = control;
    tooltip.textContent = control.getAttribute('aria-label');
    tooltip.hidden = false;
    tooltip.style.top = Math.max(8, Math.min(control.getBoundingClientRect().top, innerHeight - tooltip.offsetHeight - 8)) + 'px';
    control.setAttribute('aria-describedby', tooltip.id);
  }
  root.addEventListener('focusin', e => showTooltip(e.target));
  root.addEventListener('focusout', hideTooltip);
  root.addEventListener('mouseover', e => showTooltip(e.target));
  root.addEventListener('mouseout', hideTooltip);
  root.querySelectorAll('.hg-utility').forEach(control => control.removeAttribute('title'));

  const focusable = panel => Array.from(panel.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex="0"]'))
    .filter(element => element.getClientRects().length);

  function close(restore = false) {
    if (!opened) return;
    const { panel, trigger, drawer } = opened;
    if (drawer) {
      panel.removeAttribute('data-open');
      panel.removeAttribute('role');
      panel.removeAttribute('aria-modal');
      backdrop.hidden = true;
      inertSiblings.forEach(([element, wasInert]) => {
        if (!wasInert) element.removeAttribute('inert');
      });
      inertSiblings = [];
      document.body.style.overflow = previousOverflow;
    } else {
      panel.hidden = true;
    }
    trigger.setAttribute('aria-expanded', 'false');
    opened = null;
    if (restore) trigger.focus();
  }

  function open(trigger, drawer) {
    const id = drawer ? trigger.dataset.drawer : trigger.dataset.popoutTarget;
    const panel = document.getElementById(id);
    if (!panel || !root.contains(panel)) return;
    if (opened?.panel === panel) {
      close(true);
      return;
    }
    close();
    hideTooltip();
    opened = { panel, trigger, drawer };
    trigger.setAttribute('aria-expanded', 'true');
    if (drawer) {
      panel.dataset.open = 'true';
      panel.setAttribute('role', 'dialog');
      panel.setAttribute('aria-modal', 'true');
      backdrop.hidden = false;
      inertSiblings = Array.from(root.children)
        .filter(element => element !== panel && element !== backdrop)
        .map(element => [element, element.hasAttribute('inert')]);
      inertSiblings.forEach(([element]) => element.setAttribute('inert', ''));
      previousOverflow = document.body.style.overflow;
      document.body.style.overflow = 'hidden';
    } else {
      panel.hidden = false;
      const rect = trigger.getBoundingClientRect();
      panel.style.top = Math.max(8, Math.min(rect.top, innerHeight - panel.offsetHeight - 8)) + 'px';
    }
    (panel.querySelector('[data-popout-focus]') || focusable(panel)[0] || panel).focus();
  }

  root.addEventListener('click', e => {
    const trigger = e.target.closest('[data-popout],[data-drawer]');
    if (trigger) {
      open(trigger, trigger.hasAttribute('data-drawer'));
      return;
    }
    if (e.target.closest('[data-drawer-close]') || e.target === backdrop) {
      close(true);
      return;
    }
    if (opened && !opened.panel.contains(e.target)) close();
  });
  document.addEventListener('keydown', e => {
    if (!opened) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      close(true);
      return;
    }
    if (e.key === 'Tab' && opened.drawer) {
      const controls = focusable(opened.panel);
      if (!controls.length) {
        e.preventDefault();
        opened.panel.focus();
        return;
      }
      const first = controls[0], last = controls[controls.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  });
  root.addEventListener('focusout', () => {
    queueMicrotask(() => {
      if (opened && !opened.drawer && !opened.panel.contains(document.activeElement) && document.activeElement !== opened.trigger) close();
    });
  });
  root.querySelectorAll('[data-avatar]').forEach(img => {
    const fail = () => { img.hidden = true; };
    img.addEventListener('error', fail);
    if (img.complete && !img.naturalWidth) fail();
  });
  matchMedia('(max-width:800px)').addEventListener('change', () => close(true));
})();
