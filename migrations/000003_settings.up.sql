CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      JSONB        NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

INSERT INTO settings (key, value)
VALUES ('thresholds', '{"cpu_warn":85,"mem_warn":90,"disk_warn":90}')
ON CONFLICT (key) DO NOTHING;
