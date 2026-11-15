import { translate } from './i18n.js';
import { initializeSegmentedControl, setSegmentedValue } from './controls.js';
import { revealTheme } from './theme.js';
import { createNotifications } from './notifications.js';
import { initializeDropdowns } from './dropdown.js';
import { createPowerParticles } from './particles.js';
import { createRouting } from './routing.js';

const $ = (id) => document.getElementById(id);
const powerParticles = createPowerParticles($('power-orbit'));
const notify = createNotifications($('notifications'));
const refreshDropdowns = initializeDropdowns(document);
const maxInputBytes = 4 * 1024 * 1024;
const icons = {
  power: '<path d="M12 2v10"/><path d="M6.2 5.7a8 8 0 1 0 11.6 0"/>',
  routing: '<path d="M5 4v12a4 4 0 0 0 4 4h2M5 8h10a4 4 0 0 1 4 4v8M2 7l3-3 3 3M16 17l3 3 3-3"/>',
  log: '<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M9 8h6M9 12h6M9 16h3"/>',
  settings:
    '<path d="m9.2 3-.6 2.2-2 .9-2-.6-2.1 3.6L4 10.7v2.6l-1.5 1.6 2.1 3.6 2-.6 2 .9.6 2.2h4.2l.6-2.2 2-.9 2 .6 2.1-3.6-1.5-1.6v-2.6l1.5-1.6L18.1 5l-2 .6-2-.9-.6-2.2z"/><circle cx="11.3" cy="12" r="3"/>',
  profile: '<circle cx="12" cy="8" r="3.5"/><path d="M4 21v-2a7 7 0 0 1 7-7h2a7 7 0 0 1 7 7v2z"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  edit: '<path d="m15 4 5 5M4 20l5-1 12-12a2.1 2.1 0 0 0-5-5L4 15z"/>',
  arrow: '<path d="M5 12h14m-5-5 5 5-5 5"/>',
  close: '<path d="m6 6 12 12M18 6 6 18"/>',
  moon: '<path d="M20.5 13a8.5 8.5 0 0 1-9.5-9.5A8.5 8.5 0 1 0 20.5 13z"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5"/>',
  exit: '<path d="M10 4H5v16h5m-1-8h12m-4-4 4 4-4 4"/>',
};

let state = {
  preferences: {
    language: 'en',
    theme: document.documentElement.dataset.theme,
    autoStart: false,
    logLines: 1000,
  },
  profiles: [],
  activeID: '',
  connection: { state: 'disconnected' },
  vk: { status: 'signed_out', checking: false },
};
let page = 'connection';
let pending = false;
let polling = false;
let editorID = '';
let editorMode = 'link';
let fileTarget = '';
let profileSignature = '';
let logRevision = -1;
let displayedLogs = [];
let lastBackendError = '';
let preferencesQueue = Promise.resolve();
let savingPreferences = 0;
let mutationVersion = 0;
let connectionFlash;
const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
const t = (key, values) => translate(state.preferences.language, key, values);
const routing = createRouting({ getState: () => state, backend, mutate, t, showError, refreshDropdowns });

function renderIcon(element, name) {
  if (element.dataset.renderedIcon === name) return;
  element.dataset.renderedIcon = name;
  element.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true">${icons[name]}</svg>`;
}

document.querySelectorAll('[data-icon]').forEach((element) => renderIcon(element, element.dataset.icon));
document.querySelectorAll('[data-segmented]').forEach(initializeSegmentedControl);

function backend() {
  const bridge = window.go?.main?.App;
  if (!bridge) throw new Error(t('desktopOnly'));
  return bridge;
}

function showError(error, target) {
  const text = String(error?.message || error);
  if (!target) notify(text, t('dismiss'));
  else {
    $(target).textContent = text;
    $(target).hidden = false;
  }
}

function applyState(next) {
  if (!next) return;
  const justConnected = next.connection.state === 'connected' &&
    ['credentials', 'relay', 'handshake'].includes(state.connection.state);
  if (next.connection.state !== 'connected') connectionFlash?.cancel();
  state = next;
  render();
  if (justConnected && page === 'connection' && !document.hidden && !reducedMotion.matches) {
    powerParticles.burst();
    connectionFlash?.cancel();
    connectionFlash = $('power-flash-band').animate(
      [
        { transform: 'translateX(-120%)', opacity: 0 },
        { opacity: 0.7, offset: 0.2 },
        { opacity: 0.7, offset: 0.7 },
        { transform: 'translateX(120%)', opacity: 0 },
      ],
      { duration: 750, easing: 'ease-out' },
    );
  }
  if (state.error && state.error !== lastBackendError) showError(state.error);
  lastBackendError = state.error || '';
}

async function mutate(method, ...args) {
  mutationVersion++;
  const result = await backend()[method](...args);
  applyState(result);
  return result;
}

async function action(callback, errorTarget) {
  if (pending) return;
  pending = true;
  renderConnection();
  try {
    await callback();
  } catch (error) {
    showError(error, errorTarget);
  } finally {
    pending = false;
    renderConnection();
  }
}

function renderLanguage() {
  document.documentElement.lang = state.preferences.language;
  document.querySelectorAll('[data-i18n]').forEach((element) => {
    const text = t(element.dataset.i18n);
    if (element.textContent !== text) element.textContent = text;
  });
  document.querySelectorAll('[data-i18n-aria]').forEach((element) => {
    element.setAttribute('aria-label', t(element.dataset.i18nAria));
  });
  document.querySelectorAll('[data-i18n-placeholder]').forEach((element) => {
    element.placeholder = t(element.dataset.i18nPlaceholder);
  });
  $('profile-title').textContent = t('profileTitle');
}

function renderPreferences() {
  const preferences = state.preferences;
  document.documentElement.dataset.theme = preferences.theme;
  try {
    localStorage.setItem('vkturn.theme', preferences.theme);
  } catch (_) {}
  $('theme-label').textContent = t(preferences.theme === 'dark' ? 'lightTheme' : 'darkTheme');
  $('theme-toggle').setAttribute('aria-label', $('theme-label').textContent);
  renderIcon($('theme-icon'), preferences.theme === 'dark' ? 'sun' : 'moon');
  setSegmentedValue($('theme-choice'), preferences.theme);
  $('language').value = preferences.language;
  $('autostart').checked = preferences.autoStart;
  if (document.activeElement !== $('log-limit')) $('log-limit').value = preferences.logLines;
  const limits = state.defaults?.logLines || { min: 100, max: 10000 };
  $('log-limit').min = limits.min;
  $('log-limit').max = limits.max;
  $('log-limit-hint').textContent = t('logLimitHint', limits);
}

function renderProfiles() {
  const signature = JSON.stringify([
    state.activeID,
    state.profiles.map(({ id, name }) => [id, name]),
    state.preferences.language,
  ]);
  if (signature === profileSignature) {
    $('profile-choice').value = state.activeID;
    return;
  }
  profileSignature = signature;
  const options = state.profiles.map((profile) => new Option(profile.name, profile.id));
  if (!options.length) options.push(new Option(t('addProfile'), ''));
  $('profile-choice').replaceChildren(...options);
  $('profile-choice').value = state.activeID;
}

function renderConnection() {
  const phase = state.connection.state;
  const active = !['disconnected', 'failed'].includes(phase);
  const busy = active && phase !== 'connected';
  const selected = state.profiles.find((profile) => profile.id === state.activeID);
  if ($('connection-status').textContent !== t(phase)) $('connection-status').textContent = t(phase);
  let hint = `${phase}Hint`;
  if (phase === 'disconnected' && selected) hint = 'readyHint';
  $('connection-hint').textContent = t(hint);
  $('connect-label').textContent = t(
    active ? (phase === 'connected' ? 'disconnect' : 'cancelConnection') : 'connect',
  );
  $('power-orbit').disabled = pending || phase === 'disconnecting' || (!active && !selected);
  $('power-orbit').setAttribute('aria-label', $('connect-label').textContent);
  $('power-orbit').setAttribute('aria-pressed', String(active));
  $('power-orbit').setAttribute('aria-busy', String(pending || busy));
  $('power-orbit').classList.toggle('busy', busy);
  $('power-orbit').classList.toggle('connected', phase === 'connected');
  powerParticles.update(phase === 'connected' && page === 'connection', state.connection.trafficBytesPerSecond);
  $('status-dot').classList.toggle('connected', phase === 'connected');
  $('status-dot').classList.toggle('busy', busy);
  $('footer-status').textContent = t(phase);
  $('footer-profile').textContent = selected?.name || '';
  $('profile-choice').disabled = pending || active || !state.profiles.length;
  $('edit-profile').disabled = pending || active || !selected;
  $('add-profile').disabled = pending || active;
  const authStatus = state.vk.checking
    ? 'checking'
    : { signed_out: 'anonymous', valid: 'valid', rejected: 'rejected', unknown: 'unknown' }[
        state.vk.status
      ] || 'unknown';
  $('vk-status').textContent = t(authStatus);
  $('vk-login').hidden = state.vk.status === 'valid';
  $('vk-logout').hidden = state.vk.status === 'signed_out';
  for (const id of ['vk-login', 'vk-logout', 'cookies-open']) $(id).disabled = pending || state.vk.checking;
  refreshDropdowns();
}

function render() {
  renderLanguage();
  renderPreferences();
  renderProfiles();
  renderConnection();
  routing.render();
  $('log-count').textContent = `${displayedLogs.length} ${t('lines')}`;
}

function switchPage(next) {
  connectionFlash?.cancel();
  page = next;
  for (const name of ['connection', 'routing', 'logs', 'settings']) $(`${name}-page`).hidden = name !== page;
  document.querySelectorAll('[data-page]').forEach((button) => {
    const active = button.dataset.page === page;
    button.classList.toggle('active', active);
    if (active) button.setAttribute('aria-current', 'page');
    else button.removeAttribute('aria-current');
  });
  renderConnection();
  if (page === 'logs') refreshLogs().catch(showError);
  if (page === 'routing') routing.open();
}

function savePreferences(change, source) {
  savingPreferences++;
  preferencesQueue = preferencesQueue.then(async () => {
    const previous = state.preferences;
    const update = () => {
      state.preferences = { ...previous, ...change };
      render();
    };
    try {
      if (source && change.theme && change.theme !== previous.theme) await revealTheme(update, source);
      else update();
      await mutate('SavePreferences', state.preferences);
    } catch (error) {
      state.preferences = previous;
      render();
      showError(error);
    } finally {
      savingPreferences--;
    }
  });
  return preferencesQueue;
}

function editorValues() {
  return {
    ...state.profiles.find((profile) => profile.id === editorID),
    id: editorID,
    name: $('profile-name').value.trim(),
    mode: editorMode,
    profileText: $('profile-text').value,
    wgConfig: $('wg-config').value,
    vkLink: $('vk-link').value.trim(),
    endpoint: $('endpoint').value.trim(),
    serverName: state.profiles.find((profile) => profile.id === editorID)?.serverName || '',
    useSRTP: $('transport').value === 'srtp',
  };
}

function setEditorMode(mode) {
  editorMode = mode;
  $('link-fields').hidden = mode !== 'link';
  $('wireguard-fields').hidden = mode !== 'wireguard';
  setSegmentedValue($('profile-mode'), mode);
}

function openEditor(profile = {}) {
  editorID = profile.id || '';
  $('profile-form').reset();
  $('profile-name').value = profile.name || '';
  $('profile-text').value = profile.profileText || '';
  $('wg-config').value = profile.wgConfig || '';
  $('vk-link').value = profile.vkLink || '';
  $('endpoint').value = profile.endpoint || '';
  $('transport').value = profile.useSRTP === false ? 'dtls' : state.defaults?.defaultTransport || 'srtp';
  $('delete-profile').hidden = !editorID;
  $('profile-error').hidden = true;
  setEditorMode(profile.mode || 'link');
  $('profile-dialog').showModal();
  refreshDropdowns();
  $('profile-name').focus();
}

function byteLength(text) {
  return new TextEncoder().encode(text).length;
}

async function submitProfile(event) {
  event.preventDefault();
  const profile = editorValues();
  $('profile-error').hidden = true;
  if (!profile.name) return showError(t('nameRequired'), 'profile-error');
  if (profile.mode === 'link' && !profile.profileText.trim())
    return showError(t('linkRequired'), 'profile-error');
  if (profile.mode === 'wireguard' && (!profile.wgConfig.trim() || !profile.vkLink))
    return showError(t('configRequired'), 'profile-error');
  if (byteLength(profile.profileText) > maxInputBytes || byteLength(profile.wgConfig) > maxInputBytes)
    return showError(t('inputTooLarge'), 'profile-error');
  await action(async () => {
    await mutate('SaveProfile', profile);
    $('profile-dialog').close();
  }, 'profile-error');
}

async function deleteProfile() {
  const profile = state.profiles.find((item) => item.id === editorID);
  if (!profile) return;
  $('confirm-message').textContent = t('deleteConfirm', { name: profile.name });
  $('confirm-dialog').returnValue = '';
  $('confirm-dialog').showModal();
  $('confirm-dialog').addEventListener(
    'close',
    () => {
      if ($('confirm-dialog').returnValue !== 'delete') return;
      action(async () => {
        await mutate('DeleteProfile', profile.id);
        $('profile-dialog').close();
      }, 'profile-error');
    },
    { once: true },
  );
}

async function readProfileFile() {
  const file = $('profile-file').files[0];
  if (!file || !fileTarget) return;
  try {
    if (file.size > maxInputBytes) throw new Error(t('fileTooLarge'));
    $(fileTarget).value = await file.text();
  } catch (error) {
    showError(error, fileTarget === 'cookies-text' ? 'cookies-error' : 'profile-error');
  }
  $('profile-file').value = '';
}

async function refreshLogs() {
  const snapshot = await backend().GetLogs();
  if (snapshot.revision === logRevision) return;
  logRevision = snapshot.revision;
  const output = $('log-output');
  const previousTop = output.scrollTop;
  const atBottom = output.scrollHeight - output.clientHeight - output.scrollTop < 32;
  const lines = snapshot.lines || [];
  const container = $('log-lines');
  let keep = 0;
  // Reuse the overlapping suffix so bounded log rotation preserves selection and scroll.
  const first = displayedLogs.indexOf(lines[0]);
  if (first >= 0) {
    while (
      keep < displayedLogs.length - first &&
      keep < lines.length &&
      displayedLogs[first + keep] === lines[keep]
    )
      keep++;
  }
  let removedHeight = 0;
  if (keep > 0) {
    for (let i = 0; i < first; i++) {
      removedHeight += container.firstElementChild.offsetHeight;
      container.firstElementChild.remove();
    }
    while (container.childElementCount > keep) container.lastElementChild.remove();
  } else container.replaceChildren();
  const fragment = document.createDocumentFragment();
  for (const line of lines.slice(keep)) {
    const element = document.createElement('div');
    element.textContent = line || '\u200b';
    fragment.append(element);
  }
  container.append(fragment);
  displayedLogs = lines;
  $('logs-empty').hidden = lines.length > 0;
  $('clear-logs').hidden = lines.length === 0;
  $('follow-log').hidden = lines.length === 0;
  $('log-count').textContent = `${lines.length} ${t('lines')}`;
  if (atBottom) output.scrollTop = output.scrollHeight;
  else if (removedHeight) output.scrollTop = Math.max(0, previousTop - removedHeight);
}

async function poll() {
  if (polling || pending || savingPreferences) return;
  polling = true;
  const version = mutationVersion;
  try {
    const next = await backend().GetState();
    if (version === mutationVersion && !savingPreferences) applyState(next);
    if (page === 'logs') await refreshLogs();
  } catch (error) {
    showError(error);
  } finally {
    polling = false;
  }
}

document
  .querySelectorAll('[data-page]')
  .forEach((button) => button.addEventListener('click', () => switchPage(button.dataset.page)));
$('theme-choice').addEventListener('segment-change', (event) =>
  savePreferences({ theme: event.detail.value }, event.currentTarget),
);
$('profile-mode').addEventListener('segment-change', (event) => setEditorMode(event.detail.value));
document
  .querySelectorAll('.close-dialog')
  .forEach((button) => button.addEventListener('click', () => button.closest('dialog').close()));
document.querySelectorAll('.open-file').forEach((button) =>
  button.addEventListener('click', () => {
    fileTarget = button.dataset.target;
    $('profile-file').accept = fileTarget === 'wg-config' ? '.conf,.txt' : '.json,.txt';
    $('profile-file').click();
  }),
);
$('theme-toggle').addEventListener('click', (event) =>
  savePreferences({ theme: state.preferences.theme === 'dark' ? 'light' : 'dark' }, event.currentTarget),
);
$('language').addEventListener('change', () => savePreferences({ language: $('language').value }));
$('autostart').addEventListener('change', () => savePreferences({ autoStart: $('autostart').checked }));
$('log-limit').addEventListener('change', () => {
  if ($('log-limit').reportValidity()) savePreferences({ logLines: Number($('log-limit').value) });
});
$('profile-choice').addEventListener('change', () =>
  action(() => mutate('SelectProfile', $('profile-choice').value)),
);
$('add-profile').addEventListener('click', () => openEditor());
$('edit-profile').addEventListener('click', () =>
  openEditor(state.profiles.find((profile) => profile.id === state.activeID)),
);
$('profile-form').addEventListener('submit', submitProfile);
$('delete-profile').addEventListener('click', deleteProfile);
$('profile-file').addEventListener('change', readProfileFile);
$('profile-dialog').addEventListener('close', () => {
  $('profile-form').reset();
  editorID = '';
});
$('power-orbit').addEventListener('click', () =>
  action(() =>
    mutate(['disconnected', 'failed'].includes(state.connection.state) ? 'Connect' : 'Disconnect'),
  ),
);
$('vk-login').addEventListener('click', () => action(() => mutate('LoginVK')));
$('vk-logout').addEventListener('click', () => action(() => mutate('LogoutVK')));
$('cookies-open').addEventListener('click', () => {
  $('cookies-error').hidden = true;
  $('cookies-dialog').showModal();
});
$('cookies-dialog').addEventListener('close', () => {
  $('cookies-text').value = '';
});
$('cookies-form').addEventListener('submit', (event) => {
  event.preventDefault();
  if (byteLength($('cookies-text').value) > maxInputBytes)
    return showError(t('inputTooLarge'), 'cookies-error');
  action(async () => {
    await mutate('ImportCookies', $('cookies-text').value);
    $('cookies-dialog').close();
  }, 'cookies-error');
});
$('clear-logs').addEventListener('click', () =>
  action(async () => {
    await mutate('ClearLogs');
    logRevision = -1;
    await refreshLogs();
  }),
);
$('follow-log').addEventListener('click', () => {
  $('log-output').scrollTop = $('log-output').scrollHeight;
});
$('exit').addEventListener('click', () => action(() => backend().Exit()));

document.addEventListener('visibilitychange', () => {
  if (document.hidden) connectionFlash?.cancel();
});
reducedMotion.addEventListener('change', () => connectionFlash?.cancel());

render();
try {
  applyState(await backend().GetState());
  await document.fonts.ready;
  await backend().FrontendReady();
} catch (error) {
  showError(error);
}
setInterval(poll, 1000);
