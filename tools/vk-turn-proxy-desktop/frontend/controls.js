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
