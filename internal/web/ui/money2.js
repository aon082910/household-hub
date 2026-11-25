// Round 10: shared costs (who owes whom), yearly costs, statement check.
'use strict';

async function sharedView(el) {
  const r = await api('GET', '/api/shared');
  const members = S.users.filter((u) => u.active);
  el.innerHTML = `<div class="spread"><h1>Shared costs</h1><div class="row"><button id="shPay">Record a pay-back</button><button class="primary" id="shAdd">+ Shared cost</button></div></div>
    <p class="mute">For costs one person pays for others (dinner, groceries, a trip). Balances add up over time; pay-backs even them out. This is separate from the bank accounts.</p>
    <div class="grid"><div class="card"><h2>Balances</h2><ul class="list">${r.balances.map((b) => `<li><span class="dot" style="background:${esc((userBy(b.user_id) || {}).color || '#888')}"></span><span class="grow">${esc(userName(b.user_id))}</span><b class="${b.cents > 0 ? 'good' : 'bad'}">${b.cents > 0 ? 'is owed ' : 'owes '}${money(Math.abs(b.cents))}</b></li>`).join('') || '<li class="empty">Everyone is even.</li>'}</ul></div>
    <div class="card"><h2>To settle up</h2><ul class="list">${r.payments.map((p) => `<li><span class="grow">${esc(userName(p.from))} pays ${esc(userName(p.to))}</span><b>${money(p.cents)}</b><button class="small" data-settle="${p.from}|${p.to}|${p.cents}">Paid</button></li>`).join('') || '<li class="empty">Nothing to pay.</li>'}</ul></div></div>
    <div class="card scroll"><table><tr><th>Date</th><th>What</th><th>Paid by</th><th>Split</th><th class="num">Amount</th></tr>
    ${r.costs.map((c) => `<tr class="click" data-sc="${c.id}"><td>${esc(fmtDate(c.date))}</td><td>${esc(c.description)} ${c.kind === 'settle' ? '<span class="pill good">pay-back</span>' : ''}</td><td>${esc(userName(c.paid_by))}</td><td><small>${(c.shares || []).map((s) => esc(userName(s.user_id)) + ' ' + money(s.share_cents)).join(', ')}</small></td><td class="num">${money(c.amount_cents)}</td></tr>`).join('') || '<tr><td colspan="5" class="empty">Nothing yet.</td></tr>'}</table></div>`;
  $('#shAdd').onclick = () => sharedForm({ date: ymd(new Date()), paid_by: S.me.id, kind: 'expense', shares: members.map((u) => ({ user_id: u.id })) }, members, () => sharedView(el));
  $('#shPay').onclick = () => sharedForm({ date: ymd(new Date()), paid_by: S.me.id, kind: 'settle', description: 'Paid back', shares: [] }, members, () => sharedView(el));
  el.querySelectorAll('[data-settle]').forEach((b) => (b.onclick = async () => {
    const [from, to, cents] = b.dataset.settle.split('|').map(Number);
    if (!confirm(`${userName(from)} paid ${userName(to)} ${money(cents)}?`)) return;
    try { await api('POST', '/api/shared', { date: ymd(new Date()), description: 'Paid back', kind: 'settle', amount_cents: cents, paid_by: from, shares: [{ user_id: to, share_cents: cents }] }); sharedView(el); } catch (e) { fail(e); }
  }));
  el.querySelectorAll('[data-sc]').forEach((tr) => (tr.onclick = () => sharedForm(r.costs.find((c) => c.id === Number(tr.dataset.sc)), members, () => sharedView(el))));
}

function sharedForm(c, members, done) {
  const isNew = !c.id; const settle = c.kind === 'settle';
  const inSplit = new Set((c.shares || []).map((s) => s.user_id));
  openModal(`<form id="scF"><h2>${settle ? 'Pay-back' : isNew ? 'New shared cost' : 'Edit shared cost'}</h2><div class="form2">
    <div class="full"><label>What</label><input name="description" value="${esc(c.description)}" required></div>
    <div><label>Amount</label><input name="amount" inputmode="decimal" value="${esc(centsInput(c.amount_cents))}" required></div>
    <div><label>Date</label><input type="date" name="date" value="${esc(c.date)}"></div>
    <div><label>${settle ? 'Who paid' : 'Paid by'}</label><select name="paid_by">${opts(members.map((u) => [u.id, u.display_name]), c.paid_by)}</select></div>
    ${settle ? `<div><label>Paid to</label><select name="to">${opts(members.map((u) => [u.id, u.display_name]), (c.shares[0] || {}).user_id)}</select></div>`
      : `<div class="full"><label>Split evenly between</label>${members.map((u) => `<label class="inline"><input type="checkbox" data-group="who" name="who" value="${u.id}" ${inSplit.has(u.id) ? 'checked' : ''}>${esc(u.display_name)}</label>`).join('')}</div>`}</div>
    <div class="actions">${isNew ? '' : '<button type="button" class="danger left" id="scDel">Delete</button>'}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#scF', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try {
        const amt = Math.abs(toCents(d.amount));
        let shares;
        if (settle) shares = [{ user_id: Number(d.to), share_cents: amt }];
        else {
          const who = d.who.map(Number);
          if (!who.length) throw new Error('Pick who shares it');
          const base = Math.floor(amt / who.length); let rest = amt - base * who.length;
          shares = who.map((u) => ({ user_id: u, share_cents: base + (rest-- > 0 ? 1 : 0) }));
        }
        await api(isNew ? 'POST' : 'PUT', '/api/shared' + (isNew ? '' : '/' + c.id), { description: d.description, amount_cents: amt, date: d.date, paid_by: Number(d.paid_by), kind: c.kind, shares });
        closeModal(); done();
      } catch (err) { fail(err); }
    };
    const del = $('#scDel', sh);
    if (del) del.onclick = async () => { if (!confirm('Delete this?')) return; await api('DELETE', '/api/shared/' + c.id).catch(fail); closeModal(); done(); };
  });
}

// ---- Plan > Yearly costs ----
async function annualTab(el) {
  const r = await api('GET', '/api/annual');
  const months = [...Array(12).keys()].map((i) => [i + 1, new Date(2000, i, 1).toLocaleDateString([], { month: 'long' })]);
  el.innerHTML = `<div class="spread"><div class="stats" style="margin:0"><div class="stat"><small>Put aside each month</small><b>${money(r.per_month_cents)}</b><small>covers every yearly cost below</small></div></div><button class="primary" id="anAdd">+ Yearly cost</button></div>
    <p class="mute">Costs that come once a year: registrations, memberships, property tax, insurance paid yearly. Tip: make a savings goal with the monthly amount above.</p>
    <div class="card"><ul class="list">${r.costs.map((a) => `<li class="click" data-an="${a.id}"><span class="grow"><b>${esc(a.name)}</b><br><small>${esc(months[a.month - 1][1])}${a.months_away === 0 ? ' (this month)' : ` (in ${a.months_away} month${a.months_away > 1 ? 's' : ''})`}</small></span><b>${money(a.amount_cents)}</b></li>`).join('') || '<li class="empty">None yet.</li>'}</ul></div>`;
  const form = (a) => openModal(`<form id="anF"><h2>${a.id ? 'Edit' : 'New'} yearly cost</h2><label>Name</label><input name="name" value="${esc(a.name)}" required>
    <label>Amount</label><input name="amount" inputmode="decimal" value="${esc(centsInput(a.amount_cents))}" required><label>Month it is due</label><select name="month">${opts(months, a.month || new Date().getMonth() + 1)}</select>
    <label>Category</label><select name="category_id">${opts(S.cats.map((c) => [c.id, c.name]), a.category_id, '-')}</select><label class="inline"><input type="checkbox" name="private" ${a.private ? 'checked' : ''}> Private</label>
    <div class="actions">${a.id ? '<button type="button" class="danger left" id="anDel">Delete</button>' : ''}<button type="button" data-action="close">Cancel</button><button class="primary">Save</button></div></form>`, (sh) => {
    $('#anF', sh).onsubmit = async (e) => { e.preventDefault(); const d = formData(e.target); try { await api(a.id ? 'PUT' : 'POST', '/api/annual' + (a.id ? '/' + a.id : ''), { name: d.name, amount_cents: Math.abs(toCents(d.amount)), month: Number(d.month), category_id: idOrNull(d.category_id), private: d.private }); closeModal(); annualTab(el); } catch (err) { fail(err); } };
    const del = $('#anDel', sh);
    if (del) del.onclick = async () => { await api('DELETE', '/api/annual/' + a.id).catch(fail); closeModal(); annualTab(el); };
  });
  $('#anAdd').onclick = () => form({});
  el.querySelectorAll('[data-an]').forEach((li) => (li.onclick = () => form(r.costs.find((x) => x.id === Number(li.dataset.an)))));
}

// ---- statement check (from the account form) ----
function statementCheck(a) {
  openModal(`<form id="stF"><h2>Check ${esc(a.name)} against a statement</h2><p class="mute">Type the closing balance and date from your bank or PayPal statement. The app compares it with its own balance on that date.</p>
    <div class="form2"><div><label>Statement date</label><input type="date" name="date" value="${ymd(new Date())}" required></div><div><label>Closing balance on the statement</label><input name="bank" inputmode="decimal" required></div></div>
    <div class="actions"><button type="button" data-action="close">Close</button><button class="primary">Check</button></div><div id="stOut"></div></form>`, (sh) => {
    $('#stF', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try {
        const r = await api('POST', `/api/accounts/${a.id}/check`, { date: d.date, bank_cents: toCents(d.bank), save: true });
        const row = (t) => `<li><span class="grow">${esc(t.payee)}<br><small>${esc(fmtDate(t.date))}</small></span><b>${money(t.amount_cents, true)}</b></li>`;
        $('#stOut', sh).innerHTML = r.diff_cents === 0 ? '<p class="good"><b>It matches.</b></p>' : `<p class="bad"><b>Off by ${money(r.diff_cents, true)}</b> (bank ${money(r.bank_cents)}, app ${money(r.app_cents)}).</p>
          ${r.could_explain.length ? `<h3>A transaction of exactly that amount</h3><ul class="list">${r.could_explain.map(row).join('')}</ul><p class="mute">Is it missing on the statement, recorded twice, or entered with the wrong sign?</p>` : ''}
          ${r.possible_duplicates.length ? `<h3>Possible doubles</h3><ul class="list">${r.possible_duplicates.map(row).join('')}</ul>` : ''}
          ${!r.could_explain.length && !r.possible_duplicates.length ? '<p class="mute">No single cause found: check for a missing transaction (import the statement), or the opening balance.</p>' : ''}`;
      } catch (err) { fail(err); }
    };
  });
}

EXTRA_VIEWS.shared = sharedView;
FIN_VIEWS.push('shared');
PLAN_TABS.push(['annual', 'Yearly costs', () => annualTab]);
