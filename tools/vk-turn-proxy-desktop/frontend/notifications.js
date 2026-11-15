export function createNotifications(container) {
  const messages = new Map();
  const lifetime = 10000;
  const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');

  return function notify(text, dismissLabel) {
    const existing = messages.get(text);
    if (existing) {
      existing.repeat();
      return;
    }

    const element = document.createElement('div');
    element.className = 'notification';
    const message = document.createElement('span');
    message.className = 'notification-message';
    message.textContent = text;
    const count = document.createElement('span');
    count.className = 'notification-count';
    count.hidden = true;
    const close = document.createElement('button');
    close.type = 'button';
    close.className = 'icon-button notification-close';
    const countdown = document.createElement('span');
    countdown.className = 'notification-countdown';
    countdown.setAttribute('aria-hidden', 'true');
    const ring = document.createElement('span');
    ring.className = 'notification-ring';
    const seconds = document.createElement('span');
    seconds.className = 'notification-seconds';
    countdown.append(ring, seconds);
    const cross = document.createElement('span');
    cross.className = 'notification-dismiss-icon';
    cross.setAttribute('aria-hidden', 'true');
    close.append(countdown, cross);
    close.dataset.i18nAria = 'dismiss';
    close.setAttribute('aria-label', dismissLabel);
    element.append(message, count, close);

    let timer;
    let frame;
    let deadline;
    let remaining = lifetime;
    let repeats = 1;
    let hovered = false;
    let focused = false;

    const renderCountdown = (timeLeft) => {
      const value = Math.ceil(timeLeft / 1000);
      if (seconds.textContent !== String(value)) seconds.textContent = value;
      const progress = ((reducedMotion.matches ? value * 1000 : timeLeft) / lifetime).toFixed(4);
      if (ring.style.getPropertyValue('--remaining') !== progress)
        ring.style.setProperty('--remaining', progress);
    };
    const tick = () => {
      const timeLeft = Math.max(0, deadline - performance.now());
      renderCountdown(timeLeft);
      if (timeLeft > 0) frame = requestAnimationFrame(tick);
    };

    const dismiss = () => {
      if (messages.get(text) !== entry) return;
      messages.delete(text);
      clearTimeout(timer);
      cancelAnimationFrame(frame);
      close.disabled = true;
      const duration = parseFloat(getComputedStyle(element).getPropertyValue('--motion-fast'));
      element
        .animate(
          [
            { opacity: 1, transform: 'translateY(0)' },
            { opacity: 0, transform: 'translateY(-6px)' },
          ],
          { duration, easing: 'ease-in', fill: 'forwards' },
        )
        .finished.finally(() => element.remove());
    };

    const resume = () => {
      clearTimeout(timer);
      cancelAnimationFrame(frame);
      renderCountdown(remaining);
      if (hovered || focused || messages.get(text) !== entry) return;
      deadline = performance.now() + remaining;
      timer = setTimeout(dismiss, remaining);
      frame = requestAnimationFrame(tick);
    };
    const pause = () => {
      if (!timer) return;
      remaining = Math.max(0, deadline - performance.now());
      clearTimeout(timer);
      cancelAnimationFrame(frame);
      timer = undefined;
      renderCountdown(remaining);
    };

    const entry = {
      repeat() {
        count.hidden = false;
        count.textContent = `×${++repeats}`;
        remaining = lifetime;
        resume();
      },
    };
    messages.set(text, entry);
    close.addEventListener('click', dismiss);
    element.addEventListener('pointerenter', () => {
      hovered = true;
      pause();
    });
    element.addEventListener('pointerleave', () => {
      hovered = false;
      resume();
    });
    element.addEventListener('focusin', () => {
      focused = true;
      pause();
    });
    element.addEventListener('focusout', (event) => {
      if (element.contains(event.relatedTarget)) return;
      focused = false;
      resume();
    });
    container.append(element);
    resume();
  };
}
