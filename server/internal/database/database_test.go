package database

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMigrationLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src, _ := fs.Sub(migrations, "migrations")
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	raw1, _ := fs.ReadFile(src, "0001_accounts.sql")
	raw2, _ := fs.ReadFile(src, "0002_player_core.sql")
	raw3, _ := fs.ReadFile(src, "0003_cn35.sql")
	raw4, _ := fs.ReadFile(src, "0004_cn35_guest_device.sql")
	raw5, _ := fs.ReadFile(src, "0005_cn35_inventory.sql")
	raw6, _ := fs.ReadFile(src, "0006_cn35_quest_progress.sql")
	raw7, _ := fs.ReadFile(src, "0007_cn35_quest_battle.sql")
	raw8, _ := fs.ReadFile(src, "0008_cn35_account_decks.sql")
	raw9, _ := fs.ReadFile(src, "0009_cn35_monster_size.sql")
	raw10, _ := fs.ReadFile(src, "0010_cn35_messages.sql")
	raw11, _ := fs.ReadFile(src, "0011_cn35_messages_seed.sql")
	raw12, _ := fs.ReadFile(src, "0012_cn35_gacha.sql")
	raw13, _ := fs.ReadFile(src, "0013_cn35_gacha_roster.sql")
	raw14, _ := fs.ReadFile(src, "0014_cn35_app_conf_versions.sql")
	raw15, _ := fs.ReadFile(src, "0015_cn35_presents_infos.sql")
	raw16, _ := fs.ReadFile(src, "0016_cn35_unit_helper.sql")
	raw17, _ := fs.ReadFile(src, "0017_cn35_items_resources.sql")
	raw18, _ := fs.ReadFile(src, "0018_cn35_low_intensity_waves.sql")
	raw19, _ := fs.ReadFile(src, "0019_cn35_tutorial_task_claims.sql")
	raw20, _ := fs.ReadFile(src, "0020_cn35_item_grants_and_material_goods.sql")
	raw21, _ := fs.ReadFile(src, "0021_cn35_item_bag_category_fix.sql")
	raw22, _ := fs.ReadFile(src, "0022_cn35_user_exp_curve.sql")
	raw23, _ := fs.ReadFile(src, "0023_cn35_medal_potion_goods.sql")
	raw24, _ := fs.ReadFile(src, "0024_cn35_item_use_ap_growth.sql")
	raw25, _ := fs.ReadFile(src, "0025_cn35_skills.sql")
	raw26, _ := fs.ReadFile(src, "0026_cn35_rune_goods_fixups.sql")
	raw27, _ := fs.ReadFile(src, "0027_cn35_guild.sql")
	raw28, _ := fs.ReadFile(src, "0028_guild_subs_pending.sql")
	raw29, _ := fs.ReadFile(src, "0029_cn35_guild_battle_snapshots.sql")
	raw30, _ := fs.ReadFile(src, "0030_cn35_rune_hunt.sql")
	raw31, _ := fs.ReadFile(src, "0031_cn35_daily_shop.sql")
	raw32, _ := fs.ReadFile(src, "0032_jp_accounts.sql")
	raw33, _ := fs.ReadFile(src, "0033_cn35_guildwar_bp_potion_off.sql")
	upgrade := fstest.MapFS{
		"0001_accounts.sql":                            {Data: raw1},
		"0002_player_core.sql":                         {Data: raw2},
		"0003_cn35.sql":                                {Data: raw3},
		"0004_cn35_guest_device.sql":                   {Data: raw4},
		"0005_cn35_inventory.sql":                      {Data: raw5},
		"0006_cn35_quest_progress.sql":                 {Data: raw6},
		"0007_cn35_quest_battle.sql":                   {Data: raw7},
		"0008_cn35_account_decks.sql":                  {Data: raw8},
		"0009_cn35_monster_size.sql":                   {Data: raw9},
		"0010_cn35_messages.sql":                       {Data: raw10},
		"0011_cn35_messages_seed.sql":                  {Data: raw11},
		"0012_cn35_gacha.sql":                          {Data: raw12},
		"0013_cn35_gacha_roster.sql":                   {Data: raw13},
		"0014_cn35_app_conf_versions.sql":              {Data: raw14},
		"0015_cn35_presents_infos.sql":                 {Data: raw15},
		"0016_cn35_unit_helper.sql":                    {Data: raw16},
		"0017_cn35_items_resources.sql":                {Data: raw17},
		"0018_cn35_low_intensity_waves.sql":            {Data: raw18},
		"0019_cn35_tutorial_task_claims.sql":           {Data: raw19},
		"0020_cn35_item_grants_and_material_goods.sql": {Data: raw20},
		"0021_cn35_item_bag_category_fix.sql":          {Data: raw21},
		"0022_cn35_user_exp_curve.sql":                 {Data: raw22},
		"0023_cn35_medal_potion_goods.sql":             {Data: raw23},
		"0024_cn35_item_use_ap_growth.sql":             {Data: raw24},
		"0025_cn35_skills.sql":                         {Data: raw25},
		"0026_cn35_rune_goods_fixups.sql":              {Data: raw26},
		"0027_cn35_guild.sql":                          {Data: raw27},
		"0028_guild_subs_pending.sql":                  {Data: raw28},
		"0029_cn35_guild_battle_snapshots.sql":         {Data: raw29},
		"0030_cn35_rune_hunt.sql":                      {Data: raw30},
		"0031_cn35_daily_shop.sql":                     {Data: raw31},
		"0032_jp_accounts.sql":                         {Data: raw32},
		"0033_cn35_guildwar_bp_potion_off.sql":         {Data: raw33},
		"0034_test.sql":                                {Data: []byte(`CREATE TABLE example(id INTEGER PRIMARY KEY); INSERT INTO example VALUES(42);`)},
	}
	if err = migrate(ctx, db, upgrade); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db); err == nil {
		t.Fatal("old binary accepted newer DB")
	}
	upgrade["0034_test.sql"].Data = []byte(`CREATE TABLE changed(id INTEGER);`)
	if err = migrate(ctx, db, upgrade); err == nil {
		t.Fatal("edited history accepted")
	}
}

func TestFailedBatchRollsBack(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := fstest.MapFS{"0001_test.sql": {Data: []byte(`CREATE TABLE example(id INTEGER);`)}, "0002_bad.sql": {Data: []byte(`INSERT INTO missing VALUES(1);`)}}
	if err = migrate(context.Background(), db, source); err == nil {
		t.Fatal("bad SQL succeeded")
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('example','schema_migrations')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial migration: %d %v", n, err)
	}
}

func TestBackupIncludesWALAndRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "source.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO legacy_imports VALUES ('test','digest',2,'now')`); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backup.sqlite3")
	if err = Backup(context.Background(), db, backup); err != nil {
		t.Fatal(err)
	}
	if err = Backup(context.Background(), db, backup); err == nil {
		t.Fatal("overwrote backup")
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var n int
	if err = restored.QueryRow(`SELECT row_count FROM legacy_imports WHERE name='test'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("backup content: %d %v", n, err)
	}
}

// TestPlayerCoreUpgradeBackfill 0001 真实库（两个代表性账号）升级 0002：
// 账号字段不变、每账号独立回填 profile + coin/gacha_point；后续 INSERT
// 账号由 trigger 同事务建档；初始化失败整批回滚；约束/外键/唯一拒绝
// 非法行。对应设计文档「Migration 与约束」验收段。
func TestPlayerCoreUpgradeBackfill(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src, _ := fs.Sub(migrations, "migrations")
	raw1, _ := fs.ReadFile(src, "0001_accounts.sql")
	raw2, _ := fs.ReadFile(src, "0002_player_core.sql")

	// 真实 0001 schema + 两个代表性账号（凭据/昵称各不相同）。
	if err = migrate(ctx, db, fstest.MapFS{"0001_accounts.sql": {Data: raw1}}); err != nil {
		t.Fatal(err)
	}
	insertAccount := func(heID, userID, name string) {
		t.Helper()
		_, err = db.ExecContext(ctx, `INSERT INTO accounts
			(he_account_id,user_id,encrypted_user_id,token,sk,user_name,is_new_user,tutorial_finish,created_at,updated_at)
			VALUES (?,?,?,?,?,?,1,0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
			heID, userID, "enc-"+userID, "tok-"+heID, "sk-"+heID, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	insertAccount("CN-A", "u-alpha", "甲")
	insertAccount("CN-B", "u-beta", "乙")

	// 升级到 0002：两个账号各自独立回填。
	if err = migrate(ctx, db, fstest.MapFS{
		"0001_accounts.sql":    {Data: raw1},
		"0002_player_core.sql": {Data: raw2},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ userID, name string }{{"u-alpha", "甲"}, {"u-beta", "乙"}} {
		var level, ap, maxAP, bp, maxBP int
		var leader, guild string
		if err = db.QueryRowContext(ctx, `SELECT level,ap,max_ap,bp,max_bp,leader_unit_id,guild_name_snapshot
			FROM player_profiles WHERE user_id=?`, tc.userID).
			Scan(&level, &ap, &maxAP, &bp, &maxBP, &leader, &guild); err != nil {
			t.Fatalf("%s profile: %v", tc.userID, err)
		}
		if level != 1 || ap != 30 || maxAP != 30 || bp != 600 || maxBP != 600 || leader != "152" || guild != "小溪的大宇宙" {
			t.Fatalf("%s 回填值错误: level=%d ap=%d/%d bp=%d/%d leader=%q guild=%q", tc.userID, level, ap, maxAP, bp, maxBP, leader, guild)
		}
		var coin, gachaPoint int
		if err = db.QueryRowContext(ctx, `SELECT amount FROM player_balances WHERE user_id=? AND currency='coin'`, tc.userID).Scan(&coin); err != nil {
			t.Fatalf("%s coin: %v", tc.userID, err)
		}
		if err = db.QueryRowContext(ctx, `SELECT amount FROM player_balances WHERE user_id=? AND currency='gacha_point'`, tc.userID).Scan(&gachaPoint); err != nil {
			t.Fatalf("%s gacha_point: %v", tc.userID, err)
		}
		if coin != 3 || gachaPoint != 96343 {
			t.Fatalf("%s 余额回填错误: coin=%d gacha_point=%d", tc.userID, coin, gachaPoint)
		}
		var name string
		if err = db.QueryRowContext(ctx, `SELECT user_name FROM accounts WHERE user_id=?`, tc.userID).Scan(&name); err != nil || name != tc.name {
			t.Fatalf("%s 账号字段被改动: name=%q err=%v", tc.userID, name, err)
		}
	}

	// trigger：新 INSERT 账号同事务建档。
	insertAccount("CN-C", "u-gamma", "丙")
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM player_profiles WHERE user_id='u-gamma'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("trigger 未建 profile: %d %v", n, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM player_balances WHERE user_id='u-gamma'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("trigger 未建双余额: %d %v", n, err)
	}

	// 约束：负余额 / ap>max_ap / bp>max_bp / 外键 / 唯一 一律拒绝。
	violations := []string{
		`INSERT INTO player_balances VALUES ('u-alpha','coin',-1,'t')`,
		`INSERT INTO player_profiles VALUES ('u-none',1,0,100,0,30,'t',0,600,'1',0,'g','t','t')`,
		`UPDATE player_profiles SET ap=99 WHERE user_id='u-alpha'`,
		`UPDATE player_profiles SET bp=601 WHERE user_id='u-alpha'`,
		`INSERT INTO player_balances VALUES ('u-alpha','coin',0,'t')`,
	}
	for i, sql := range violations {
		if _, err = db.ExecContext(ctx, sql); err == nil {
			t.Fatalf("约束违规 #%d 未被拒绝: %s", i, sql)
		}
	}

	// 初始化失败整批回滚：拆掉 profile 表后 INSERT 账号 → trigger 失败，
	// 账号行不得残留。
	if _, err = db.ExecContext(ctx, `DROP TABLE player_profiles`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO accounts
		(he_account_id,user_id,encrypted_user_id,token,sk,user_name,is_new_user,tutorial_finish,created_at,updated_at)
		VALUES ('CN-D','u-delta','enc-u-delta','tok','sk','丁',1,0,'t','t')`); err == nil {
		t.Fatal("trigger 失败时账号 INSERT 竟然成功")
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE user_id='u-delta'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("初始化失败后账号残留: %d %v", n, err)
	}
}

func TestMigrationNames(t *testing.T) {
	for _, name := range []string{"0002_gap.sql", "bad.sql"} {
		if _, err := scripts(fstest.MapFS{name: {Data: []byte("SELECT 1;")}}); err == nil {
			t.Fatal("invalid sequence accepted", name)
		}
	}
}
