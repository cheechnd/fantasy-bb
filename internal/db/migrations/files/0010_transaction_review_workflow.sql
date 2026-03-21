CREATE TABLE IF NOT EXISTS transaction_review_states (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  transaction_plan_item_id INTEGER NOT NULL UNIQUE REFERENCES transaction_plan_items(id) ON DELETE CASCADE,
  current_state TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS transaction_review_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  transaction_plan_item_id INTEGER NOT NULL REFERENCES transaction_plan_items(id) ON DELETE CASCADE,
  previous_state TEXT NOT NULL,
  new_state TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  changed_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_transaction_review_states_item_id
  ON transaction_review_states(transaction_plan_item_id);

CREATE INDEX IF NOT EXISTS idx_transaction_review_states_state_updated
  ON transaction_review_states(current_state, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_transaction_review_history_item_changed
  ON transaction_review_history(transaction_plan_item_id, changed_at DESC);

INSERT INTO transaction_review_states (transaction_plan_item_id, current_state, note, updated_at)
SELECT tpi.id, 'pending', '', tpi.created_at
FROM transaction_plan_items tpi
LEFT JOIN transaction_review_states trs ON trs.transaction_plan_item_id = tpi.id
WHERE trs.id IS NULL;

INSERT INTO transaction_review_history (transaction_plan_item_id, previous_state, new_state, note, changed_at)
SELECT tpi.id, 'pending', 'pending', 'initial state', tpi.created_at
FROM transaction_plan_items tpi
LEFT JOIN transaction_review_history trh ON trh.transaction_plan_item_id = tpi.id
WHERE trh.id IS NULL;
