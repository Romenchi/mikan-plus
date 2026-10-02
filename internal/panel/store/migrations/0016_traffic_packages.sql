-- +goose Up
-- Traffic packages (GitHub issue #12): extra traffic for the main quota or one traffic
-- pool, sold in the bot and the Mini App or given by the admin. What a user got is a
-- grant: the base quota of the period is spent first, then the grants, the soonest to
-- expire first. Grants outlive period resets, except those that last one period.
CREATE TABLE traffic_packages (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  bytes       INTEGER NOT NULL CHECK (bytes > 0),
  pool_id     INTEGER REFERENCES traffic_pools(id) ON DELETE CASCADE, -- NULL: the main traffic
  lifetime    TEXT NOT NULL CHECK (lifetime IN ('used', 'period', 'days')),
  days        INTEGER NOT NULL DEFAULT 0,                             -- lifetime 'days': from the purchase
  price_stars INTEGER,                                                -- NULL: not sold for Stars
  price_rub   INTEGER,                                                -- kopecks; NULL: not sold for rubles
  on_sale     INTEGER NOT NULL DEFAULT 0,
  sort        INTEGER NOT NULL DEFAULT 0,
  archived    INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  CHECK (lifetime <> 'days' OR days BETWEEN 1 AND 3650)
);

-- Payments buy a package too: the tariff becomes optional, and marketplace adapters
-- (provider "addon:<name>") take payments next to the built-in providers. SQLite changes
-- CHECK constraints only by rebuilding the table; nothing references payments yet.
CREATE TABLE payments_new (
  id          INTEGER PRIMARY KEY,
  provider    TEXT NOT NULL CHECK (provider IN ('stars', 'yookassa', 'cryptobot') OR provider LIKE 'addon:%'),
  payload     TEXT NOT NULL UNIQUE,
  external_id TEXT,
  tg_id       INTEGER NOT NULL,
  kind        TEXT NOT NULL CHECK (kind IN ('new', 'renew', 'package')),
  user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  tariff_id   INTEGER REFERENCES tariffs(id),                         -- new and renew
  package_id  INTEGER REFERENCES traffic_packages(id) ON DELETE SET NULL, -- package
  tariff_name TEXT NOT NULL,                                          -- the tariff's or package's name as bought
  amount      INTEGER NOT NULL,
  currency    TEXT NOT NULL CHECK (currency IN ('XTR', 'RUB')),
  status      TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'applied', 'expired', 'failed', 'refunded')),
  error       TEXT NOT NULL DEFAULT '',
  pay_url     TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  paid_at     INTEGER,
  applied_at  INTEGER,
  refunded_at INTEGER,
  CHECK (kind = 'package' OR tariff_id IS NOT NULL)
);
INSERT INTO payments_new (id, provider, payload, external_id, tg_id, kind, user_id, tariff_id, tariff_name, amount, currency, status,
                          error, pay_url, created_at, paid_at, applied_at, refunded_at)
SELECT id, provider, payload, external_id, tg_id, kind, user_id, tariff_id, tariff_name, amount, currency, status,
       error, pay_url, created_at, paid_at, applied_at, refunded_at
FROM payments;
DROP TABLE payments;
ALTER TABLE payments_new RENAME TO payments;
CREATE UNIQUE INDEX payments_external ON payments(provider, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX payments_status ON payments(status, created_at);
CREATE INDEX payments_user ON payments(user_id);

-- A grant: traffic a user got from a purchase or from the admin. remaining goes down as
-- the user spends past the base quota; expires_at NULL lasts until used up.
CREATE TABLE traffic_grants (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  pool_id    INTEGER REFERENCES traffic_pools(id) ON DELETE CASCADE, -- NULL: the main traffic
  bytes      INTEGER NOT NULL CHECK (bytes > 0),
  remaining  INTEGER NOT NULL CHECK (remaining BETWEEN 0 AND bytes),
  lifetime   TEXT NOT NULL CHECK (lifetime IN ('used', 'period', 'days')),
  expires_at INTEGER,
  source     TEXT NOT NULL CHECK (source IN ('purchase', 'admin')),
  payment_id INTEGER UNIQUE REFERENCES payments(id) ON DELETE SET NULL, -- one grant per payment
  package_id INTEGER REFERENCES traffic_packages(id) ON DELETE SET NULL,
  note       TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX traffic_grants_user ON traffic_grants(user_id, pool_id);

-- +goose Down
DROP TABLE traffic_grants;
CREATE TABLE payments_old (
  id          INTEGER PRIMARY KEY,
  provider    TEXT NOT NULL CHECK (provider IN ('stars', 'yookassa', 'cryptobot')),
  payload     TEXT NOT NULL UNIQUE,
  external_id TEXT,
  tg_id       INTEGER NOT NULL,
  kind        TEXT NOT NULL CHECK (kind IN ('new', 'renew')),
  user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  tariff_id   INTEGER NOT NULL REFERENCES tariffs(id),
  tariff_name TEXT NOT NULL,
  amount      INTEGER NOT NULL,
  currency    TEXT NOT NULL CHECK (currency IN ('XTR', 'RUB')),
  status      TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'applied', 'expired', 'failed', 'refunded')),
  error       TEXT NOT NULL DEFAULT '',
  pay_url     TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  paid_at     INTEGER,
  applied_at  INTEGER,
  refunded_at INTEGER
);
INSERT INTO payments_old (id, provider, payload, external_id, tg_id, kind, user_id, tariff_id, tariff_name, amount, currency, status,
                          error, pay_url, created_at, paid_at, applied_at, refunded_at)
SELECT id, provider, payload, external_id, tg_id, kind, user_id, tariff_id, tariff_name, amount, currency, status,
       error, pay_url, created_at, paid_at, applied_at, refunded_at
FROM payments WHERE kind <> 'package' AND provider IN ('stars', 'yookassa', 'cryptobot');
DROP TABLE payments;
ALTER TABLE payments_old RENAME TO payments;
CREATE UNIQUE INDEX payments_external ON payments(provider, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX payments_status ON payments(status, created_at);
CREATE INDEX payments_user ON payments(user_id);
DROP TABLE traffic_packages;
