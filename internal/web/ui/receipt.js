// Round 11: receipt reader. Take a photo of a receipt; the server reads the store, date and total, the form is
// filled in, and the photo is attached to the new transaction.
'use strict';

let receiptOK = null;
async function moneyExtras(el) {
  if (receiptOK === null) receiptOK = await api('GET', '/api/receipts/status').then((r) => r.available).catch(() => false);
  if (!receiptOK) return;
  const add = $('#txAdd', el);
  if (!add || $('#rcScan', el)) return;
  add.insertAdjacentHTML('beforebegin', '<label class="btn" id="rcScan" title="Photo of a receipt">&#128247; Scan receipt<input type="file" accept="image/*,application/pdf" capture="environment" hidden></label>');
  $('#rcScan input', el).onchange = (e) => { const f = e.target.files[0]; e.target.value = ''; if (f) scanReceipt(f); };
}

async function scanReceipt(file) {
  toast('Reading the receipt...');
  const fd = new FormData(); fd.append('file', file);
  let g;
  try {
    const r = await fetch('/api/receipts/read', { method: 'POST', headers: { 'X-HHub': '1' }, body: fd });
    g = await r.json();
    if (!r.ok) throw new Error(g.error || 'could not read it');
  } catch (e) { fail(e); g = { merchant: '', date: '', total_cents: 0 }; }
  openModal(`<form id="rcF"><h2>From the receipt</h2>
    <p class="mute">${g.confident ? 'Check what was read, then save.' : 'Some parts could not be read; fill them in.'} The photo is attached to the transaction.</p>
    <div class="form2"><div><label>Store</label><input name="payee" value="${esc(g.merchant)}" required></div>
    <div><label>Total</label><input name="amount" inputmode="decimal" value="${esc(centsInput(g.total_cents))}" required></div>
    <div><label>Date</label><input type="date" name="date" value="${esc(g.date || ymd(new Date()))}" required></div>
    <div><label>Account</label><select name="account_id">${opts(S.accounts.filter((a) => !a.archived).map((a) => [a.id, a.name]), (S.accounts[0] || {}).id)}</select></div>
    <div><label>Category</label><select name="category_id">${opts(S.cats.map((c) => [c.id, c.name]), '', '(learn from store)')}</select></div>
    <div><label>&nbsp;</label><label class="inline"><input type="checkbox" name="business"> Business</label></div></div>
    ${g.text ? `<details><summary>What the reader saw</summary><pre style="white-space:pre-wrap;max-height:30vh;overflow:auto">${esc(g.text)}</pre></details>` : ''}
    <p class="mute">If the bank or card import later brings the same charge, it is matched to this one instead of added twice.</p>
    <div class="actions"><button type="button" data-action="close">Cancel</button><button class="primary">Save with photo</button></div></form>`, (sh) => {
    $('#rcF', sh).onsubmit = async (e) => {
      e.preventDefault(); const d = formData(e.target);
      try {
        const t = await api('POST', '/api/transactions', { date: d.date, amount_cents: -Math.abs(toCents(d.amount)), payee: d.payee, account_id: idOrNull(d.account_id), category_id: idOrNull(d.category_id), business: d.business, notes: 'from receipt' });
        const f2 = new FormData(); f2.append('file', file);
        await fetch(`/api/transactions/${t.id}/receipts`, { method: 'POST', headers: { 'X-HHub': '1' }, body: f2 });
        closeModal(); toast('Saved with the receipt'); render();
      } catch (err) { fail(err); }
    };
  });
}
