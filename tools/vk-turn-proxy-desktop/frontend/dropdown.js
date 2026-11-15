export function initializeDropdowns(root) {
  const controls = [...root.querySelectorAll('select')].map(createDropdown);
  return () => controls.forEach((refresh) => refresh());
}

function createDropdown(source) {
  const wrapper = document.createElement('div');
  wrapper.className = 'dropdown';
  const trigger = document.createElement('button');
  trigger.type = 'button';
  trigger.id = `${source.id}-button`;
  trigger.className = 'dropdown-trigger';
  trigger.setAttribute('role', 'combobox');
  trigger.setAttribute('aria-haspopup', 'listbox');
  trigger.setAttribute('aria-expanded', 'false');
  const value = document.createElement('span');
  value.className = 'dropdown-value';
  trigger.append(value);

  const popup = document.createElement('div');
  popup.id = `${source.id}-list`;
  popup.className = 'dropdown-menu';
  popup.setAttribute('role', 'listbox');
  popup.hidden = true;
  if (typeof popup.showPopover === 'function') popup.popover = 'manual';
  trigger.setAttribute('aria-controls', popup.id);
  if (source.hasAttribute('aria-describedby'))
    trigger.setAttribute('aria-describedby', source.getAttribute('aria-describedby'));
  const labels = [...source.labels];
  labels.forEach((label, index) => {
    label.id ||= `${source.id}-label-${index}`;
    label.addEventListener('click', (event) => {
      event.preventDefault();
      trigger.focus();
    });
  });
  const labelIDs = labels.map((label) => label.id).join(' ');
  trigger.setAttribute('aria-labelledby', labelIDs);
  popup.setAttribute('aria-labelledby', labelIDs);
  source.before(wrapper);
  wrapper.append(source, trigger, popup);
  source.hidden = true;

  let open = false;
  let options = [];
  let signature = '';
  let active = -1;
  let search = '';
  let searchTimer;

  function highlight(index, scroll = true) {
    active = index;
    [...popup.children].forEach((item, i) => {
      item.classList.toggle('active', i === active);
      item.setAttribute('aria-selected', String(i === source.selectedIndex));
    });
    if (open && active >= 0) {
      trigger.setAttribute('aria-activedescendant', popup.children[active].id);
      if (scroll) popup.children[active].scrollIntoView({ block: 'nearest' });
    }
  }

  function position() {
    if (!open) return;
    const bounds = trigger.getBoundingClientRect();
    const below = innerHeight - bounds.bottom - 12;
    const above = bounds.top - 12;
    const upward = below < 240 && above > below;
    popup.style.width = `${bounds.width}px`;
    popup.style.maxHeight = `${Math.max(40, Math.min(240, (upward ? above : below) - 6))}px`;
    popup.style.left = `${Math.max(12, Math.min(bounds.left, innerWidth - bounds.width - 12))}px`;
    popup.style.top = `${upward ? bounds.top - popup.getBoundingClientRect().height - 6 : bounds.bottom + 6}px`;
  }

  function close() {
    if (!open) return;
    open = false;
    if (popup.popover) popup.hidePopover();
    popup.hidden = true;
    trigger.setAttribute('aria-expanded', 'false');
    trigger.removeAttribute('aria-activedescendant');
    search = '';
    clearTimeout(searchTimer);
  }

  function refresh() {
    options = [...source.options];
    const nextSignature = JSON.stringify(
      options.map((option) => [option.value, option.label, option.disabled]),
    );
    if (nextSignature !== signature) {
      signature = nextSignature;
      popup.replaceChildren(
        ...options.map((option, index) => {
          const item = document.createElement('div');
          item.id = `${source.id}-option-${index}`;
          item.className = 'dropdown-option';
          item.setAttribute('role', 'option');
          item.setAttribute('aria-disabled', String(option.disabled));
          item.dataset.index = index;
          item.textContent = option.label;
          return item;
        }),
      );
      active = source.selectedIndex;
    }
    const label = options[source.selectedIndex]?.label || '';
    if (value.textContent !== label) value.textContent = label;
    trigger.disabled = source.disabled || !options.some((option) => !option.disabled);
    if (trigger.disabled) close();
    highlight(active, false);
    position();
  }

  function show() {
    refresh();
    if (trigger.disabled) return;
    popup.hidden = false;
    if (popup.popover) popup.showPopover();
    open = true;
    trigger.setAttribute('aria-expanded', 'true');
    position();
    highlight(
      source.selectedIndex >= 0 ? source.selectedIndex : options.findIndex((option) => !option.disabled),
    );
  }

  function commit() {
    if (active < 0 || options[active].disabled) return;
    const changed = source.selectedIndex !== active;
    source.selectedIndex = active;
    close();
    refresh();
    if (changed) source.dispatchEvent(new Event('change', { bubbles: true }));
  }

  function move(direction) {
    let index = active;
    for (let i = 0; i < options.length; i++) {
      index = (index + direction + options.length) % options.length;
      if (!options[index].disabled) {
        highlight(index);
        return;
      }
    }
  }

  trigger.addEventListener('click', () => {
    if (open) close();
    else show();
  });
  trigger.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      if (open) {
        event.preventDefault();
        event.stopPropagation();
        close();
      }
      return;
    }
    if (event.key === 'Tab') {
      if (open) commit();
      return;
    }
    if (event.key === 'Enter' || (event.key === ' ' && !search)) {
      event.preventDefault();
      if (open) commit();
      else show();
      return;
    }
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault();
      if (!open) show();
      if (event.key === 'Home') highlight(options.findIndex((option) => !option.disabled));
      else if (event.key === 'End') highlight(options.findLastIndex((option) => !option.disabled));
      else move(event.key === 'ArrowDown' ? 1 : -1);
      return;
    }
    if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      event.preventDefault();
      if (!open) show();
      search += event.key.toLocaleLowerCase();
      clearTimeout(searchTimer);
      searchTimer = setTimeout(() => {
        search = '';
      }, 700);
      const match = options.findIndex(
        (option) => !option.disabled && option.label.toLocaleLowerCase().startsWith(search),
      );
      if (match >= 0) highlight(match);
    }
  });
  trigger.addEventListener('blur', close);
  popup.addEventListener('pointerdown', (event) => event.preventDefault());
  popup.addEventListener('pointermove', (event) => {
    const item = event.target.closest('[data-index]');
    if (item && !options[Number(item.dataset.index)].disabled) highlight(Number(item.dataset.index), false);
  });
  popup.addEventListener('click', (event) => {
    const item = event.target.closest('[data-index]');
    if (item && !options[Number(item.dataset.index)].disabled) {
      active = Number(item.dataset.index);
      commit();
    }
  });
  document.addEventListener('pointerdown', (event) => {
    if (!wrapper.contains(event.target)) close();
  });
  window.addEventListener('resize', close);
  document.addEventListener(
    'scroll',
    (event) => {
      if (open && event.target !== popup) position();
    },
    true,
  );
  source.addEventListener('change', refresh);
  source.form?.addEventListener('reset', () => queueMicrotask(refresh));
  refresh();
  return refresh;
}
