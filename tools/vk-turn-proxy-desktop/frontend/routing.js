import { setSegmentedValue } from './controls.js';

export function createRouting({ getState, backend, mutate, t, showError, refreshDropdowns }) {
  const $ = (id) => document.getElementById(id);
  const drafts = new Map();
  let profileID = '';
  let catalog = { geoip: [], geosite: [], updatedAt: '', autoUpdate: true };
  let loaded = false;
  let loading;
  let busy = false;
  let pickerType = 'geoip';
  let pending = new Set();
  let profileSignature = '';
  let language = '';

  function draft() {
    if (!drafts.has(profileID)) {
      const saved = getState().profiles.find((profile) => profile.id === profileID)?.routing;
      drafts.set(profileID, {
        mode: saved?.mode || '',
        geoip: [...(saved?.geoip || [])],
        geosite: [...(saved?.geosite || [])],
        custom: saved?.custom || '',
        localDirect: saved?.localDirect ?? true,
      });
    }
    return drafts.get(profileID);
  }

  function renderSelection(type) {
    const target = $(`routing-selected-${type}`);
    const names = new Map((catalog[type] || []).map((item) => [item.id, itemName(item, type)]));
    const items = draft()[type].map((id) => [id, names.get(id) || id]);
    const signature = JSON.stringify([items, busy, getState().preferences.language]);
    if (target.dataset.selection === signature) return;
    const focusedIndex = [...target.children].indexOf(document.activeElement);
    target.dataset.selection = signature;
    target.replaceChildren();
    for (const [id, name] of items) {
      const tag = document.createElement('button');
      tag.type = 'button';
      tag.className = 'routing-tag';
      tag.disabled = busy;
      tag.setAttribute('aria-label', t('routingRemove', { name }));
      tag.append(document.createTextNode(name));
      const remove = document.createElement('span');
      remove.className = 'routing-tag-remove';
      remove.setAttribute('aria-hidden', 'true');
      tag.append(remove);
      tag.addEventListener('click', () => {
        draft()[type] = draft()[type].filter((value) => value !== id);
        changed();
        renderSelection(type);
      });
      target.append(tag);
    }
    if (!target.childElementCount) target.textContent = t('routingNothingSelected');
    if (focusedIndex >= 0) {
      const next = target.children[Math.min(focusedIndex, target.childElementCount - 1)];
      (next || document.querySelector(`[data-routing-picker="${type}"]`)).focus();
    }
  }

  function render() {
    const state = getState();
    const profiles = state.profiles;
    if (!profiles.some((profile) => profile.id === profileID)) profileID = state.activeID || profiles[0]?.id || '';
    const signature = JSON.stringify([profiles.map(({ id, name }) => [id, name]), state.preferences.language]);
    if (signature !== profileSignature) {
      $('routing-profile').replaceChildren(...profiles.map((profile) => new Option(profile.name, profile.id)));
      profileSignature = signature;
      for (const id of drafts.keys()) if (!profiles.some((profile) => profile.id === id)) drafts.delete(id);
    }
    $('routing-profile').value = profileID;
    $('routing-profile').disabled = busy || !profiles.length;
    $('routing-empty').hidden = profiles.length > 0;
    $('routing-fields').hidden = !profiles.length;
    const settings = draft();
    setSegmentedValue($('routing-mode'), settings.mode);
    $('routing-summary').textContent = t(settings.mode === 'include' ? 'routingIncludeHint' : settings.mode === 'exclude' ? 'routingExcludeHint' : 'routingAllHint');
    $('routing-rules').hidden = !settings.mode;
    if (document.activeElement !== $('routing-custom')) $('routing-custom').value = settings.custom;
    $('routing-local').checked = settings.localDirect;
    $('routing-auto').checked = catalog.autoUpdate;
    $('routing-save').disabled = busy || !profileID;
    document.querySelectorAll('#routing-mode button, [data-routing-picker], #routing-custom, #routing-local').forEach((element) => { element.disabled = busy; });
    $('routing-sync').disabled = busy || Boolean(loading);
    $('routing-auto').disabled = busy || Boolean(loading);
    $('routing-updated').textContent = loading ? t('routingLoading') : catalog.updatedAt
      ? t('routingUpdated', { date: new Date(catalog.updatedAt).toLocaleString(state.preferences.language) })
      : t('routingNotLoaded');
    for (const type of ['geoip', 'geosite']) renderSelection(type);
    $('routing-save-hint').textContent = t($('routing-save-hint').dataset.saved ? 'routingSaved' : 'routingNextConnection');
    if (language !== state.preferences.language && $('routing-picker').open) renderPicker();
    language = state.preferences.language;
    refreshDropdowns();
  }

  async function loadCatalog(force = false) {
    if (loading) return loading;
    if (loaded && !force) return;
    loading = (async () => {
      try {
        catalog = await backend()[force ? 'UpdateRoutingData' : 'GetRoutingCatalog']();
        loaded = true;
      } catch (error) {
        showError(error, 'routing-error');
      } finally {
        loading = null;
        render();
        if ($('routing-picker').open) renderPicker();
      }
    })();
    render();
    return loading;
  }

  function groupName(group) {
    const key = `routingGroup${group.replaceAll(/[^a-zA-Z0-9]/g, '')}`;
    const value = t(key);
    return value === key ? group : value;
  }

  function itemName(item, type) {
    if (type === 'geoip' && /^[a-z]{2}$/i.test(item.id) && Intl.DisplayNames) {
      return new Intl.DisplayNames([getState().preferences.language], { type: 'region' }).of(item.id.toUpperCase()) || item.name;
    }
    return item.name;
  }

  function renderPicker() {
    $('routing-picker-title').textContent = t(pickerType === 'geoip' ? 'routingGeoIP' : 'routingGeoSite');
    const query = $('routing-picker-search').value.trim().toLocaleLowerCase();
    const list = $('routing-picker-list');
    const fragment = document.createDocumentFragment();
    const groups = new Map();
    const entries = [...(catalog[pickerType] || [])];
    const available = new Set(entries.map((item) => item.id));
    for (const id of new Set([...draft()[pickerType], ...pending])) {
      if (!available.has(id)) entries.push({ id, name: id, group: 'Unavailable' });
    }
    for (const item of entries) {
      const group = groupName(item.group || 'Other');
      if (!`${itemName(item, pickerType)} ${item.name} ${item.id} ${group}`.toLocaleLowerCase().includes(query)) continue;
      if (!groups.has(group)) groups.set(group, []);
      groups.get(group).push(item);
    }
    for (const [group, items] of groups) {
      const heading = document.createElement('h3');
      heading.className = 'routing-picker-group';
      heading.textContent = group;
      fragment.append(heading);
      for (const item of items) {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'routing-picker-option';
        button.setAttribute('aria-pressed', String(pending.has(item.id)));
        const name = document.createElement('span');
        name.className = 'routing-picker-name';
        name.textContent = itemName(item, pickerType);
        const code = document.createElement('span');
        code.className = 'routing-picker-code';
        code.textContent = item.id;
        const mark = document.createElement('span');
        mark.className = 'routing-picker-mark';
        mark.setAttribute('aria-hidden', 'true');
        mark.textContent = pending.has(item.id) ? '✓' : '';
        button.append(name, code, mark);
        button.addEventListener('click', () => {
          if (pending.has(item.id)) pending.delete(item.id);
          else pending.add(item.id);
          button.setAttribute('aria-pressed', String(pending.has(item.id)));
          mark.textContent = pending.has(item.id) ? '✓' : '';
          renderCount();
        });
        fragment.append(button);
      }
    }
    list.replaceChildren(fragment);
    if (!list.childElementCount) list.textContent = t(loading ? 'routingLoading' : loaded && (catalog[pickerType] || []).length ? 'routingNoResults' : 'routingSyncFirst');
    renderCount();
  }

  function renderCount() {
    $('routing-picker-count').textContent = t('routingSelectedCount', { count: pending.size });
  }

  function changed() {
    delete $('routing-save-hint').dataset.saved;
    $('routing-save-hint').textContent = t('routingNextConnection');
    $('routing-error').hidden = true;
  }

  $('routing-profile').addEventListener('change', () => {
    profileID = $('routing-profile').value;
    changed();
    render();
  });
  $('routing-mode').addEventListener('segment-change', (event) => {
    draft().mode = event.detail.value;
    changed();
    render();
  });
  $('routing-custom').addEventListener('input', () => { draft().custom = $('routing-custom').value; changed(); });
  $('routing-local').addEventListener('change', () => { draft().localDirect = $('routing-local').checked; changed(); });
  document.querySelectorAll('[data-routing-picker]').forEach((button) => button.addEventListener('click', () => {
    pickerType = button.dataset.routingPicker;
    pending = new Set(draft()[pickerType]);
    $('routing-picker-search').value = '';
    renderPicker();
    $('routing-picker').showModal();
    $('routing-picker-search').focus();
    loadCatalog();
  }));
  $('routing-picker-search').addEventListener('input', renderPicker);
  $('routing-picker').addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      $('routing-picker').close();
    }
  });
  $('routing-picker-form').addEventListener('submit', (event) => {
    event.preventDefault();
    draft()[pickerType] = [...pending];
    changed();
    $('routing-picker').close();
    render();
  });
  $('routing-sync').addEventListener('click', () => { $('routing-error').hidden = true; loadCatalog(true); });
  $('routing-auto').addEventListener('change', async () => {
    const enabled = $('routing-auto').checked;
    const previous = catalog.autoUpdate;
    catalog.autoUpdate = enabled;
    busy = true;
    render();
    try {
      catalog = await backend().SetRoutingAutoUpdate(enabled);
      await mutate('GetState');
    }
    catch (error) { catalog.autoUpdate = previous; showError(error, 'routing-error'); }
    finally { busy = false; render(); }
  });
  $('routing-save').addEventListener('click', async () => {
    const id = profileID;
    const current = draft();
    const settings = { ...current, geoip: [...current.geoip], geosite: [...current.geosite] };
    busy = true;
    $('routing-error').hidden = true;
    render();
    try {
      await mutate('SaveRouting', id, settings);
      $('routing-save-hint').dataset.saved = 'true';
      $('routing-save-hint').textContent = t('routingSaved');
    } catch (error) { showError(error, 'routing-error'); }
    finally { busy = false; render(); }
  });

  return { render, open: () => { loaded = false; render(); loadCatalog(); } };
}
