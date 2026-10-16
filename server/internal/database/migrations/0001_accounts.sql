-- Account identifiers are opaque protocol strings, not numeric IDs.
CREATE TABLE accounts (
    he_account_id TEXT PRIMARY KEY CHECK (length(he_account_id) > 0),
    user_id TEXT NOT NULL UNIQUE CHECK (length(user_id) > 0),
    encrypted_user_id TEXT NOT NULL UNIQUE,
    token TEXT NOT NULL,
    sk TEXT NOT NULL,
    user_name TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    is_new_user INTEGER NOT NULL CHECK (is_new_user IN (0, 1)),
    tutorial_finish INTEGER NOT NULL CHECK (tutorial_finish IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
CREATE TABLE legacy_imports (
    name TEXT PRIMARY KEY,
    source_sha256 TEXT NOT NULL,
    row_count INTEGER NOT NULL CHECK (row_count >= 0),
    imported_at TEXT NOT NULL
) STRICT;
