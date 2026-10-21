// Typed client for the Rewardly Go API.

export type Kind = 'percent' | 'fixed';

export interface Campaign {
  id: number;
  name: string;
  kind: Kind;
  value: number;
  maxDiscountCents?: number;
  minOrderCents: number;
  newCustomersOnly: boolean;
  perCustomerLimit: number;
  maxRedemptions?: number;
  redeemedCount: number;
}

export interface CampaignStats extends Campaign {
  code?: string;
  system: boolean;
  discountGivenCents: number;
}

export interface Customer {
  id: number;
  email: string;
  name: string;
  referralCode: string;
  referredBy?: number;
  orders: number;
  referrals: number;
}

export interface WalletVoucher {
  code: string;
  used: boolean;
  campaign: Campaign;
}

export interface Order {
  id: number;
  subtotalCents: number;
  discountCents: number;
  totalCents: number;
  voucherCode?: string;
  createdAt: string;
}

export interface Quote {
  code: string;
  campaignName: string;
  discountCents: number;
  totalCents: number;
}

export interface Stats {
  customers: number;
  referredCustomers: number;
  orders: number;
  ordersWithVoucher: number;
  discountGivenCents: number;
  revenueCents: number;
}

/** An error returned by the API, with its stable code (e.g. "budget_exhausted"). */
export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
  }
}

const BASE = import.meta.env.VITE_API_URL ?? '';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  });
  const body = await res.json().catch(() => null);
  if (!res.ok) {
    throw new ApiError(res.status, body?.error?.code ?? 'http_error', body?.error?.message ?? `Request failed (${res.status})`);
  }
  return body as T;
}

const post = <T,>(path: string, data: unknown, headers?: Record<string, string>) =>
  request<T>(path, { method: 'POST', body: JSON.stringify(data), headers });

export const api = {
  stats: () => request<Stats>('/api/stats'),
  customers: () => request<Customer[]>('/api/customers'),
  createCustomer: (data: { name: string; email: string; referralCode: string }) => post<Customer>('/api/customers', data),
  wallet: (id: number) => request<WalletVoucher[]>(`/api/customers/${id}/vouchers`),
  orders: (id: number) => request<Order[]>(`/api/customers/${id}/orders`),
  campaigns: () => request<CampaignStats[]>('/api/campaigns'),
  createCampaign: (data: unknown) => post<CampaignStats>('/api/campaigns', data),
  quote: (customerId: number, code: string, subtotalCents: number) =>
    post<Quote>('/api/quote', { customerId, code, subtotalCents }),
  /** The key must stay the same when retrying one checkout, so a retry can't double-charge. */
  checkout: (customerId: number, subtotalCents: number, voucherCode: string, idempotencyKey: string) =>
    post<Order>('/api/checkout', { customerId, subtotalCents, voucherCode }, { 'Idempotency-Key': idempotencyKey }),
};

const fmt = new Intl.NumberFormat('en-SG', { style: 'currency', currency: 'SGD' });
export const money = (cents: number) => fmt.format(cents / 100);

export const describe = (c: Campaign) => {
  const off = c.kind === 'percent'
    ? `${c.value}% off${c.maxDiscountCents ? ` (max ${money(c.maxDiscountCents)})` : ''}`
    : `${money(c.value)} off`;
  const rules = [
    c.minOrderCents > 0 && `min ${money(c.minOrderCents)}`,
    c.newCustomersOnly && 'first order only',
    `${c.perCustomerLimit}× per customer`,
  ].filter(Boolean);
  return `${off} · ${rules.join(' · ')}`;
};
