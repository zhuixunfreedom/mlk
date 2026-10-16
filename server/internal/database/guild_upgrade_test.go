package database

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestGuildSnapshotUpgradeFrom0028(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite3")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := fs.Sub(migrations, "migrations")
	old := fstest.MapFS{}
	entries, _ := fs.ReadDir(src, ".")
	for _, e := range entries {
		if e.Name() >= "0029" {
			continue
		}
		raw, _ := fs.ReadFile(src, e.Name())
		old[e.Name()] = &fstest.MapFile{Data: raw}
	}
	if err = migrate(context.Background(), db, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO cn35_guilds(guild_id,name,created_at,updated_at,gp) VALUES('a','A','old','old',500),('b','B','old','old',400)`,
		`INSERT INTO cn35_gw_sessions(session_id,date_key,field_index,group_key,created_at) VALUES('s','2026-10-02',0,'a+b','old')`,
		`INSERT INTO cn35_gw_participants(session_id,guild_id,gp) VALUES('s','a',500),('s','b',400)`,
		`INSERT INTO cn35_gw_actions(session_id,user_id,op_key,action,bp_cost,gp_delta,created_at) VALUES('s','ua','help:s','help',25,500,'old'),('s','ub','pray:s','pray',15,400,'old')`,
		`INSERT INTO cn35_gw_member_scores(session_id,user_id,guild_id,gp_earned,updated_at) VALUES('s','ua','a',500,'old'),('s','ub','b',400,'old')`,
		`INSERT INTO cn35_gw_attacks(battle_id,session_id,user_id,guild_id,target_guild_id,difficulty,status,bp_cost,snapshot_json,party_json,created_at) VALUES('bid','s','ua','a','b','normal','bp_paid',30,'[]','{"party_id":"1"}','old')`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sp, hc, pc, gp int
	var prayer float64
	var target, status string
	if err = db.QueryRow(`SELECT initial_sp,help_count,gp FROM cn35_gw_participants WHERE guild_id='a'`).Scan(&sp, &hc, &gp); err != nil || sp != 200 || hc != 1 || gp != 500 {
		t.Fatalf("help upgrade %d %d %d %v", sp, hc, gp, err)
	}
	if err = db.QueryRow(`SELECT pray_count,seed_powerup_rate,gp FROM cn35_gw_participants WHERE guild_id='b'`).Scan(&pc, &prayer, &gp); err != nil || pc != 1 || prayer != 0.025 || gp != 400 {
		t.Fatalf("prayer upgrade %d %f %d %v", pc, prayer, gp, err)
	}
	if err = db.QueryRow(`SELECT targets_json,status FROM cn35_gw_attacks WHERE battle_id='bid'`).Scan(&target, &status); err != nil || target != `["b"]` || status != "bp_paid" {
		t.Fatalf("legacy attack %s %s %v", target, status, err)
	}
	var fk string
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatalf("FK violation %s", fk)
	}
}
