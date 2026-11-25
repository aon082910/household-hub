// Round 1: Plan page (forecast, subscriptions, income) and automatic backups. Loaded after app.js.
'use strict';

const PERIOD_TEXT = { weekly: 'Weekly', biweekly: 'Every 2 weeks', monthly: 'Monthly', quarterly: 'Every 3 months', yearly: 'Yearly' };

// small inline SVG line chart: series = [{name, color, points:[{date, balance_cents}]}]
function lineChart(series, floor) {
  const W = 640, H = 200, P = 34;
  const all = series.flatMap((s) => s.points.map((p) => p.balance_cents));
  if (floor != null) all.push(floor);
  if (!all.length) return '';
  let lo = Math.min(0, ...all), hi = Math.max(...all);
  if (hi === lo) hi = lo + 100;
  const n = Math.max(...series.map((s) => s.points.length));
  const x = (i) => P + (i * (W - P - 8)) / Math.max(1, n - 1);
  const y = (v) => H - 22 - ((v - lo) * (H - 40)) / (hi - lo);
  let svg = `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="Balance forecast" style="width:100%;height:auto">`;
  svg += `<line x1="${P}" x2="${W - 8}" y1="${y(0)}" y2="${y(0)}" stroke="var(--line)"/>`;
  if (floor != null) svg += `<line x1="${P}" x2="${W - 8}" y1="${y(floor)}" y2="${y(floor)}" stroke="var(--warn)" stroke-dasharray="4 4"/>`;
  svg += `<text x="2" y="${y(hi) + 4}" font-size="10" fill="var(--mute)">${esc(money(hi).replace(/\.\d\d$/, ''))}</text><text x="2" y="${y(lo) + 4}" font-size="10" fill="var(--mute)">${esc(money(lo).replace(/\.\d\d$/, ''))}</text>`;
  for (const s of series) {
    const d = s.points.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(p.balance_cents).toFixed(1)}`).join('');
    svg += `<path d="${d}" fill="none" stroke="${esc(s.color)}" stroke-width="2"/>`;
  }
  const first = series[0] && series[0].points[0], last = series[0] && series[0].points[series[0].points.length - 1];
  if (first) svg += `<text x="${P}" y="${H - 4}" font-size="10" fill="var(--mute)">${esc(fmtDate(first.date, { month: 'short', day: 'numeric' }))}</text>`;
  if (last) svg += `<text x="${W - 8}" y="${H - 4}" font-size="10" fill="var(--mute)" text-anchor="end">${esc(fmtDate(last.date, { month: 'short', day: 'numeric' }))}</text>`;
  return svg + '</svg>';
}
const COLORS = ['#4f7cff', '#2bb673', '#ff8a3d', '#e2558b', '#8a63d2', '#14a3a3'];

async function planView(el) {
  const tab = S.planTab || 'forecast';
  el.innerHTML = `<h1>Plan</h1><div class="tabs">${[['forecast', 'Forecast'], ['subs', 'Subscriptions'], ['income', 'Income']].map(([k, t]) => `<button data-ptab="${k}" class="${k === tab ? 'on' : ''}">${t}</button>`).join('')}</div><div id="planBody"></div>`;
  el.querySelectorAll('[data-ptab]').forEach((b) => (b.onclick = () => { S.planTab = b.dataset.ptab; planView(el); }));
  const body = $('#planBody');
  await ({ forecast: forecastTab, subs: subsTab, income: incomeTab }[tab])(body);
}

async function forecastTab(el) {
  const days = S.fcDays || 60;
  const r = await api('GET', '/api/forecast?days=' + days);
  const accs = r.accounts;
  const totalSafe = accs.reduce((s, a) => s + a.safe_to_spend_cents, 0);
  el.innerHTML = `<div class="spread"><p class="mute" style="margin:0">Expected balances from unpaid bills, your income schedules and found subscriptions. Nothing here is recorded.</p>
    <select id="fcDays" style="width:auto">${opts([[30, 'Next 30 days'], [60, 'Next 60 days'], [90, 'Next 90 days'], [180, 'Next 6 months']], days)}</select></div>
    ${r.notes.map((n) => `<div class="card warn">${esc(n)}</div>`).join('')}
    <div class="stats"><div class="stat"><small>Safe to spend (all accounts)</small><b class="good">${money(totalSafe)}</b></div>
    ${accs.filter((a) => a.warning).map((a) => `<div class="stat"><small>${esc(a.name)} low point</small><b class="bad">${money(a.lowest_cents)}</b><small>${esc(fmtDate(a.lowest_date))}</small></div>`).join('')}</div>
    <div class="card">${lineChart(accs.map((a, i) => ({ name: a.name, color: COLORS[i % COLORS.length], points: a.points })), null)}
      <div class="row">${accs.map((a, i) => `<span><span class="dot" style="background:${COLORS[i % COLORS.length]}"></span> ${esc(a.name)}</span>`).join('')}</div></div>
    <div class="grid">${accs.map((a) => `<div class="card"><div class="spread"><h2>${esc(a.name)}</h2>${a.warning ? '<span class="pill bad">below warning</span>' : ''}</div>
      <div class="stats"><div class="stat"><small>Now</small><b>${money(a.start_cents)}</b></div><div class="stat"><small>Lowest</small><b class="${a.lowest_cents < 0 ? 'bad' : ''}">${money(a.lowest_cents)}</b><small>${esc(fmtDate(a.lowest_date))}</small></div>
      <div class="stat"><small>Safe to spend</small><b>${money(a.safe_to_spend_cents)}</b></div></div>
      ${a.low_balance_cents != null ? `<small>Warning level ${money(a.low_balance_cents)} (change it on the Accounts page)</small>` : '<small>No warning level set (Accounts page)</small>'}
      <ul class="list">${a.events.slice(0, 40).map((e) => `<li><span class="grow">${esc(e.name)} <small>${esc(e.kind)}</small><br><small>${esc(fmtDate(e.date))}</small></span><span class="num ${e.amount_cents > 0 ? 'good' : ''}">${money(e.amount_cents, true)}</span><small class="num">${money(e.balance_cents)}</small></li>`).join('') || '<li class="empty">Nothing expected.</li>'}</ul></div>`).join('')}</div>`;
  $('#fcDays').onchange = (e) => { S.fcDays = Number(e.target.value); forecastTab(el); };
}

async function subsTab(el) {
  const subs = await api('GET', '/api/subscriptions');
  const act = subs.filter((s) => s.active && s.status !== 'ignored');
  const monthly = act.reduce((a, s) => a + s.monthly_cents, 0);
  el.innerHTML = `<div class="stats"><div class="stat"><small>Active subscriptions</small><b>${act.length}</b></div><div class="stat"><small>Per month</small><b>${money(monthly)}</b></div><div class="stat"><small>Per year</small><b>${money(act.reduce((a, s) => a + s.yearly_cents, 0))}</b></div></div>
    <div class="card"><p class="mute">Found from charges that repeat on a schedule (imports, emails and entries). It needs at least 3 charges for monthly ones, so it fills in as your statements come in. Price changes send an alert.</p>
    <div class="scroll"><table><tr><th>Name</th><th>How often</th><th class="num">Amount</th><th class="num">Per year</th><th>Last / next</th><th></th></tr>
    ${subs.map((s) => `<tr class="${s.status === 'ignored' || !s.active ? 'mute' : ''}"><td><b>${esc(s.name)}</b>${s.old_cents ? ` <span class="pill warn">was ${money(s.old_cents)}</span>` : ''}${!s.active ? ' <span class="pill">stopped?</span>' : ''}${s.status === 'ignored' ? ' <span class="pill">ignored</span>' : ''}<br><small>${esc(accName(s.account_id))} &middot; ${s.count} charges</small></td>
      <td>${esc(PERIOD_TEXT[s.period])}</td><td class="num">${money(s.amount_cents)}</td><td class="num">${money(s.yearly_cents)}</td><td><small>${esc(fmtDate(s.last_date))}<br>next ~${esc(fmtDate(s.next_date))}</small></td>
      <td>${s.bill_id ? '<span class="pill good">bill</span>' : `<button class="small" data-sbill="${esc(s.key)}">Make bill</button>`} <button class="small" data-sign="${esc(s.key)}" data-st="${s.status === 'ignored' ? '' : 'ignored'}">${s.status === 'ignored' ? 'Unignore' : 'Ignore'}</button></td></tr>`).join('') || '<tr><td colspan="6" class="empty">None found yet. Import a few months of statements.</td></tr>'}</table></div></div>`;
  el.querySelectorAll('[data-sbill]').forEach((b) => (b.onclick = async () => { try { await api('POST', '/api/subscriptions/bill', { key: b.dataset.sbill }); toast('Bill made (autopay on, reminder 1 day before)'); subsTab(el); } catch (e) { fail(e); } }));
  el.querySelectorAll('[data-sign]').forEach((b) => (b.onclick = async () => { try { await api('POST', '/api/subscriptions/state', { key: b.dataset.sign, status: b.dataset.st }); subsTab(el); } catch (e) { fail(e); } }));
}

async function incomeTab(el) {
  const list = await api('GET', '/api/incomes');
  el.innerHTML = `<div class="card"><div class="spread"><h2>Income schedules</h2><button class="primary" id="incAdd">+ Income</button></div>
    <p class="mute">Paychecks, VA payments and other money that arrives on a schedule. Used only for the forecast; the real deposit still comes from your statements or by hand.</p>
    <ul class="list">${list.map((i) => `<li class="click" data-inc="${i.id}"><span class="grow"><b>${esc(i.name)}</b> ${i.active ? '' : '<span class="pill">paused</span>'}<br><small>${esc(freqText(i))} &middot; next from ${esc(fmtDate(i.start_date))} &middot; ${esc(accName(i.account_id) || 'default account')}</small></span><b class="good">${money(i.amount_cents)}</b></li>`).join('') || '<li class="empty">None yet.</li>'}</ul></div>`;
  $('#incAdd').onclick = () => incomeForm({ freq: 'monthly', interval: 1, active: true }, () => incomeTab(el));
  el.querySelectorAll('[data-inc]').forEach((r) => (r.onclick = () => incomeForm(list.find((x) => x.id === Number(r.dataset.inc)), () => incomeTab(el))));
}

function incomeForm(i, done) {
  const isNew = !i.id;
  openModal(`<form id="incForm"><h2>${isNew ? 'New income' : 'Edit income'}</h2><div class="form2">
    <div class="full"><label>Name</label><input name="name" value="${esc(i.name)}" required placeholder="VA disability, paycheck..."></div>
    <div><label>Amount</label><input name="amount" inputmode="decimal" value="${esc(centsInput(i.amount_cents))}" required></div>
    <div><label>Next date</label><input type="date" name="start_date" value="${esc(i.start_date || ymd(new Date()))}" required></div>
    <div><label>Repeats</label><select name="freq">${freqOpts(i.freq, true)}</select></div>
    <div><label>Every</label><input type="number" min="1" name="interval" value="${esc(i.interval || 1)}"></div>
    <div><label>Into account</label><select name="account_id">${opts(S.accounts.map((a) => [a.id, a.name]), i.account_id, '(default account)')}</select></div>
    <div><label>Ends (optional)</label><input type="date" name="until" value="${esc(i.until)}"></div>
    <div class="full"><label class="inline"><input type="checkbox" name="private" ${i.private ? 'checked' : ''}> Private (only me)</label>${isNew ? '' : `<label class="inline"><input type="checkbox" name="active" ${i.active ? 'checked' : ''}> Active</label>`}</div></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="incDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#incForm', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try {
        await api(isNew ? 'POST' : 'PUT', '/api/incomes' + (isNew ? '' : '/' + i.id), { name: d.name, amount_cents: Math.abs(toCents(d.amount)), start_date: d.start_date, freq: d.freq, interval: Number(d.interval) || 1, until: d.until, account_id: idOrNull(d.account_id), private: d.private, active: isNew ? true : d.active });
        closeModal(); toast('Saved'); done();
      } catch (err) { fail(err); }
    };
    const del = $('#incDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete "' + i.name + '"?')) return; try { await api('DELETE', '/api/incomes/' + i.id); closeModal(); done(); } catch (err) { fail(err); } };
  });
}

// Settings > Backup: automatic daily backups
async function setBackupAuto(el) {
  const r = await api('GET', '/api/backups');
  el.innerHTML = `<div class="grid"><div class="card"><h2>Automatic backups</h2>
    <p>Every day after ${esc(r.hour)}:00 a full copy is saved in <code>${esc(r.dir)}</code>, keeping the newest ${esc(r.keep)}. Receipts and documents are copied too.</p>
    <p class="mute">This protects against mistakes and a damaged database. It is on the same disk as the app, so also keep your normal appdata backup (or copy this folder elsewhere) for a dead disk.</p>
    <form id="bkForm" class="form2"><div><label>Hour of day (0-23)</label><input name="backup_hour" type="number" min="0" max="23" value="${esc(r.hour)}"></div>
    <div><label>Copies to keep (0 = off)</label><input name="backup_keep" type="number" min="0" max="365" value="${esc(r.keep)}"></div>
    <div class="full actions"><button type="button" id="bkNow">Back up now</button><button class="primary">Save</button></div></form></div>
    <div class="card"><h2>Saved copies</h2><ul class="list">${r.backups.map((b) => `<li><span class="grow">${esc(b.name)}<br><small>${(b.size / 1048576).toFixed(1)} MB</small></span><a class="btn small" href="/api/backups/${encodeURIComponent(b.name)}" download>Download</a></li>`).join('') || '<li class="empty">None yet.</li>'}</ul>
    <a class="btn" href="/api/backup" download>Download a fresh copy</a></div>
    <div class="card"><h3>Restore</h3><p class="mute">Stop the app, replace <code>household.db</code> in the data folder with a backup file (delete any household.db-wal and -shm files next to it), then start the app.</p></div></div>`;
  $('#bkForm').onsubmit = async (e) => { e.preventDefault(); try { await api('PUT', '/api/settings', formData(e.target)); toast('Saved'); setBackupAuto(el); } catch (err) { fail(err); } };
  $('#bkNow').onclick = async () => { try { const x = await api('POST', '/api/backups/run'); toast('Saved ' + x.name); setBackupAuto(el); } catch (err) { fail(err); } };
}

EXTRA_VIEWS.plan = planView;
FIN_VIEWS.push('plan');
