export function createPowerParticles(button) {
  const layer = document.createElement('span');
  layer.className = 'power-ambient';
  layer.setAttribute('aria-hidden', 'true');
  button.append(layer);

  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
  const particles = new Set();
  let enabled = false;
  let rate = 2;
  let timer = 0;

  function stop() {
    clearTimeout(timer);
    timer = 0;
    for (const particle of particles) {
      for (const animation of particle.getAnimations({ subtree: true })) animation.cancel();
      particle.remove();
    }
    particles.clear();
  }

  function schedule() {
    if (!enabled || reducedMotion.matches || document.hidden) return stop();
    if (!timer) timer = setTimeout(emit, 1000 / rate);
  }

  function spawn(angle, distance, duration, burst = false) {
    if (particles.size >= 16) return;
    const particle = document.createElement('span');
    particle.style.setProperty('--angle', `${angle}deg`);
    const dot = document.createElement('span');
    dot.className = burst ? 'power-dot power-burst-dot' : 'power-dot';
    particle.append(dot);
    layer.append(particle);
    particles.add(particle);
    const animation = dot.animate(
      [
        { transform: 'translateX(0) scale(0.7)', opacity: 0 },
        { transform: 'translateX(4px) scale(1)', opacity: 0.7, offset: 0.15 },
        { transform: `translateX(${distance}px) scale(0.25)`, opacity: 0 },
      ],
      { duration, easing: 'ease-out' },
    );
    const remove = () => {
      particle.remove();
      particles.delete(particle);
    };
    animation.finished.then(remove, remove);
  }

  function emit() {
    timer = 0;
    if (!enabled || reducedMotion.matches || document.hidden) return stop();
    spawn(Math.random() * 360, 26 + Math.random() * 10, 1200 + Math.random() * 300);
    schedule();
  }

  function burst() {
    if (!enabled || reducedMotion.matches || document.hidden) return;
    for (const angle of [-20, 0, 20, 160, 180, 200]) {
      spawn(angle, 44 + Math.random() * 20, 700 + Math.random() * 200, true);
    }
  }

  document.addEventListener('visibilitychange', schedule);
  reducedMotion.addEventListener('change', schedule);

  function update(connected, bytesPerSecond) {
    enabled = connected;
    const speed = Number.isFinite(bytesPerSecond) ? Math.max(0, bytesPerSecond) : 0;
    rate = 2 + 8 * Math.min(1, Math.log2(1 + speed / 16384) / 8);
    schedule();
  }

  return { update, burst };
}
