-- System campaigns used by the referral programme.
INSERT INTO campaigns (slug, name, kind, value, max_discount_cents, min_order_cents, new_customers_only, per_customer_limit)
VALUES ('referral_welcome', 'Referral welcome: 30% off your first order (max 8.00)', 'percent', 30, 800, 0, true, 1),
       ('referral_reward', 'Referral reward: 5.00 off', 'fixed', 500, NULL, 1500, false, 1);

-- Public demo campaigns, one voucher code each.
WITH c AS (
    INSERT INTO campaigns (name, kind, value, max_discount_cents, min_order_cents, new_customers_only, per_customer_limit, max_redemptions)
    VALUES ('Welcome: 20% off your first order (max 10.00)', 'percent', 20, 1000, 0, true, 1, NULL)
    RETURNING id
)
INSERT INTO vouchers (campaign_id, code) SELECT id, 'WELCOME20' FROM c;

WITH c AS (
    INSERT INTO campaigns (name, kind, value, max_discount_cents, min_order_cents, new_customers_only, per_customer_limit, max_redemptions)
    VALUES ('Lunch deal: 5.00 off orders over 25.00', 'fixed', 500, NULL, 2500, false, 3, 500)
    RETURNING id
)
INSERT INTO vouchers (campaign_id, code) SELECT id, 'LUNCH5' FROM c;

WITH c AS (
    INSERT INTO campaigns (name, kind, value, max_discount_cents, min_order_cents, new_customers_only, per_customer_limit, max_redemptions)
    VALUES ('Flash sale: 50% off (max 15.00), first 5 orders only', 'percent', 50, 1500, 1000, false, 1, 5)
    RETURNING id
)
INSERT INTO vouchers (campaign_id, code) SELECT id, 'FLASH50' FROM c;
