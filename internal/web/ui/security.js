// Round 5: two-step sign-in, change log, reverse-proxy setting. Loaded after app.js.
'use strict';

function askLoginCode(ticket) {
  const a = $('#auth');
  a.innerHTML = `<form class="card" id="codeForm"><img src="icon.svg" alt=""><h1>One more step</h1>
    <p class="mute">Type the 6-digit code from your authenticator app, or one of your recovery codes.</p>
    <input name="code" inputmode="numeric" autocomplete="one-time-code" autofocus required placeholder="123456">
    <div class="err" id="authErr"></div><button class="primary" style="width:100%;margin-top:.6rem">Sign in</button>
    <p style="text-align:center"><button type="button" class="link" id="codeBack">Back</button></p></form>`;
  $('#codeBack').onclick = () => showAuth(false);
  $('#codeForm').onsubmit = async (e) => {
    e.preventDefault();
    try { await api('POST', '/api/login/code', { Ticket: ticket, Code: e.target.code.value }); boot(); }
    catch (err) { $('#authErr').textContent = err.message; }
  };
}

async function setSecurity(el) {
  const st = await api('GET', '/api/me/2fa');
  el.innerHTML = `<div class="grid"><div class="card"><h2>Two-step sign-in</h2>
    <p>Status: <b class="${st.on ? 'good' : ''}">${st.on ? 'On' : 'Off'}</b></p>
    <p class="mute">With it on, signing in needs your password and a code from an authenticator app (Google Authenticator, Microsoft Authenticator, 1Password, Authy). Strongly suggested if this app can be reached from outside your home.</p>
    <div id="tsBody">${st.on ? '<form id="tsOff"><label>Your password, to turn it off</label><input type="password" name="password" required autocomplete="current-password"><button class="danger" style="margin-top:.5rem">Turn off</button></form>' : '<button class="primary" id="tsStart">Turn on</button>'}</div></div></div>`;
  const start = $('#tsStart');
  if (start) start.onclick = async () => {
    try {
      const s = await api('POST', '/api/me/2fa/start');
      $('#tsBody').innerHTML = `<p>1. Scan this with your authenticator app${s.qr ? '' : ' (or type the key)'}:</p>${s.qr ? `<img src="${esc(s.qr)}" alt="QR code" style="width:200px;background:#fff;padding:8px;border-radius:8px">` : ''}
        <p class="mute">Key: <code>${esc(s.secret.replace(/(.{4})/g, '$1 ').trim())}</code></p>
        <form id="tsOn"><label>2. Type the 6-digit code it shows</label><input name="code" inputmode="numeric" required autocomplete="one-time-code"><button class="primary" style="margin-top:.5rem">Turn on</button></form>`;
      $('#tsOn').onsubmit = async (e) => {
        e.preventDefault();
        try {
          const r = await api('POST', '/api/me/2fa/enable', { Code: e.target.code.value });
          $('#tsBody').innerHTML = `<p class="good"><b>Two-step sign-in is on.</b></p><p>Save these recovery codes somewhere safe (a password manager or printed). Each works once if you lose your phone. They are not shown again.</p>
            <pre style="font-size:1.05rem">${r.recovery_codes.map(esc).join('\n')}</pre><button id="tsDone" class="primary">I saved them</button>`;
          $('#tsDone').onclick = () => setSecurity(el);
        } catch (err) { fail(err); }
      };
    } catch (err) { fail(err); }
  };
  const off = $('#tsOff');
  if (off) off.onsubmit = async (e) => {
    e.preventDefault();
    if (!confirm('Turn off two-step sign-in?')) return;
    try { await api('POST', '/api/me/2fa/disable', { Password: e.target.password.value }); toast('Turned off'); setSecurity(el); } catch (err) { fail(err); }
  };
}

async function setAudit(el) {
  const list = await api('GET', '/api/audit' + (S.auditBefore ? '?before=' + S.auditBefore : ''));
  const nice = (a) => a.replace(/^(POST|PUT|DELETE) \/api\//, (m, v) => ({ POST: 'add/do ', PUT: 'change ', DELETE: 'delete ' }[v])).replace(/\{id\}/g, '#');
  el.innerHTML = `<div class="card"><h2>Change log</h2><p class="mute">Who changed what and when, kept for a year. It lists the action and item number, never the data itself (no passwords, amounts or notes).</p>
    <div class="scroll"><table><tr><th>When</th><th>Who</th><th>What</th><th>Item</th><th>From</th></tr>
    ${list.map((e) => `<tr><td><small>${esc(new Date(e.at).toLocaleString())}</small></td><td>${e.user_id ? esc(userName(e.user_id)) : '<span class="bad">unknown</span>'}</td><td>${esc(nice(e.action))}</td><td>${esc(e.item)}</td><td><small>${esc(e.ip)}</small></td></tr>`).join('') || '<tr><td colspan="5" class="empty">Nothing yet.</td></tr>'}</table></div>
    <div class="actions">${S.auditBefore ? '<button id="auNew">Newest</button>' : ''}${list.length === 200 ? '<button id="auMore">Older</button>' : ''}</div></div>`;
  const more = $('#auMore');
  if (more) more.onclick = () => { S.auditBefore = list[list.length - 1].id; setAudit(el); };
  const nw = $('#auNew');
  if (nw) nw.onclick = () => { S.auditBefore = 0; setAudit(el); };
}

async function setAccess(el) {
  const s = await api('GET', '/api/settings');
  el.innerHTML = `<div class="grid"><form class="card" id="pxForm"><h2>Reverse proxy</h2>
    <p class="mute">Only needed if Household Hub sits behind HTTPS (Nginx Proxy Manager, SWAG, Caddy, Tailscale Serve). Put the proxy's address here so the app sees each visitor's real address (for the sign-in lock) and knows the page is HTTPS. Leave empty otherwise.</p>
    <label>Trusted proxy addresses (comma separated, IPs or ranges like 172.17.0.0/16)</label><input name="trusted_proxies" value="${esc(s.trusted_proxies)}" placeholder="172.18.0.5">
    <button class="primary" style="margin-top:.6rem">Save</button></form>
    <div class="card"><h2>Using it from outside home, with HTTPS</h2>
    <p>HTTPS is needed for browser pop-ups on phones and before using the app away from home. Easiest choices on Unraid:</p>
    <ol><li><b>Tailscale</b> (you already use it): install Tailscale on Unraid and your phones; open <code>http://&lt;server's Tailscale name&gt;:8095</code>. Private, nothing open to the internet. For HTTPS run <code>tailscale serve --bg --https=443 http://localhost:8095</code> on the server and use the https://...ts.net address.</li>
    <li><b>Nginx Proxy Manager</b> app: add a proxy host to <code>192.168.1.144:8095</code> with a Let's Encrypt certificate, then put NPM's container address in the box on the left. Turn on two-step sign-in for everyone first.</li></ol>
    <p class="mute">Never forward port 8095 straight from your router to the internet.</p></div></div>`;
  $('#pxForm').onsubmit = async (e) => { e.preventDefault(); try { await api('PUT', '/api/settings', formData(e.target)); toast('Saved'); } catch (err) { fail(err); } };
}

SETTINGS_TABS.push(['security', 'Sign-in security', setSecurity, false]);
SETTINGS_TABS.push(['audit', 'Change log', setAudit, true]);
SETTINGS_TABS.push(['access', 'Outside access', setAccess, true]);
