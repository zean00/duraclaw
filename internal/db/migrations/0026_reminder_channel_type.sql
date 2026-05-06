ALTER TABLE reminder_subscriptions
    ADD COLUMN IF NOT EXISTS channel_type text;

CREATE INDEX IF NOT EXISTS reminder_subscriptions_channel_idx
    ON reminder_subscriptions (customer_id, user_id, channel_type)
    WHERE channel_type IS NOT NULL;
