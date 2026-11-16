let activeTransition;

export async function revealTheme(update, source) {
  if (!document.startViewTransition || matchMedia('(prefers-reduced-motion: reduce)').matches) {
    update();
    return;
  }

  activeTransition?.skipTransition();
  for (const animation of document.getAnimations()) {
    if (animation.animationName === 'page-in' || animation.animationName === 'dialog-in') animation.finish();
  }
  const root = document.documentElement;
  const bounds = source.getBoundingClientRect();
  const x = bounds.x + bounds.width / 2;
  const y = bounds.y + bounds.height / 2;
  const radius = Math.ceil(Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y)));
  root.style.setProperty('--theme-origin-x', `${x}px`);
  root.style.setProperty('--theme-origin-y', `${y}px`);
  root.style.setProperty('--theme-radius', `${radius}px`);
  root.classList.add('theme-transitioning');

  const transition = document.startViewTransition(update);
  activeTransition = transition;
  transition.finished
    .catch(() => {})
    .finally(() => {
      if (activeTransition === transition) {
        activeTransition = undefined;
        root.classList.remove('theme-transitioning');
      }
    });
  await transition.updateCallbackDone;
}
