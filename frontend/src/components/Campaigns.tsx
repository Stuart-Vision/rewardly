import { useEffect, useState, type FormEvent } from 'react';
import { api, ApiError, describe, money, type CampaignStats, type Kind } from '../api';

interface Props { version: number; onChange: () => void }

const EMPTY = { code: '', name: '', kind: 'percent' as Kind, value: '15', maxDiscount: '', minOrder: '', perCustomer: '1', budget: '', newOnly: false };

// Form fields are strings; the API takes integer cents (or null for "no limit").
const cents = (v: string) => (v.trim() === '' ? null : Math.round(Number(v) * 100));
const int = (v: string) => (v.trim() === '' ? null : Number(v));

export default function Campaigns({ version, onChange }: Props) {
  const [campaigns, setCampaigns] = useState<CampaignStats[]>([]);
  const [form, setForm] = useState(EMPTY);
  const [message, setMessage] = useState<{ kind: 'error' | 'success'; text: string } | null>(null);

  useEffect(() => { api.campaigns().then(setCampaigns).catch(() => {}); }, [version]);

  const set = (field: keyof typeof EMPTY) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setForm({ ...form, [field]: e.target instanceof HTMLInputElement && e.target.type === 'checkbox' ? e.target.checked : e.target.value });

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMessage(null);
    try {
      const c = await api.createCampaign({
        code: form.code,
        name: form.name,
        kind: form.kind,
        value: form.kind === 'percent' ? Number(form.value) : cents(form.value),
        maxDiscountCents: form.kind === 'percent' ? cents(form.maxDiscount) : null,
        minOrderCents: cents(form.minOrder) ?? 0,
        perCustomerLimit: int(form.perCustomer) ?? 1,
        maxRedemptions: int(form.budget),
        newCustomersOnly: form.newOnly,
      });
      setMessage({ kind: 'success', text: `Campaign created. Customers can now use ${c.code}.` });
      setForm(EMPTY);
      onChange();
    } catch (err) {
      setMessage({ kind: 'error', text: err instanceof ApiError ? err.message : 'Network error' });
    }
  }

  return (
    <div className="grid">
      <section className="card span">
        <h2>Campaigns</h2>
        <table>
          <thead><tr><th>Code</th><th>Campaign</th><th>Rules</th><th>Redeemed</th><th>Discount given</th></tr></thead>
          <tbody>
            {campaigns.map((c) => (
              <tr key={c.id}>
                <td>{c.system ? <span className="muted">personal</span> : <code>{c.code}</code>}</td>
                <td>{c.name}</td>
                <td className="muted">{describe(c)}</td>
                <td>
                  {c.redeemedCount}{c.maxRedemptions ? ` / ${c.maxRedemptions}` : ''}
                  {c.maxRedemptions && (
                    <div className="bar"><span style={{ width: `${Math.min(100, (c.redeemedCount / c.maxRedemptions) * 100)}%` }} /></div>
                  )}
                </td>
                <td>{money(c.discountGivenCents)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="card span">
        <h2>New campaign</h2>
        <form className="form-grid" onSubmit={submit}>
          <label>Code<input required value={form.code} placeholder="SUMMER15" onChange={(e) => setForm({ ...form, code: e.target.value.toUpperCase() })} /></label>
          <label>Name<input required value={form.name} placeholder="Summer promo" onChange={set('name')} /></label>
          <label>Type
            <select value={form.kind} onChange={set('kind')}>
              <option value="percent">Percent off</option>
              <option value="fixed">Fixed amount off</option>
            </select>
          </label>
          <label>{form.kind === 'percent' ? 'Percent' : 'Amount (SGD)'}<input required type="number" min="0.01" step="0.01" value={form.value} onChange={set('value')} /></label>
          {form.kind === 'percent' && (
            <label>Max discount (SGD)<input type="number" min="0.01" step="0.01" value={form.maxDiscount} placeholder="No cap" onChange={set('maxDiscount')} /></label>
          )}
          <label>Minimum order (SGD)<input type="number" min="0" step="0.01" value={form.minOrder} placeholder="None" onChange={set('minOrder')} /></label>
          <label>Uses per customer<input type="number" min="1" value={form.perCustomer} onChange={set('perCustomer')} /></label>
          <label>Total budget (uses)<input type="number" min="1" value={form.budget} placeholder="Unlimited" onChange={set('budget')} /></label>
          <label className="check"><input type="checkbox" checked={form.newOnly} onChange={set('newOnly')} /> First order only</label>
          <button className="primary" type="submit">Create campaign</button>
        </form>
        {message && <p className={`alert ${message.kind}`}>{message.text}</p>}
      </section>
    </div>
  );
}
