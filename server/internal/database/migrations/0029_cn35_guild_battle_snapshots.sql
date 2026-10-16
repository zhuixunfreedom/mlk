-- cn35_guild_battle_snapshots
-- Describe affected routes, data backfill, verification and recovery in the PR.
-- Transaction is managed by mercdb; do not add BEGIN/COMMIT or PRAGMA.
-- 公会战 review 修复：效果与多目标持久快照；历史迁移不可改写。
ALTER TABLE cn35_gw_participants ADD COLUMN initial_sp INTEGER NOT NULL DEFAULT 0 CHECK(initial_sp>=0);
ALTER TABLE cn35_gw_participants ADD COLUMN help_count INTEGER NOT NULL DEFAULT 0 CHECK(help_count>=0);
ALTER TABLE cn35_gw_participants ADD COLUMN pray_count INTEGER NOT NULL DEFAULT 0 CHECK(pray_count>=0);
UPDATE cn35_gw_participants AS p SET
 help_count=(SELECT count(*) FROM cn35_gw_actions a JOIN cn35_gw_member_scores m ON m.session_id=a.session_id AND m.user_id=a.user_id WHERE a.session_id=p.session_id AND m.guild_id=p.guild_id AND a.action='help'),
 pray_count=(SELECT count(*) FROM cn35_gw_actions a JOIN cn35_gw_member_scores m ON m.session_id=a.session_id AND m.user_id=a.user_id WHERE a.session_id=p.session_id AND m.guild_id=p.guild_id AND a.action='pray');
UPDATE cn35_gw_participants SET initial_sp=help_count*200,seed_powerup_rate=pray_count*0.025;
ALTER TABLE cn35_gw_attacks ADD COLUMN targets_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE cn35_gw_attacks ADD COLUMN effects_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE cn35_gw_attacks ADD COLUMN request_json TEXT NOT NULL DEFAULT '';
UPDATE cn35_gw_attacks SET targets_json=json_array(target_guild_id);
CREATE INDEX cn35_gw_attacks_active ON cn35_gw_attacks(user_id,session_id,status);
