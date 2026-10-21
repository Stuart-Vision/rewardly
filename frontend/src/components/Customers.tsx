import { useEffect, useState, type FormEvent } from 'react';
import { api, ApiError, type Customer } from '../api';

interface Props { version: number; onChange: () => void }

export default function Customers({ version, onChange }: Props) {
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [form, setForm] = useState({ name: '', email: '', referralCode: '' });
  const [message, setMessage] = useState<{ kind: 'error' | 'success'; text: string } | null>(null);

  useEffect(() => { api.customers().then(setCustomers).catch(() => {}); }, [version]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMessage(null);
    try {
      const c = await api.createCustomer(form);
      setMessage({
        kind: 'success',
        text: form.referralCode
          ? `Welcome ${c.name}! A first-order voucher is in their wallet, and the referrer earns a reward when ${c.name} orders.`
          : `Welcome ${c.name}! Their referral code is ${c.referralCode}.`,
      });
      setForm({ name: '', email: '', referralCode: '' });
      onChange();
    } catch (err) {
      setMessage({ kind: 'error', text: err instanceof ApiError ? err.message : 'Network error' });
    }
  }

  const byId = new Map(customers.map((c) => [c.id, c.name]));

  return (
    <div className="grid">
      <section className="card">
        <h2>Sign up</h2>
        <form className="stack" onSubmit={submit}>
          <label>Name<input required maxLength={80} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></label>
          <label>E-mail<input required type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} /></label>
          <label>
            Referral code <span className="muted">(optional)</span>
            <input value={form.referralCode} placeholder="A friend's code"
              onChange={(e) => setForm({ ...form, referralCode: e.target.value.toUpperCase() })} />
          </label>
          <button className="primary" type="submit">Create customer</button>
        </form>
        {message && <p className={`alert ${message.kind}`}>{message.text}</p>}
      </section>

      <section className="card">
        <h2>How referrals work</h2>
        <ol className="steps">
          <li>Every customer gets a referral code to share.</li>
          <li>A friend who signs up with it gets a personal <strong>30% off</strong> first-order voucher.</li>
          <li>When the friend places their <strong>first</strong> order, the referrer gets a <strong>5.00</strong> reward voucher, once per friend, even if orders arrive at the same moment.</li>
        </ol>
      </section>

      <section className="card span">
        <h2>Customers</h2>
        {customers.length === 0 ? <p className="muted">No customers yet.</p> : (
          <table>
            <thead><tr><th>Name</th><th>E-mail</th><th>Referral code</th><th>Referred by</th><th>Orders</th><th>Referrals</th></tr></thead>
            <tbody>
              {customers.map((c) => (
                <tr key={c.id}>
                  <td>{c.name}</td><td>{c.email}</td><td><code>{c.referralCode}</code></td>
                  <td>{c.referredBy ? byId.get(c.referredBy) ?? `#${c.referredBy}` : '–'}</td>
                  <td>{c.orders}</td><td>{c.referrals}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
