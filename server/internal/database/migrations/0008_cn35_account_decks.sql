-- CN3.5 account-owned, editable parties (MIN350-06).
-- user_units[].id is a template key and 0005 already guarantees one row per
-- (user_id, unit_id); the composite slot FK prevents cross-account members.
-- Current local story content has unit_count_limit=5. The five-slot shape is
-- this reconstruction's playable-content policy, not a recovered global cap.
CREATE TABLE cn35_player_decks (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    party_id TEXT NOT NULL CHECK (
        length(party_id) > 0
        AND party_id NOT GLOB '*[^0-9]*'
        AND CAST(party_id AS INTEGER) > 0
    ),
    display_order INTEGER NOT NULL CHECK (display_order >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, party_id),
    UNIQUE (user_id, display_order)
) STRICT;

CREATE TABLE cn35_player_deck_slots (
    user_id TEXT NOT NULL,
    party_id TEXT NOT NULL,
    slot_index INTEGER NOT NULL CHECK (slot_index BETWEEN 0 AND 4),
    unit_id INTEGER CHECK (unit_id IS NULL OR unit_id > 0),
    PRIMARY KEY (user_id, party_id, slot_index),
    UNIQUE (user_id, party_id, unit_id),
    FOREIGN KEY (user_id, party_id)
        REFERENCES cn35_player_decks(user_id, party_id) ON DELETE RESTRICT,
    FOREIGN KEY (user_id, unit_id)
        REFERENCES cn35_player_units(user_id, unit_id) ON DELETE RESTRICT
) STRICT;

CREATE INDEX cn35_player_deck_slots_unit_key
    ON cn35_player_deck_slots(user_id, unit_id) WHERE unit_id IS NOT NULL;
