export function createSpeedTest({ backend, t, getState, refreshDropdowns, showError }) {
  const $ = (id) => document.getElementById(id);
  let result = { phase: 'idle', servers: [] };
  let pending = false;
  let profileName = '';
  let signature = '';
  let lastResult;
  let actionError = '';
  let polling = false;
  let actionVersion = 0;
  const animations = new Map();
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
  const number = (value, unit) => value > 0 ? `${value.toFixed(1)} ${t(unit)}` : '—';

  function render() {
    const nextSignature = JSON.stringify([result.servers, document.documentElement.lang]);
    if (signature !== nextSignature) {
      signature = nextSignature;
      const selected = $('speed-server').value;
      $('speed-server').replaceChildren(...(result.servers || []).map((server) =>
        new Option(server.id === 'auto' ? t('speedAuto') : server.name, server.id)));
      if ([...$('speed-server').options].some((option) => option.value === selected)) $('speed-server').value = selected;
    }
    const transitioning = Boolean(getState().connection.retryError) || !['connected', 'disconnected', 'failed'].includes(getState().connection.state);
    $('speed-start').textContent = t(result.running ? 'speedStop' : 'speedStart');
    $('speed-start').disabled = pending || (!result.running && transitioning);
    $('speed-server').disabled = pending || result.running;
    $('speed-latency').textContent = number(result.latencyMs, 'speedMs');
    $('speed-download').textContent = number(result.downloadMbps, 'speedMbps');
    $('speed-upload').textContent = number(result.uploadMbps, 'speedMbps');
    renderProgress();
    const phases = { idle: 'speedIdle', latency: 'speedLatencyPhase', download: 'speedDownloadPhase', upload: 'speedUploadPhase', done: 'speedDone', cancelled: 'speedCancelled', failed: 'speedFailed' };
    $('speed-phase').hidden = !result.running || Boolean(result.error);
    $('speed-phase').textContent = result.running ? t(phases[result.phase] || 'speedIdle') : '';
    $('speed-error').hidden = !result.error && !actionError;
    $('speed-error').textContent = actionError || (result.error ? t('speedFailed') : '');
    $('speed-context').textContent = profileName ? t('speedProfile', { name: profileName }) : t('speedDirect');
    const server = result.servers?.find((item) => item.id === result.serverID);
    if (server && server.id !== 'auto' && result.phase !== 'idle') $('speed-context').textContent += ` · ${server.name}`;
    if (result.phase === 'done') lastResult = result;
    $('speed-summary').hidden = !lastResult;
    if (lastResult) {
      $('speed-summary').textContent = t('speedLast', {
        download: number(lastResult.downloadMbps, 'speedMbps'),
        upload: number(lastResult.uploadMbps, 'speedMbps'),
        time: new Date(lastResult.finishedAt).toLocaleTimeString(document.documentElement.lang, { hour: '2-digit', minute: '2-digit' }),
      });
    }
    refreshDropdowns();
  }

  function renderProgress() {
    for (const stage of ['download', 'upload']) {
      const bar = $(`speed-${stage}-progress`);
      const fill = bar.firstElementChild;
      const startedAt = result[`${stage}StartedAt`] || 0;
      const duration = result.measurementMs;
      let progress = Math.max(0, Math.min(1, Number(result[`${stage}Progress`]) || 0));
      let entry = animations.get(stage);
      if (entry && (entry.startedAt !== startedAt || reducedMotion.matches || !$('speed-dialog').open)) {
        entry.animation.cancel();
        animations.delete(stage);
        entry = undefined;
      }
      if (startedAt && duration > 0 && !reducedMotion.matches && $('speed-dialog').open) {
        if (!entry) {
          const animation = fill.animate(
            [{ transform: 'scaleX(0)' }, { transform: 'scaleX(1)' }],
            { duration, easing: 'linear', fill: 'forwards' },
          );
          animation.currentTime = result.running
            ? Math.max(0, Math.min(duration, Date.now() - startedAt))
            : progress * duration;
          entry = { startedAt, animation };
          animations.set(stage, entry);
        }
        if (progress === 1) entry.animation.finish();
        else if (!result.running || result.phase !== stage) entry.animation.pause();
        progress = Math.max(0, Math.min(1, Number(entry.animation.currentTime) / duration));
      } else fill.style.transform = `scaleX(${progress})`;
      bar.setAttribute('aria-valuenow', String(Math.round(progress * 100)));
    }
  }

  reducedMotion.addEventListener('change', renderProgress);
  $('speed-dialog').addEventListener('close', renderProgress);
  setInterval(() => {
    if ($('speed-dialog').open && result.running) poll().catch(showError);
  }, 100);

  async function poll() {
    if (polling || pending || (!$('speed-dialog').open && !result.running)) return;
    polling = true;
    const version = actionVersion;
    try {
      const next = await backend().GetSpeedTest();
      if (version === actionVersion) { result = next; render(); }
    } finally { polling = false; }
  }

  $('speed-open').addEventListener('click', async () => {
    $('speed-dialog').showModal();
    try {
      await poll();
    } catch (error) { showError(error, 'speed-error'); }
  });
  $('speed-start').addEventListener('click', async () => {
    if (pending) return;
    pending = true;
    actionVersion++;
    actionError = '';
    render();
    try {
      if (result.running) result = await backend().StopSpeedTest();
      else {
        const state = getState();
        profileName = state.connection.state === 'connected'
          ? state.profiles.find((profile) => profile.id === state.activeID)?.name || '' : '';
        result = await backend().StartSpeedTest($('speed-server').value);
      }
    } catch (error) { actionError = String(error?.message || error); }
    finally { pending = false; render(); }
  });
  return { render, poll };
}
