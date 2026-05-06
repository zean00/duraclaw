ALTER TABLE reminder_subscriptions
    ADD COLUMN IF NOT EXISTS repeat_interval_seconds integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS repeat_until timestamptz,
    ADD COLUMN IF NOT EXISTS repeat_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS fired_count integer NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'reminder_repeat_interval_non_negative'
    ) THEN
        ALTER TABLE reminder_subscriptions
            ADD CONSTRAINT reminder_repeat_interval_non_negative CHECK (repeat_interval_seconds >= 0) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'reminder_repeat_count_non_negative'
    ) THEN
        ALTER TABLE reminder_subscriptions
            ADD CONSTRAINT reminder_repeat_count_non_negative CHECK (repeat_count >= 0) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'reminder_fired_count_non_negative'
    ) THEN
        ALTER TABLE reminder_subscriptions
            ADD CONSTRAINT reminder_fired_count_non_negative CHECK (fired_count >= 0) NOT VALID;
    END IF;
END $$;
