// Round 8: "Windows app" settings tab (only inside the Windows app) and phone install (service worker).
'use strict';

async function setWinApp(el) {
  el.innerHTML = '<div class="card"><h2>Windows app</h2><p class="mute">Checking for a newer version...</p></div>';
  let v;
  try { v = JSON.parse(await window.hhubVersion()); } catch (e) { el.innerHTML = `<div class="card err">${esc(e.message || e)}</div>`; return; }
  el.innerHTML = `<div class="card"><h2>Windows app</h2><p>This app: <b>${esc(v.current)}</b>${v.latest ? ` &middot; newest: <b>${esc(v.latest)}</b>` : ''}</p>
    ${v.error ? `<p class="warn">${esc(v.error)}</p>` : v.newer ? `<p class="good">An update is ready.</p><pre style="white-space:pre-wrap">${esc(v.notes || '')}</pre><button class="primary" id="wuGo">Install update</button>
      <p class="mute">The app closes, copies the new version and opens again. Windows Smart App Control may ask about the new files once.</p>` : '<p class="good">Up to date.</p>'}
    <div class="actions"><button data-action="switch-server">Change server</button></div></div>`;
  const go = $('#wuGo');
  if (go) go.onclick = async () => {
    go.disabled = true; go.textContent = 'Downloading...';
    try { await window.hhubInstallUpdate(); go.textContent = 'Restarting...'; } catch (e) { fail(new Error(String(e.message || e))); go.disabled = false; go.textContent = 'Install update'; }
  };
}
if (typeof window.hhubVersion === 'function') SETTINGS_TABS.push(['winapp', 'Windows app', setWinApp, false]);

// phone install: a service worker keeps the app's own files so it opens like an app (needs https or localhost)
if ('serviceWorker' in navigator && window.isSecureContext && !inWindowApp()) {
  navigator.serviceWorker.register('/sw.js').catch(() => {});
}
