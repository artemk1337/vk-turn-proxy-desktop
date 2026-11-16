export function setSegmentedValue(element, value) {
  const buttons = [...element.querySelectorAll(':scope > button[data-value]')];
  const index = buttons.findIndex((button) => button.dataset.value === value);
  if (index < 0) return;
  element.style.setProperty('--segment-count', buttons.length);
  element.style.setProperty('--selected-index', index);
  for (const button of buttons) {
    const selected = button.dataset.value === value;
    button.setAttribute('aria-pressed', String(selected));
    button.tabIndex = selected ? 0 : -1;
  }
}

export function initializeSegmentedControl(element) {
  const choose = (button) => {
    if (button.disabled || button.getAttribute('aria-pressed') === 'true') return;
    setSegmentedValue(element, button.dataset.value);
    element.dispatchEvent(new CustomEvent('segment-change', { detail: { value: button.dataset.value } }));
  };

  element.addEventListener('click', (event) => {
    const button = event.target.closest('button[data-value]');
    if (button?.parentElement === element) choose(button);
  });
  element.addEventListener('keydown', (event) => {
    const buttons = [...element.querySelectorAll(':scope > button[data-value]')].filter(
      (button) => !button.disabled,
    );
    const index = buttons.indexOf(document.activeElement);
    if (index < 0) return;
    let next;
    switch (event.key) {
      case 'ArrowRight':
        next = (index + 1) % buttons.length;
        break;
      case 'ArrowLeft':
        next = (index - 1 + buttons.length) % buttons.length;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = buttons.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    buttons[next].focus();
    choose(buttons[next]);
  });
}

export function initializeHelpTooltips(root) {
  for (const help of root.querySelectorAll('.help')) {
    const tooltip = help.querySelector('.help-tooltip');
    const button = help.querySelector('.help-button');
    if (!tooltip || !button) continue;
    const supportsPopover = typeof tooltip.showPopover === 'function';
    if (supportsPopover) tooltip.setAttribute('popover', 'manual');

    const position = () => {
      const anchor = button.getBoundingClientRect();
      const margin = 12;
      const gap = 8;
      const width = tooltip.offsetWidth;
      const height = tooltip.offsetHeight;
      const left = Math.max(margin, Math.min(anchor.left, window.innerWidth - width - margin));
      const above = anchor.top - height - gap;
      const top = above >= margin ? above : anchor.bottom + gap;
      tooltip.style.left = `${left}px`;
      tooltip.style.top = `${Math.max(margin, Math.min(top, window.innerHeight - height - margin))}px`;
    };
    const show = () => {
      if (supportsPopover && !tooltip.matches(':popover-open')) tooltip.showPopover();
      position();
      tooltip.classList.add('is-visible');
    };
    const hide = () => {
      tooltip.classList.remove('is-visible');
      if (supportsPopover && tooltip.matches(':popover-open')) tooltip.hidePopover();
    };
    help.addEventListener('mouseenter', show);
    help.addEventListener('mouseleave', () => {
      if (!help.contains(document.activeElement)) hide();
    });
    help.addEventListener('focusin', show);
    help.addEventListener('focusout', () => {
      if (!help.matches(':hover')) hide();
    });
    button.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && tooltip.classList.contains('is-visible')) {
        event.preventDefault();
        event.stopPropagation();
        hide();
      }
    });
    window.addEventListener('resize', hide);
    root.addEventListener('scroll', hide, true);
    help.closest('dialog')?.addEventListener('close', hide);
  }
}
