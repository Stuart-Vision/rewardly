import { useEffect, useMemo, useState } from 'react';
import { api, ApiError, describe, money, type Customer, type Order, type Quote, type WalletVoucher } from '../api';

const MENU = [
  { id: 'laksa', name: 'Laksa', cents: 850 },
  { id: 'chicken-rice', name: 'Chicken rice', cents: 650 },
  { id: 'nasi-lemak', name: 'Nasi lemak', cents: 750 },
  { id: 'satay', name: 'Satay (10 sticks)', cents: 1200 },
  { id: 'teh-tarik', name: 'Teh tarik', cents: 250 },
];

interface Props { version: number; onChange: () => void }

export default function Checkout({ version, onChange }: Props) {
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [customerId, setCustomerId] = useState<number | null>(null);
  const [cart, setCart] = useState<Record<string, number>>({ laksa: 2, 'teh-tarik': 2 });
  const [code, setCode] = useState('');
  const [quote, setQuote] = useState<Quote | null>(null);
  const [message, setMessage] = useState<{ kind: 'error' | 'success'; text: string } | null>(null);
  const [wallet, setWallet] = useState<WalletVoucher[]>([]);
  const [orders, setOrders] = useState<Order[]>([]);
  const [busy, setBusy] = useState(false);
  // One key per checkout attempt. It is reused if the request is retried and
  // replaced only after the order succeeds, so a retry can never double-charge.
  const [idempotencyKey, setIdempotencyKey] = useState(() => crypto.randomUUID());

  useEffect(() => {
    api.customers().then((list) => {
      setCustomers(list);
      setCustomerId((id) => id ?? list[0]?.id ?? null);
    }).catch(() => {});
  }, [version]);

  useEffect(() => {
    if (customerId == null) return;
    api.wallet(customerId).then(setWallet).catch(() => setWallet([]));
    api.orders(customerId).then(setOrders).catch(() => setOrders([]));
  }, [customerId, version]);

  const subtotal = useMemo(
    () => MENU.reduce((sum, item) => sum + item.cents * (cart[item.id] ?? 0), 0),
    [cart],
  );

  // A quote is only valid for the cart and customer it was made for.
  useEffect(() => { setQuote(null); }, [subtotal, customerId]);

  const setQty = (id: string, delta: number) =>
    setCart((c) => ({ ...c, [id]: Math.max(0, Math.min(20, (c[id] ?? 0) + delta)) }));

  const showError = (e: unknown) =>
    setMessage({ kind: 'error', text: e instanceof ApiError ? e.message : 'Network error, please try again' });

  async function applyCode(value = code) {
    if (!customerId || !value.trim()) return;
    setCode(value);
    setMessage(null);
    try {
      setQuote(await api.quote(customerId, value, subtotal));
    } catch (e) {
      setQuote(null);
      showError(e);
    }
  }

  async function placeOrder() {
    if (!customerId || subtotal === 0) return;
    setBusy(true);
    setMessage(null);
    try {
      const order = await api.checkout(customerId, subtotal, quote?.code ?? '', idempotencyKey);
      setMessage({
        kind: 'success',
        text: `Order #${order.id} placed: ${money(order.totalCents)}${order.discountCents ? ` (saved ${money(order.discountCents)})` : ''}`,
      });
      setIdempotencyKey(crypto.randomUUID());
      setQuote(null);
      setCode('');
      onChange();
    } catch (e) {
      showError(e);
    } finally {
      setBusy(false);
    }
  }

  if (customers.length === 0) {
    return <p className="card muted">No customers yet. Create one under “Customers & referrals”.</p>;
  }

  return (
    <div className="grid">
      <section className="card">
        <label>
          Ordering as
          <select value={customerId ?? ''} onChange={(e) => setCustomerId(Number(e.target.value))}>
            {customers.map((c) => (
              <option key={c.id} value={c.id}>{c.name} ({c.orders} orders)</option>
            ))}
          </select>
        </label>

        <h2>Menu</h2>
        <ul className="menu">
          {MENU.map((item) => (
            <li key={item.id}>
              <span>{item.name}<br /><span className="muted">{money(item.cents)}</span></span>
              <span className="qty">
                <button aria-label={`Remove ${item.name}`} onClick={() => setQty(item.id, -1)}>−</button>
                <span>{cart[item.id] ?? 0}</span>
                <button aria-label={`Add ${item.name}`} onClick={() => setQty(item.id, 1)}>+</button>
              </span>
            </li>
          ))}
        </ul>
      </section>

      <section className="card">
        <h2>Checkout</h2>
        <form className="row" onSubmit={(e) => { e.preventDefault(); applyCode(); }}>
          <input placeholder="Voucher code" aria-label="Voucher code" value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase())} />
          <button type="submit" disabled={!code.trim()}>Apply</button>
        </form>
        <p className="muted hint">Try WELCOME20 (first order), LUNCH5 (over $25) or FLASH50 (first 5 orders).</p>

        {wallet.length > 0 && (
          <div className="wallet">
            <span className="muted">Your vouchers:</span>
            {wallet.map((w) => (
              <button key={w.code} className="chip" disabled={w.used} title={describe(w.campaign)} onClick={() => applyCode(w.code)}>
                {w.code} {w.used ? '(used)' : `· ${w.campaign.kind === 'percent' ? `${w.campaign.value}%` : money(w.campaign.value)}`}
              </button>
            ))}
          </div>
        )}

        <dl className="totals">
          <dt>Subtotal</dt><dd>{money(subtotal)}</dd>
          {quote && (<><dt>{quote.code} <span className="muted">{quote.campaignName}</span></dt><dd className="discount">−{money(quote.discountCents)}</dd></>)}
          <dt className="total">Total</dt><dd className="total">{money(quote ? quote.totalCents : subtotal)}</dd>
        </dl>

        {message && <p className={`alert ${message.kind}`}>{message.text}</p>}
        <button className="primary" disabled={busy || subtotal === 0} onClick={placeOrder}>
          {busy ? 'Placing order…' : 'Place order'}
        </button>
      </section>

      <section className="card span">
        <h2>Order history</h2>
        {orders.length === 0 ? <p className="muted">No orders yet.</p> : (
          <table>
            <thead><tr><th>#</th><th>Subtotal</th><th>Voucher</th><th>Discount</th><th>Total</th><th>Time</th></tr></thead>
            <tbody>
              {orders.map((o) => (
                <tr key={o.id}>
                  <td>{o.id}</td><td>{money(o.subtotalCents)}</td><td>{o.voucherCode ?? '–'}</td>
                  <td>{o.discountCents ? `−${money(o.discountCents)}` : '–'}</td><td>{money(o.totalCents)}</td>
                  <td>{new Date(o.createdAt).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
