ALTER TABLE transaction_plan_items ADD COLUMN add_start_date TEXT NULL;
ALTER TABLE transaction_plan_items ADD COLUMN add_start_opponent TEXT NULL;
ALTER TABLE transaction_plan_items ADD COLUMN drop_best_start_date TEXT NULL;
ALTER TABLE transaction_plan_items ADD COLUMN drop_best_start_opponent TEXT NULL;

CREATE INDEX IF NOT EXISTS idx_transaction_plan_items_add_start_date
  ON transaction_plan_items(add_start_date);
