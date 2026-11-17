const response = await fetch('./settings.json');
if (!response.ok) throw new Error('Could not load demo settings.');
const defaults = await response.json();
const language = new URLSearchParams(location.search).get('lang') === 'en' ? 'en' : 'ru';
const clone = (value) => structuredClone(value);
const state = {
  defaults,
  updates: { currentVersion: "demo", latestVersion: "", status: "development", url: "" },
  preferences: {
    language, theme: 'dark', autoStart: false, logLines: defaults.logLines.default,
    routingAutoUpdate: true,
    srtpHandshakeSeconds: defaults.handshakeTimeouts.srtp.default,
    dtlsHandshakeSeconds: defaults.handshakeTimeouts.dtls.default,
    wrapAHandshakeSeconds: defaults.handshakeTimeouts.wrapA.default,
  },
  profiles: [{
    id: 'demo', name: language === 'ru' ? 'Личный профиль' : 'Personal profile', mode: 'wireguard',
    profileText: '',
    wgConfig: '[Interface]\nPrivateKey = DEMO_KEY\nAddress = 10.10.0.2/32\nDNS = 1.1.1.1\n\n[Peer]\nPublicKey = DEMO_PUBLIC_KEY\nAllowedIPs = 0.0.0.0/0\nEndpoint = vpn.example.com:51820',
    vkLink: 'https://vk.com/call/demo', endpoint: 'vpn.example.com:51820', serverName: '',
    useSRTP: true, turnTransport: '', numConnections: defaults.connections.default,
    mtu: defaults.mtu.default,
    routing: { mode: '', geoip: ['ru'], geosite: ['youtube', 'telegram'], custom: '', localDirect: true },
  }],
  activeID: 'demo', connection: { state: 'disconnected' },
  vk: { status: 'signed_out', checking: false }, error: '',
};
const banner = document.createElement('div');
banner.id = 'demo-banner';
banner.setAttribute('role', 'note');
document.body.prepend(banner);
function renderBanner() {
  banner.textContent = state.preferences.language === 'ru'
    ? 'Демонстрация · Подключение и данные смоделированы'
    : 'Demo · Connection and data are simulated';
}
renderBanner();
document.documentElement.dataset.theme = 'dark';

const logs = { revision: 1, lines: ['[demo] Interface ready. All connection activity is simulated.'] };
function log(message) {
  logs.lines.push(`[${new Date().toLocaleTimeString('en-GB')}] [demo] ${message}`);
  logs.lines = logs.lines.slice(-state.preferences.logLines);
  logs.revision++;
}
const catalog = {
  geoip: ['ru', 'us', 'de', 'nl', 'fi', 'gb', 'fr', 'jp'].map((id) => ({ id, name: id.toUpperCase(), group: 'Countries' })),
  geosite: [
    { id: 'youtube', name: 'YouTube', group: 'Media' },
    { id: 'netflix', name: 'Netflix', group: 'Media' },
    { id: 'spotify', name: 'Spotify', group: 'Media' },
    { id: 'telegram', name: 'Telegram', group: 'Social' },
    { id: 'discord', name: 'Discord', group: 'Social' },
    { id: 'github', name: 'GitHub', group: 'Development' },
    { id: 'google', name: 'Google', group: 'Other' },
  ],
  updatedAt: new Date().toISOString(), autoUpdate: true,
};
let connectionTimers = [];
function cancelConnectionTimers() {
  connectionTimers.forEach(clearTimeout);
  connectionTimers = [];
}
function setConnection(phase) {
  state.connection = { state: phase };
  if (phase === 'connected') {
    Object.assign(state.connection, {
      downloadBytesPerSecond: 4_200_000, uploadBytesPerSecond: 780_000,
      trafficBytesPerSecond: 4_980_000,
    });
  }
  log(`Connection ${phase}.`);
}
function findProfile(id) {
  const profile = state.profiles.find((item) => item.id === id);
  if (!profile) throw new Error('Profile does not exist.');
  return profile;
}
function requireDisconnected() {
  if (state.connection.state !== 'disconnected') throw new Error('Disconnect before changing profiles.');
}
function unavailable() {
  throw new Error(state.preferences.language === 'ru'
    ? 'Это демоверсия. Вход в VK и импорт cookies доступны в установленном приложении.'
    : 'This is a demo. VK sign-in and cookie import are available in the installed app.');
}

let speed = {
  phase: 'idle', running: false, serverID: 'auto',
  servers: [{ id: 'auto', name: 'Auto' }, { id: 'demo', name: 'Demo server' }],
  latencyMs: 0, downloadMbps: 0, uploadMbps: 0,
  downloadProgress: 0, uploadProgress: 0, measurementMs: 2500, error: '',
};
let speedStartedAt = 0;
function updateSpeed() {
  if (!speed.running) return clone(speed);
  const elapsed = Date.now() - speedStartedAt;
  speed.latencyMs = 18.4;
  speed.phase = elapsed < 600 ? 'latency' : elapsed < 3100 ? 'download' : elapsed < 5600 ? 'upload' : 'done';
  speed.downloadStartedAt = elapsed >= 600 ? speedStartedAt + 600 : 0;
  speed.uploadStartedAt = elapsed >= 3100 ? speedStartedAt + 3100 : 0;
  speed.downloadProgress = Math.max(0, Math.min(1, (elapsed - 600) / 2500));
  speed.uploadProgress = Math.max(0, Math.min(1, (elapsed - 3100) / 2500));
  speed.downloadMbps = 86.2 * speed.downloadProgress;
  speed.uploadMbps = 32.8 * speed.uploadProgress;
  if (speed.phase === 'done') {
    speed.running = false;
    speed.finishedAt = new Date().toISOString();
    log('Simulated speed test finished.');
  }
  return clone(speed);
}

window.go = { main: { App: {
  CheckForUpdates: async () => clone(state),
  DownloadUpdate: async () => unavailable(),
  GetState: async () => clone(state),
  FrontendReady: async () => {},
  SavePreferences: async (preferences) => {
    state.preferences = clone(preferences);
    renderBanner();
    return clone(state);
  },
  SaveProfile: async (value) => {
    requireDisconnected();
    const profile = clone(value);
    profile.id ||= crypto.randomUUID();
    const index = state.profiles.findIndex((item) => item.id === profile.id);
    if (index < 0) state.profiles.push(profile);
    else state.profiles[index] = profile;
    state.activeID = profile.id;
    log('Demo profile saved for this tab.');
    return clone(state);
  },
  DeleteProfile: async (id) => {
    requireDisconnected();
    findProfile(id);
    state.profiles = state.profiles.filter((profile) => profile.id !== id);
    if (state.activeID === id) state.activeID = state.profiles[0]?.id || '';
    return clone(state);
  },
  SelectProfile: async (id) => {
    requireDisconnected();
    findProfile(id);
    state.activeID = id;
    return clone(state);
  },
  Connect: async () => {
    findProfile(state.activeID);
    cancelConnectionTimers();
    setConnection('credentials');
    for (const [index, phase] of ['relay', 'handshake', 'connected'].entries()) {
      connectionTimers.push(setTimeout(() => setConnection(phase), (index + 1) * 1100));
    }
    return clone(state);
  },
  Disconnect: async () => {
    cancelConnectionTimers();
    setConnection('disconnecting');
    connectionTimers.push(setTimeout(() => setConnection('disconnected'), 600));
    return clone(state);
  },
  LoginVK: async () => unavailable(),
  ImportCookies: async () => unavailable(),
  LogoutVK: async () => clone(state),
  GetLogs: async () => clone(logs),
  ClearLogs: async () => { logs.lines = []; logs.revision++; return clone(state); },
  GetRoutingCatalog: async () => clone(catalog),
  UpdateRoutingData: async () => { catalog.updatedAt = new Date().toISOString(); return clone(catalog); },
  SetRoutingAutoUpdate: async (enabled) => {
    catalog.autoUpdate = enabled;
    state.preferences.routingAutoUpdate = enabled;
    return clone(catalog);
  },
  SaveRouting: async (id, settings) => {
    findProfile(id).routing = clone(settings);
    log('Demo routing policy saved.');
    return clone(state);
  },
  GetSpeedTest: async () => updateSpeed(),
  StartSpeedTest: async (serverID) => {
    speedStartedAt = Date.now();
    speed = {
      ...speed, serverID, phase: 'latency', running: true, error: '', finishedAt: '',
      latencyMs: 0, downloadMbps: 0, uploadMbps: 0, downloadProgress: 0, uploadProgress: 0,
      downloadStartedAt: 0, uploadStartedAt: 0,
    };
    return clone(speed);
  },
  StopSpeedTest: async () => {
    updateSpeed();
    speed.running = false;
    speed.phase = 'cancelled';
    return clone(speed);
  },
  Exit: async () => {
    cancelConnectionTimers();
    setConnection('disconnected');
    speed.running = false;
    location.reload();
  },
} } };
