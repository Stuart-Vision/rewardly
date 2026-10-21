import { useCallback, useEffect, useState } from 'react';
import { api, money, type Stats } from './api';
import Checkout from './components/Checkout';
import Customers from './components/Customers';
import Campaigns from './components/Campaigns';

const TABS = ['Checkout', 'Customers & referrals', 'Campaigns'] as const;
type Tab = (typeof TABS)[number];
const slug = (t: Tab) => t.split(' ')[0].toLowerCase();

export default function App() {
  // The tab lives in the URL hash so a link can open a specific tab.
  const fromHash = () => TABS.find((t) => slug(t) === window.location.hash.slice(1)) ?? 'Checkout';
  const [tab, setTabState] = useState<Tab>(fromHash);
  const setTab = (t: Tab) => { window.location.hash = slug(t); setTabState(t); };
  const [stats, setStats] = useState<Stats | null>(null);
  const [offline, setOffline] = useState(false);
  // Bumped after every write so all panels reload their data.
  const [version, setVersion] = useState(0);
  const changed = useCallback(() => setVersion((v) => v + 1), []);

  useEffect(() => {
    api.stats().then((s) => { setStats(s); setOffline(false); }).catch(() => setOffline(true));
  }, [version]);

  const rate = (part: number, whole: number) => (whole ? `${Math.round((part / whole) * 100)}%` : '–');

  return (
    <div className="page">
      <header>
        <div>
          <h1>Rewardly</h1>
          <p className="muted">Vouchers and referrals for a food delivery app · Go API + React + PostgreSQL</p>
        </div>
      </header>

      {offline && <p className="alert error">Can't reach the API. Start it with <code>go run ./cmd/api</code> in <code>backend/</code>.</p>}

      {stats && (
        <section className="stats">
          <Stat label="Customers" value={stats.customers} />
          <Stat label="Joined by referral" value={rate(stats.referredCustomers, stats.customers)} />
          <Stat label="Orders" value={stats.orders} />
          <Stat label="Orders with a voucher" value={rate(stats.ordersWithVoucher, stats.orders)} />
          <Stat label="Discount given" value={money(stats.discountGivenCents)} />
          <Stat label="Net revenue" value={money(stats.revenueCents)} />
        </section>
      )}

      <nav className="tabs" role="tablist">
        {TABS.map((t) => (
          <button key={t} role="tab" aria-selected={tab === t} className={tab === t ? 'active' : ''} onClick={() => setTab(t)}>
            {t}
          </button>
        ))}
      </nav>

      {tab === 'Checkout' && <Checkout version={version} onChange={changed} />}
      {tab === 'Customers & referrals' && <Customers version={version} onChange={changed} />}
      {tab === 'Campaigns' && <Campaigns version={version} onChange={changed} />}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="stat">
      <span className="muted">{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
