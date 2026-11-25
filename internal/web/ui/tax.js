// Round 9: estimated taxes and mileage log (Business page tabs).
'use strict';

async function estTab(el) {
  const y = S.taxYear || new Date().getFullYear();
  const e = await api('GET', '/api/tax/estimated?year=' + y);
  const set = await api('GET', '/api/trips?year=' + y).catch(() => ({ rate: '0.70' }));
  el.innerHTML = `<div class="card warn">A simple guide, not tax advice: it sets aside a percent of your business profit. Your real amount depends on all your income and deductions; check it with your tax preparer. Payments: irs.gov/payments (Direct Pay, choose "Estimated tax", form 1040-ES).</div>
    <div class="stats"><div class="stat"><small>Business profit ${y}</small><b>${money(e.profit_cents)}</b><small>after ${e.miles} mi mileage (${money(e.mileage_cents)})</small></div>
      <div class="stat"><small>Set aside (${e.rate_pct}%)</small><b>${money(e.set_aside_cents)}</b></div>
      <div class="stat"><small>Paid so far</small><b class="good">${money(e.paid_cents)}</b><small>category "Estimated tax payments"</small></div></div>
    <div class="card scroll"><table><tr><th>Quarter</th><th>Income through</th><th>Due</th><th class="num">Profit to date</th><th class="num">Set aside</th><th class="num">Paid by due</th><th class="num">Suggested payment</th></tr>
    ${e.quarters.map((q) => `<tr><td>${esc(q.name)}</td><td>${esc(fmtDate(q.through))}</td><td>${esc(fmtDate(q.due))}${q.past ? ' <span class="pill">past</span>' : ''}</td><td class="num">${money(q.profit_cents)}</td><td class="num">${money(q.set_aside_cents)}</td><td class="num">${money(q.paid_cents)}</td><td class="num"><b>${money(q.suggested_cents)}</b></td></tr>`).join('')}</table></div>
    <form class="card" id="etForm"><h2>Settings</h2><div class="form2">
      <div><label>Set aside this percent of business profit</label><input name="est_tax_rate" value="${esc(e.rate_pct)}" inputmode="decimal"></div>
      <div><label>Mileage rate (dollars per mile; check the IRS rate each January)</label><input name="mileage_rate" value="${esc(set.rate || '0.70')}" inputmode="decimal"></div>
      <div class="full"><label class="inline"><input type="checkbox" name="rem" checked> Remind admins 7 days and 1 day before each due date</label></div></div>
      <p class="mute">Record each payment you make to the IRS as a transaction with the category "Estimated tax payments".</p>
      <div class="actions"><button class="primary">Save</button></div></form>`;
  $('#etForm').onsubmit = async (ev) => {
    ev.preventDefault(); const d = formData(ev.target);
    try { await api('PUT', '/api/tax/settings', { est_tax_rate: d.est_tax_rate, mileage_rate: d.mileage_rate, est_tax_reminders: d.rem ? '1' : '0' }); toast('Saved'); estTab(el); } catch (err) { fail(err); }
  };
}

async function mileTab(el) {
  const y = S.taxYear || new Date().getFullYear();
  const r = await api('GET', '/api/trips?year=' + y);
  el.innerHTML = `<div class="spread"><div class="stats" style="margin:0"><div class="stat"><small>Business miles ${y}</small><b>${r.miles}</b></div><div class="stat"><small>Deduction at $${esc(r.rate)}/mi</small><b class="good">${money(r.deduction_cents)}</b></div></div><button class="primary" id="tpAdd">+ Trip</button></div>
    <p class="mute">Log each business drive (post office, supplies, client). The IRS wants the date, miles and business purpose. The total goes to line 9 of the tax summary.</p>
    <div class="card scroll"><table><tr><th>Date</th><th>Purpose</th><th>From / to</th><th class="num">Miles</th><th></th></tr>
    ${r.trips.map((t) => `<tr><td>${esc(fmtDate(t.date))}</td><td>${esc(t.purpose)}</td><td><small>${esc(t.from)}${t.to ? ' &rarr; ' + esc(t.to) : ''}</small></td><td class="num">${t.miles}</td><td><button class="small" data-tp="${t.id}">Edit</button></td></tr>`).join('') || '<tr><td colspan="5" class="empty">No trips yet.</td></tr>'}</table></div>`;
  $('#tpAdd').onclick = () => tripForm({ date: ymd(new Date()) }, () => mileTab(el));
  el.querySelectorAll('[data-tp]').forEach((b) => (b.onclick = () => tripForm(r.trips.find((t) => t.id === Number(b.dataset.tp)), () => mileTab(el))));
}
function tripForm(t, done) {
  const isNew = !t.id;
  openModal(`<form id="tpF"><h2>${isNew ? 'New trip' : 'Edit trip'}</h2><div class="form2">
    <div><label>Date</label><input type="date" name="date" value="${esc(t.date)}" required></div>
    <div><label>Miles (round trip)</label><input name="miles" inputmode="decimal" value="${esc(t.miles || '')}" required></div>
    <div class="full"><label>Business purpose</label><input name="purpose" value="${esc(t.purpose)}" required placeholder="Ship orders at the post office"></div>
    <div><label>From</label><input name="from" value="${esc(t.from)}"></div><div><label>To</label><input name="to" value="${esc(t.to)}"></div>
    <div class="full"><label>Vehicle</label><input name="vehicle" value="${esc(t.vehicle)}"></div></div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="tpDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#tpF', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try { await api(isNew ? 'POST' : 'PUT', '/api/trips' + (isNew ? '' : '/' + t.id), { ...d, miles: parseFloat(d.miles) || 0 }); closeModal(); done(); } catch (err) { fail(err); }
    };
    const del = $('#tpDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete this trip?')) return; await api('DELETE', '/api/trips/' + t.id).catch(fail); closeModal(); done(); };
  });
}

BIZ_TABS.push(['est', 'Estimated taxes', () => estTab], ['miles', 'Mileage', () => mileTab]);
