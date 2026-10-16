// guild.go GUILD350-02..08：公会/公会战持久化（迁移 0027 表族）。
//
// 事务纪律（docs/后端数据库与Go工作流.md）：
//   - 每条写面在 withTx 内完成「幂等判重 + 前置校验 + 跨域写入」；
//   - 状态迁移一律条件 UPDATE（RowsAffected==0 → 重放/并发拒绝）；
//   - 会战会话创建采用「(date_key,field_index,group_key) 唯一约束 +
//     冲突回填」模式，并发建场天然合并。
package cn35store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.GuildStore = (*Store)(nil)

// guildLoc 会战时段判定时区（国服 Asia/Shanghai，无夏令时）。
var guildLoc = time.FixedZone("Asia/Shanghai", 8*3600)

func newGuildID() string    { return "g" + randHex(14) }
func newGuildSubID() string { return "gs" + randHex(14) }

// errOpReplay 幂等键命中已决操作（调用方转「已成功」响应）。
var errOpReplay = errors.New("cn35store: 公会操作幂等键重放")

// opKeyOr 缺省幂等键：由操作语义生成（写面天然状态机幂等时仅作台账）。
func opKeyOr(opKey, fallback string) string {
	if opKey != "" {
		return opKey
	}
	return fallback + ":" + randHex(8)
}

// recordGuildOpTx 幂等键登记：已存在同键 → errOpReplay。
func recordGuildOpTx(tx *sql.Tx, userID, opKey, op, payload string) error {
	res, err := tx.Exec(`INSERT INTO cn35_guild_ops
		(user_id,op_key,op,payload_json,created_at) VALUES (?,?,?,?,?)
		ON CONFLICT(user_id,op_key) DO NOTHING`,
		userID, opKey, op, payload, nowUTC())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errOpReplay
	}
	return nil
}

// memberRoleTx 查用户职位；未入会返回 0,nil。
func memberRoleTx(tx *sql.Tx, userID string) (guildID string, role int, err error) {
	err = tx.QueryRow(`SELECT guild_id,role FROM cn35_guild_members WHERE user_id=?`,
		userID).Scan(&guildID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return guildID, role, err
}

// guildRowTx 读公会行。
func guildRowTx(tx *sql.Tx, guildID string) (*cn35game.Guild, error) {
	var g cn35game.Guild
	var shifts int
	err := tx.QueryRow(`SELECT guild_id,name,play_type_id,approval_type_id,
		recruit_text,memo,shifts,battle_setting_text,rating,rank_id,gp
		FROM cn35_guilds WHERE guild_id=?`, guildID).Scan(
		&g.ID, &g.Name, &g.PlayTypeID, &g.ApprovalTypeID, &g.RecruitText,
		&g.Memo, &shifts, &g.BattleSettingText, &g.Rating, &g.RankID, &g.Point)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 7; i++ {
		g.Shifts[i] = shifts&(1<<i) != 0
	}
	var master string
	_ = tx.QueryRow(`SELECT COALESCE(NULLIF(a.user_name,''),NULLIF(a.account_name,''),a.user_id)
		FROM cn35_guild_members m
		JOIN cn35_accounts a ON a.user_id=m.user_id
		WHERE m.guild_id=? AND m.role=3`, guildID).Scan(&master)
	g.MasterName = master
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_members WHERE guild_id=?`,
		guildID).Scan(&g.MemberCount); err != nil {
		return nil, err
	}
	return &g, nil
}

// guildViewTx 组装读面聚合。
func guildViewTx(tx *sql.Tx, guildID, viewerID string, withSubs bool) (*cn35game.GuildView, error) {
	g, err := guildRowTx(tx, guildID)
	if err != nil {
		return nil, err
	}
	view := &cn35game.GuildView{Guild: g, Equipments: map[string]*cn35game.GuildEquipment{}}
	rows, err := tx.Query(`SELECT m.user_id,
		COALESCE(NULLIF(a.user_name,''),NULLIF(a.account_name,''),a.user_id),
		m.role,p.level,p.leader_unit_id,m.joined_at
		FROM cn35_guild_members m
		JOIN cn35_accounts a ON a.user_id=m.user_id
		LEFT JOIN cn35_player_profiles p ON p.user_id=m.user_id
		WHERE m.guild_id=? ORDER BY m.role DESC, m.joined_at`, guildID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var mem cn35game.GuildMember
		if err = rows.Scan(&mem.UserID, &mem.Name, &mem.Role, &mem.Level, &mem.LeaderUnitID, &mem.JoinedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if mem.UserID == viewerID {
			view.MyRole = mem.Role
		}
		view.Members = append(view.Members, mem)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	eqRows, err := tx.Query(`SELECT type_id,level,point FROM cn35_guild_equipment
		WHERE guild_id=?`, guildID)
	if err != nil {
		return nil, err
	}
	for eqRows.Next() {
		var e cn35game.GuildEquipment
		if err = eqRows.Scan(&e.TypeID, &e.Level, &e.Point); err != nil {
			eqRows.Close()
			return nil, err
		}
		view.Equipments[e.TypeID] = &e
	}
	if err = eqRows.Close(); err != nil {
		return nil, err
	}
	if withSubs {
		subRows, err := tx.Query(`SELECT s.sub_id,s.guild_id,s.user_id,
			COALESCE(a.user_name,''),COALESCE(p.level,0),s.kind,s.status
			FROM cn35_guild_subscriptions s
			JOIN cn35_accounts a ON a.user_id=s.user_id
			LEFT JOIN cn35_player_profiles p ON p.user_id=s.user_id
			WHERE s.guild_id=? AND s.status='pending'
			ORDER BY s.created_at`, guildID)
		if err != nil {
			return nil, err
		}
		for subRows.Next() {
			var sub cn35game.GuildSubscription
			if err = subRows.Scan(&sub.SubID, &sub.GuildID, &sub.UserID,
				&sub.UserName, &sub.Level, &sub.Kind, &sub.Status); err != nil {
				subRows.Close()
				return nil, err
			}
			view.Subscriptions = append(view.Subscriptions, sub)
		}
		if err = subRows.Close(); err != nil {
			return nil, err
		}
	}
	return view, nil
}

// MyGuild /guilds/ 读面：未入会返回 Guild=nil。
func (s *Store) MyGuild(userID string) (*cn35game.GuildView, error) {
	guildID, role, err := func() (string, int, error) {
		var gid string
		var role int
		err := s.db.QueryRow(`SELECT guild_id,role FROM cn35_guild_members WHERE user_id=?`,
			userID).Scan(&gid, &role)
		return gid, role, err
	}()
	if errors.Is(err, sql.ErrNoRows) {
		return &cn35game.GuildView{Equipments: map[string]*cn35game.GuildEquipment{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var view *cn35game.GuildView
	err = s.withTx(func(tx *sql.Tx) error {
		v, err := guildViewTx(tx, guildID, userID, role >= 2)
		if err == nil {
			view = v
		}
		return err
	})
	return view, err
}

// GuildShow /guilds/show 读面（任意公会）。
func (s *Store) GuildShow(guildID string) (*cn35game.GuildView, error) {
	var view *cn35game.GuildView
	err := s.withTx(func(tx *sql.Tx) error {
		v, err := guildViewTx(tx, guildID, "", false)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrGuildNotFound
		}
		if err == nil {
			view = v
		}
		return err
	})
	return view, err
}

// SearchGuilds /guilds/search.json 读面。
func (s *Store) SearchGuilds(keyword, playTypeID, approvalTypeID string) ([]cn35game.Guild, error) {
	q := `SELECT guild_id,name,play_type_id,approval_type_id,recruit_text,memo,
		shifts,battle_setting_text,rating,rank_id,gp FROM cn35_guilds WHERE 1=1`
	args := []any{}
	if keyword != "" {
		q += ` AND name LIKE ?`
		args = append(args, "%"+keyword+"%")
	}
	if playTypeID != "" {
		q += ` AND (play_type_id=? OR play_type_id='')`
		args = append(args, playTypeID)
	}
	if approvalTypeID != "" {
		q += ` AND (approval_type_id=? OR approval_type_id='')`
		args = append(args, approvalTypeID)
	}
	q += ` ORDER BY gp DESC, created_at LIMIT 50`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.Guild{}
	for rows.Next() {
		var g cn35game.Guild
		var shifts int
		if err = rows.Scan(&g.ID, &g.Name, &g.PlayTypeID, &g.ApprovalTypeID,
			&g.RecruitText, &g.Memo, &shifts, &g.BattleSettingText,
			&g.Rating, &g.RankID, &g.Point); err != nil {
			return nil, err
		}
		for i := 0; i < 7; i++ {
			g.Shifts[i] = shifts&(1<<i) != 0
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// syncGuildNameTx 公会名快照投影回 cn35_player_profiles（Snapshot.user.guild_name）。
func syncGuildNameTx(tx *sql.Tx, userID, name string) error {
	_, err := tx.Exec(`UPDATE cn35_player_profiles SET guild_name_snapshot=?,updated_at=?
		WHERE user_id=?`, name, nowUTC(), userID)
	return err
}

// CreateGuild POST /guilds/create.json。
func (s *Store) CreateGuild(userID, name, playTypeID, approvalTypeID, recruitText, opKey string) (*cn35game.Guild, error) {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n < 1 || n > 8 {
		return nil, cn35game.ErrGuildName
	}
	var g *cn35game.Guild
	err := s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, userID, opKeyOr(opKey, "create"), "create_guild", name); err != nil {
			return err
		}
		if gid, _, err := memberRoleTx(tx, userID); err != nil {
			return err
		} else if gid != "" {
			return cn35game.ErrGuildJoined
		}
		gid := newGuildID()
		now := nowUTC()
		if _, err := tx.Exec(`INSERT INTO cn35_guilds
			(guild_id,name,play_type_id,approval_type_id,recruit_text,memo,shifts,
			 battle_setting_text,rating,rank_id,gp,gp_total,created_at,updated_at)
			VALUES (?,?,?,?,?,'',127,'',0,'1',0,0,?,?)`,
			gid, name, playTypeID, approvalTypeID, recruitText, now, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_guild_members
			(guild_id,user_id,role,joined_at) VALUES (?,?,3,?)`, gid, userID, now); err != nil {
			return err
		}
		if err := syncGuildNameTx(tx, userID, name); err != nil {
			return err
		}
		row, err := guildRowTx(tx, gid)
		if err == nil {
			g = row
		}
		return err
	})
	return g, err
}

// JoinGuild 入会（auto_approval 直进；否则挂 apply 申请）。
func (s *Store) JoinGuild(userID, guildID, opKey string) (bool, error) {
	auto := false
	err := s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, userID, opKeyOr(opKey, "join:"+guildID), "join_guild", guildID); err != nil {
			return err
		}
		g, err := guildRowTx(tx, guildID)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrGuildNotFound
		}
		if err != nil {
			return err
		}
		if gid, _, err := memberRoleTx(tx, userID); err != nil {
			return err
		} else if gid != "" {
			return cn35game.ErrGuildJoined
		}
		// auto_approval = approval_type_id 空或 "1"（暂定契约，见取证 §7）。
		if g.ApprovalTypeID == "" || g.ApprovalTypeID == "1" {
			if g.MemberCount >= cn35game.GuildRules.MaxMembers {
				return cn35game.ErrGuildFull
			}
			if _, err := tx.Exec(`INSERT INTO cn35_guild_members
				(guild_id,user_id,role,joined_at) VALUES (?,?,1,?)`,
				guildID, userID, nowUTC()); err != nil {
				return err
			}
			if err := syncGuildNameTx(tx, userID, g.Name); err != nil {
				return err
			}
			auto = true
			return nil
		}
		// 申请制：挂 pending apply（同对 (guild,user,kind) 仅 pending 唯一）。
		res, err := tx.Exec(`INSERT INTO cn35_guild_subscriptions
			(sub_id,guild_id,user_id,kind,status,created_at)
			VALUES (?,?,?,'apply','pending',?)
			ON CONFLICT(guild_id,user_id,kind) WHERE status='pending' DO NOTHING`,
			newGuildSubID(), guildID, userID, nowUTC())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return cn35game.ErrGuildSubDecided
		}
		return nil
	})
	return auto, err
}

// DecideSubscription 审批单条申请/邀请（副长以上；invite 由被邀用户确认）。
func (s *Store) DecideSubscription(actorID, subID string, approve bool, opKey string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, actorID, opKeyOr(opKey, "decide:"+subID),
			map[bool]string{true: "approve_sub", false: "reject_sub"}[approve], subID); err != nil {
			return err
		}
		var guildID, subUser, kind string
		var status string
		err := tx.QueryRow(`SELECT guild_id,user_id,kind,status FROM cn35_guild_subscriptions
			WHERE sub_id=?`, subID).Scan(&guildID, &subUser, &kind, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrGuildSubNotFound
		}
		if err != nil {
			return err
		}
		if status != "pending" {
			return cn35game.ErrGuildSubDecided
		}
		if kind == "apply" {
			// 申请者入会：审批人须该会副长以上。
			agid, role, err := memberRoleTx(tx, actorID)
			if err != nil {
				return err
			}
			if agid != guildID || role < 2 {
				return cn35game.ErrGuildNoPerm
			}
		} else if actorID != subUser {
			// invite：只有被邀请人本人可决定。
			return cn35game.ErrGuildNoPerm
		}
		status = "approved"
		if !approve {
			status = "rejected"
		}
		res, err := tx.Exec(`UPDATE cn35_guild_subscriptions
			SET status=?,decided_at=? WHERE sub_id=? AND status='pending'`,
			status, nowUTC(), subID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return cn35game.ErrGuildSubDecided
		}
		if !approve {
			return nil
		}
		if gid, _, err := memberRoleTx(tx, subUser); err != nil {
			return err
		} else if gid != "" {
			return cn35game.ErrGuildJoined
		}
		var cnt int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_members WHERE guild_id=?`,
			guildID).Scan(&cnt); err != nil {
			return err
		}
		if cnt >= cn35game.GuildRules.MaxMembers {
			return cn35game.ErrGuildFull
		}
		g, err := guildRowTx(tx, guildID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_guild_members
			(guild_id,user_id,role,joined_at) VALUES (?,?,1,?)`,
			guildID, subUser, nowUTC()); err != nil {
			return err
		}
		return syncGuildNameTx(tx, subUser, g.Name)
	})
}

// RejectAllSubscriptions /guilds/reject_all_subscription（副长以上）。
func (s *Store) RejectAllSubscriptions(actorID, opKey string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, actorID, opKeyOr(opKey, "reject_all"), "reject_all", ""); err != nil {
			return err
		}
		gid, role, err := memberRoleTx(tx, actorID)
		if err != nil {
			return err
		}
		if gid == "" || role < 2 {
			return cn35game.ErrGuildNoPerm
		}
		_, err = tx.Exec(`UPDATE cn35_guild_subscriptions SET status='rejected',decided_at=?
			WHERE guild_id=? AND status='pending'`, nowUTC(), gid)
		return err
	})
}

// Dropout 退会/踢人（target==actor 自退；否则要求副长以上且目标职位低于操作者）。
func (s *Store) Dropout(actorID, targetUserID, opKey string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, actorID, opKeyOr(opKey, "dropout:"+targetUserID), "dropout", targetUserID); err != nil {
			return err
		}
		gid, actorRole, err := memberRoleTx(tx, actorID)
		if err != nil {
			return err
		}
		if gid == "" {
			return cn35game.ErrGuildNoMember
		}
		target := targetUserID
		if target == "" {
			target = actorID
		}
		tgid, trole, err := memberRoleTx(tx, target)
		if err != nil {
			return err
		}
		if tgid != gid {
			return cn35game.ErrGuildNotFound
		}
		if target != actorID && (actorRole < 2 || actorRole <= trole) {
			return cn35game.ErrGuildNoPerm
		}
		if target == actorID && trole == 3 {
			// 会长退会：禅让给资历最老的副长/成员；无继任则解散公会。
			var heir string
			var heirRole int
			err := tx.QueryRow(`SELECT user_id,role FROM cn35_guild_members
				WHERE guild_id=? AND user_id<>? ORDER BY role DESC, joined_at LIMIT 1`,
				gid, actorID).Scan(&heir, &heirRole)
			if errors.Is(err, sql.ErrNoRows) {
				if _, err := tx.Exec(`DELETE FROM cn35_guild_members WHERE guild_id=?`, gid); err != nil {
					return err
				}
				if _, err := tx.Exec(`DELETE FROM cn35_guilds WHERE guild_id=?`, gid); err != nil {
					return err
				}
				return syncGuildNameTx(tx, actorID, "")
			}
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE cn35_guild_members SET role=3 WHERE user_id=?`, heir); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM cn35_guild_members WHERE user_id=? AND guild_id=?`,
			target, gid); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM cn35_guild_defense WHERE user_id=?`, target); err != nil {
			return err
		}
		return syncGuildNameTx(tx, target, "")
	})
}

// ChangeRole 职位变更：role=3 禅让会长；2 任命副长；1 解除副长。
func (s *Store) ChangeRole(actorID, targetUserID string, role int, opKey string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, actorID, opKeyOr(opKey, "role:"+targetUserID), "change_role",
			fmt.Sprintf("%s:%d", targetUserID, role)); err != nil {
			return err
		}
		gid, actorRole, err := memberRoleTx(tx, actorID)
		if err != nil {
			return err
		}
		if gid == "" {
			return cn35game.ErrGuildNoMember
		}
		tgid, trole, err := memberRoleTx(tx, targetUserID)
		if err != nil {
			return err
		}
		if tgid != gid {
			return cn35game.ErrGuildNotFound
		}
		switch role {
		case 3: // 禅让：仅会长可操作
			if actorRole != 3 || actorID == targetUserID {
				return cn35game.ErrGuildNoPerm
			}
			if _, err := tx.Exec(`UPDATE cn35_guild_members SET role=1 WHERE user_id=?`, actorID); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE cn35_guild_members SET role=3 WHERE user_id=?`, targetUserID); err != nil {
				return err
			}
			// 会长名快照
			var name string
			_ = tx.QueryRow(`SELECT user_name FROM cn35_accounts WHERE user_id=?`, targetUserID).Scan(&name)
			_, err = tx.Exec(`UPDATE cn35_guilds SET updated_at=? WHERE guild_id=?`, nowUTC(), gid)
			return err
		case 2: // 任命副长：会长
			if actorRole != 3 || trole != 1 {
				return cn35game.ErrGuildNoPerm
			}
			_, err = tx.Exec(`UPDATE cn35_guild_members SET role=2 WHERE user_id=?`, targetUserID)
			return err
		case 1: // 解除副长：会长
			if actorRole != 3 || trole != 2 {
				return cn35game.ErrGuildNoPerm
			}
			_, err = tx.Exec(`UPDATE cn35_guild_members SET role=1 WHERE user_id=?`, targetUserID)
			return err
		}
		return cn35game.ErrGuildOp
	})
}

// UpdateGuild /guilds/update（副长以上）。
func (s *Store) UpdateGuild(actorID string, in cn35game.GuildUpdateInput, opKey string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, actorID, opKeyOr(opKey, "update:"+nowUTC()), "update_guild", ""); err != nil {
			return err
		}
		gid, role, err := memberRoleTx(tx, actorID)
		if err != nil {
			return err
		}
		if gid == "" || role < 2 {
			return cn35game.ErrGuildNoPerm
		}
		if in.PlayTypeID != nil {
			if _, err := tx.Exec(`UPDATE cn35_guilds SET play_type_id=?,updated_at=? WHERE guild_id=?`,
				*in.PlayTypeID, nowUTC(), gid); err != nil {
				return err
			}
		}
		if in.ApprovalTypeID != nil {
			if _, err := tx.Exec(`UPDATE cn35_guilds SET approval_type_id=?,updated_at=? WHERE guild_id=?`,
				*in.ApprovalTypeID, nowUTC(), gid); err != nil {
				return err
			}
		}
		if in.RecruitText != nil {
			if _, err := tx.Exec(`UPDATE cn35_guilds SET recruit_text=?,updated_at=? WHERE guild_id=?`,
				*in.RecruitText, nowUTC(), gid); err != nil {
				return err
			}
		}
		if in.Memo != nil {
			if _, err := tx.Exec(`UPDATE cn35_guilds SET memo=?,updated_at=? WHERE guild_id=?`,
				*in.Memo, nowUTC(), gid); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetShifts /guilds/set_shifts（副长以上）。
func (s *Store) SetShifts(actorID string, shifts [7]bool, opKey string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, actorID, opKeyOr(opKey, "shifts:"+nowUTC()), "set_shifts", ""); err != nil {
			return err
		}
		gid, role, err := memberRoleTx(tx, actorID)
		if err != nil {
			return err
		}
		if gid == "" || role < 2 {
			return cn35game.ErrGuildNoPerm
		}
		mask := 0
		for i, on := range shifts {
			if on {
				mask |= 1 << i
			}
		}
		_, err = tx.Exec(`UPDATE cn35_guilds SET shifts=?,updated_at=? WHERE guild_id=?`,
			mask, nowUTC(), gid)
		return err
	})
}

// Invest /guilds/invest：个人资材 → 公会设施 point/level。
func (s *Store) Invest(userID, typeID string, value int64, opKey string) (*cn35game.GuildView, error) {
	if typeID == "" || value <= 0 {
		return nil, cn35game.ErrGuildInvest
	}
	var view *cn35game.GuildView
	err := s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, userID, opKeyOr(opKey, "invest:"+typeID+":"+strconv.FormatInt(value, 10)+":"+nowUTC()),
			"invest", typeID); err != nil {
			return err
		}
		gid, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return err
		}
		if gid == "" {
			return cn35game.ErrGuildNoMember
		}
		// 满级拒绝 + 封顶投入：单笔不超过达上限所需剩余 point。
		var curPoint int64
		err = tx.QueryRow(`SELECT point FROM cn35_guild_equipment
			WHERE guild_id=? AND type_id=?`, gid, typeID).Scan(&curPoint)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		maxPoint := int64(cn35game.GuildRules.EquipMaxLevel) * cn35game.GuildRules.EquipPointPerLevel
		remaining := maxPoint - curPoint
		if remaining <= 0 {
			return cn35game.ErrGuildEquipMax
		}
		if value*cn35game.GuildRules.InvestCostPerPoint > remaining {
			value = remaining / cn35game.GuildRules.InvestCostPerPoint
			if value < 1 {
				return cn35game.ErrGuildInvest
			}
		}
		// 资材余额条件扣减（currency=guild_asset）。
		res, err := tx.Exec(`UPDATE cn35_player_balances
			SET amount=amount-?,updated_at=?
			WHERE user_id=? AND currency='guild_asset' AND amount>=?`,
			value, nowUTC(), userID, value)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return cn35game.ErrGuildAsset
		}
		points := value * cn35game.GuildRules.InvestCostPerPoint
		if _, err := tx.Exec(`INSERT INTO cn35_guild_equipment
			(guild_id,type_id,level,point,updated_at) VALUES (?,?,0,?,?)
			ON CONFLICT(guild_id,type_id) DO UPDATE SET
			point=point+excluded.point,updated_at=excluded.updated_at`,
			gid, typeID, points, nowUTC()); err != nil {
			return err
		}
		var point int64
		if err := tx.QueryRow(`SELECT point FROM cn35_guild_equipment
			WHERE guild_id=? AND type_id=?`, gid, typeID).Scan(&point); err != nil {
			return err
		}
		level := int(point / cn35game.GuildRules.EquipPointPerLevel)
		if level > cn35game.GuildRules.EquipMaxLevel {
			level = cn35game.GuildRules.EquipMaxLevel
		}
		if _, err := tx.Exec(`UPDATE cn35_guild_equipment SET level=? WHERE guild_id=? AND type_id=?`,
			level, gid, typeID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_guild_invest_log
			(guild_id,user_id,type_id,amount,created_at) VALUES (?,?,?,?,?)`,
			gid, userID, typeID, value, nowUTC()); err != nil {
			return err
		}
		v, err := guildViewTx(tx, gid, userID, false)
		if err == nil {
			view = v
		}
		return err
	})
	return view, err
}

// DefenseOf /guilds/deck 读面。
func (s *Store) DefenseOf(userID string) ([]cn35game.GWDefenseSlot, error) {
	rows, err := s.db.Query(`SELECT d.slot,d.monster_uuid,m.monster_id,m.level,d.batch
		FROM cn35_guild_defense d JOIN cn35_player_monsters m
		ON m.user_id=d.user_id AND m.uuid=d.monster_uuid
		WHERE d.user_id=? ORDER BY d.slot`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.GWDefenseSlot{}
	for rows.Next() {
		var d cn35game.GWDefenseSlot
		if err = rows.Scan(&d.Slot, &d.MonsterUUID, &d.MonsterID, &d.Level, &d.Batch); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DefenseSnapshot 目标公会全在籍成员防卫快照（出战冻结用）。
func (s *Store) DefenseSnapshot(guildID string) ([]cn35game.GWDefenseMonster, error) {
	rows, err := s.db.Query(`SELECT d.user_id,d.slot,d.monster_uuid,m.monster_id,m.level
		FROM cn35_guild_defense d JOIN cn35_player_monsters m
		ON m.user_id=d.user_id AND m.uuid=d.monster_uuid
		JOIN cn35_guild_members mem ON mem.user_id=d.user_id
		WHERE mem.guild_id=? ORDER BY d.user_id,d.slot`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.GWDefenseMonster{}
	for rows.Next() {
		var d cn35game.GWDefenseMonster
		if err = rows.Scan(&d.OwnerUserID, &d.Slot, &d.MonsterUUID, &d.MonsterID, &d.Level); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetDefense /guilds/set_deck?party_index=&uuid=。
func (s *Store) SetDefense(userID string, slot int, monsterUUID, opKey string) error {
	if slot < 0 || slot > 4 || monsterUUID == "" {
		return cn35game.ErrGuildDeckSlot
	}
	return s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, userID, opKeyOr(opKey, fmt.Sprintf("def:%d:%s", slot, monsterUUID)),
			"set_defense", monsterUUID); err != nil {
			return err
		}
		if gid, _, err := memberRoleTx(tx, userID); err != nil {
			return err
		} else if gid == "" {
			return cn35game.ErrGuildNoMember
		}
		var mid int
		err := tx.QueryRow(`SELECT monster_id FROM cn35_player_monsters
			WHERE user_id=? AND uuid=?`, userID, monsterUUID).Scan(&mid)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrGuildMonster
		}
		if err != nil {
			return err
		}
		// 同一只魔宠不占两个防卫槽。
		var dupSlot int
		err = tx.QueryRow(`SELECT slot FROM cn35_guild_defense
			WHERE user_id=? AND monster_uuid=? AND slot<>?`, userID, monsterUUID, slot).Scan(&dupSlot)
		if err == nil {
			return cn35game.ErrGuildMonster
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// batch 为客户端同批限制标记（无生产字段证据）：下发固定 0，
		// 占位可调——数值 batching 语义由后续取证补齐。
		_, err = tx.Exec(`INSERT INTO cn35_guild_defense
			(user_id,slot,monster_uuid,batch,updated_at) VALUES (?,?,?,0,?)
			ON CONFLICT(user_id,slot) DO UPDATE SET
			monster_uuid=excluded.monster_uuid,batch=0,updated_at=excluded.updated_at`,
			userID, slot, monsterUUID, nowUTC())
		return err
	})
}

// RemoveDefense /guilds/remove_from_deck?uuid=。
func (s *Store) RemoveDefense(userID, monsterUUID, opKey string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := recordGuildOpTx(tx, userID, opKeyOr(opKey, "undef:"+monsterUUID), "remove_defense", monsterUUID); err != nil {
			return err
		}
		res, err := tx.Exec(`DELETE FROM cn35_guild_defense WHERE user_id=? AND monster_uuid=?`,
			userID, monsterUUID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return cn35game.ErrGuildMonster
		}
		return nil
	})
}

// ---- 会战 ----

// settleDueSessionsTx 关闭并结算所有已过窗口的 open 会话（惰性结算：
// 首个越过终点的请求触发；幂等——settled 行 PK 保证单次发奖）。
func (s *Store) settleDueSessionsTx(tx *sql.Tx, now time.Time) error {
	local := now.In(guildLoc)
	today := local.Format("2006-01-02")
	hm := local.Format("15:04:05")
	rows, err := tx.Query(`SELECT session_id,date_key,field_index FROM cn35_gw_sessions
		WHERE status='open'`)
	if err != nil {
		return err
	}
	type due struct {
		id, dk string
		fi     int
	}
	var dues []due
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.id, &d.dk, &d.fi); err != nil {
			rows.Close()
			return err
		}
		if d.dk < today ||
			(d.dk == today && d.fi < len(cn35game.GuildRules.Fields) &&
				cn35game.GuildRules.Fields[d.fi].EndHM <= hm) {
			dues = append(dues, d)
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, d := range dues {
		res, err := tx.Exec(`UPDATE cn35_gw_sessions SET status='closed'
			WHERE session_id=? AND status='open'`, d.id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // 并发已收
		}
		// prepared（开战未扣 BP 确认）随关场作废；bp_paid 是已进入
		// 战斗中的出击——会战无战斗限时，玩家可能在关场后才清完
		// 大怪上报胜负，保留 bp_paid 供 GWAttackSettle 终结结算。
		if _, err := tx.Exec(`UPDATE cn35_gw_attacks SET status='abandoned',settled_at=?
			WHERE session_id=? AND status='prepared'`,
			nowUTC(), d.id); err != nil {
			return err
		}
		// 组内名次（GP 降序，同分按加入序）。
		type part struct {
			gid string
			gp  int64
		}
		pr, err := tx.Query(`SELECT guild_id,gp FROM cn35_gw_participants
			WHERE session_id=?`, d.id)
		if err != nil {
			return err
		}
		var parts []part
		for pr.Next() {
			var p part
			if err = pr.Scan(&p.gid, &p.gp); err != nil {
				pr.Close()
				return err
			}
			parts = append(parts, p)
		}
		if err = pr.Close(); err != nil {
			return err
		}
		sort.Slice(parts, func(i, j int) bool {
			if parts[i].gp != parts[j].gp {
				return parts[i].gp > parts[j].gp
			}
			return parts[i].gid < parts[j].gid // 同分确定性次序
		})
		winner := ""
		for i, p := range parts {
			if i == 0 {
				winner = p.gid
			}
			if _, err := tx.Exec(`UPDATE cn35_gw_participants SET rank_in_group=?
				WHERE session_id=? AND guild_id=?`, i+1, d.id, p.gid); err != nil {
				return err
			}
			rewards, _ := json.Marshal([]cn35game.BattleReward{
				{Key: "guild_asset", Count: cn35game.GuildRules.RewardGuildAsset},
			})
			if _, err := tx.Exec(`INSERT INTO cn35_gw_settlements
				(session_id,guild_id,rewards_json,created_at) VALUES (?,?,?,?)
				ON CONFLICT(session_id,guild_id) DO NOTHING`,
				d.id, p.gid, string(rewards), nowUTC()); err != nil {
				return err
			}
			// gp 列是「未结场累计」读面：只减本场入账，误清并行的后续场次。
			if _, err := tx.Exec(`UPDATE cn35_guilds SET gp_total=gp_total+?,gp=MAX(0,gp-?),updated_at=?
				WHERE guild_id=?`, p.gp, p.gp, nowUTC(), p.gid); err != nil {
				return err
			}
			// 胜场公会资材发到成员（占位：会长领取入口外，直接入全员余额）。
			if p.gid == winner {
				mr, err := tx.Query(`SELECT user_id FROM cn35_gw_member_scores
					WHERE session_id=? AND guild_id=?`, d.id, p.gid)
				if err != nil {
					return err
				}
				var winners []string
				for mr.Next() {
					var u string
					if err = mr.Scan(&u); err != nil {
						mr.Close()
						return err
					}
					winners = append(winners, u)
				}
				if err = mr.Close(); err != nil {
					return err
				}
				for _, u := range winners {
					if err := creditBalanceTx(tx, u, "guild_asset", int64(cn35game.GuildRules.RewardGuildAsset)); err != nil {
						return err
					}
				}
			}
		}
		if _, err := tx.Exec(`UPDATE cn35_gw_sessions SET status='settled',winner_guild_id=?
			WHERE session_id=?`, winner, d.id); err != nil {
			return err
		}
	}
	return nil
}

// ensureGWSessionTx 当前时窗内为公会匹配/建场，返回场次+组。
// 参会条件：公会 shifts 当日勾选且有成员（shifts 掩码 bit=weekday）。
func (s *Store) ensureGWSessionTx(tx *sql.Tx, guildID string, local time.Time) (*cn35game.GWSession, string, error) {
	fi := cn35game.GuildFieldIndex(local)
	if fi < 0 {
		return nil, "", nil
	}
	var shifts int
	var memberCnt int
	if err := tx.QueryRow(`SELECT shifts FROM cn35_guilds WHERE guild_id=?`,
		guildID).Scan(&shifts); err != nil {
		return nil, "", err
	}
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_members WHERE guild_id=?`,
		guildID).Scan(&memberCnt); err != nil {
		return nil, "", err
	}
	if memberCnt == 0 || shifts&(1<<int(local.Weekday())) == 0 {
		return nil, "", nil
	}
	dateKey := local.Format("2006-01-02")
	// 已参会？
	var sessID, groupKey string
	err := tx.QueryRow(`SELECT p.session_id,s.group_key FROM cn35_gw_participants p
		JOIN cn35_gw_sessions s ON s.session_id=p.session_id
		WHERE p.guild_id=? AND s.date_key=? AND s.field_index=?`,
		guildID, dateKey, fi).Scan(&sessID, &groupKey)
	if err == nil {
		return &cn35game.GWSession{SessionID: sessID, DateKey: dateKey, FieldIndex: fi, GroupKey: groupKey, Status: "open"}, groupKey, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	// 匹配：当日该时段尚未入组的参会公会，按建会序取 GroupSize-1 个。
	cand, err := tx.Query(`SELECT g.guild_id FROM cn35_guilds g
		WHERE g.guild_id<>? AND (g.shifts & ?)<>0
		AND EXISTS(SELECT 1 FROM cn35_guild_members m WHERE m.guild_id=g.guild_id)
		AND NOT EXISTS(
			SELECT 1 FROM cn35_gw_participants p JOIN cn35_gw_sessions s ON s.session_id=p.session_id
			WHERE p.guild_id=g.guild_id AND s.date_key=? AND s.field_index=?)
		ORDER BY g.created_at LIMIT ?`,
		guildID, 1<<int(local.Weekday()), dateKey, fi, cn35game.GuildRules.GroupSize-1)
	if err != nil {
		return nil, "", err
	}
	group := []string{guildID}
	for cand.Next() {
		var gid string
		if err = cand.Scan(&gid); err != nil {
			cand.Close()
			return nil, "", err
		}
		group = append(group, gid)
	}
	if err = cand.Close(); err != nil {
		return nil, "", err
	}
	sort.Strings(group)
	groupKey = strings.Join(group, "+")
	sessID = "gws" + randHex(12)
	if _, err := tx.Exec(`INSERT INTO cn35_gw_sessions
		(session_id,date_key,field_index,group_key,status,created_at)
		VALUES (?,?,?,?,'open',?)`, sessID, dateKey, fi, groupKey, nowUTC()); err != nil {
		// 并发已建同组场次：回填既有行。
		if !strings.Contains(err.Error(), "UNIQUE") {
			return nil, "", err
		}
		if err := tx.QueryRow(`SELECT session_id FROM cn35_gw_sessions
			WHERE date_key=? AND field_index=? AND group_key=?`,
			dateKey, fi, groupKey).Scan(&sessID); err != nil {
			return nil, "", err
		}
	}
	for i, gid := range group {
		color := cn35game.GuildRules.Colors[i%len(cn35game.GuildRules.Colors)]
		if _, err := tx.Exec(`INSERT INTO cn35_gw_participants
			(session_id,guild_id,color,gp,rank_in_group,seed_powerup_rate)
			VALUES (?,?,?,0,0,0) ON CONFLICT(session_id,guild_id) DO NOTHING`,
			sessID, gid, color); err != nil {
			return nil, "", err
		}
	}
	for _, gid := range group {
		_, err := tx.Exec(`UPDATE cn35_gw_participants SET
   initial_sp=COALESCE((SELECT p.initial_sp FROM cn35_gw_participants p JOIN cn35_gw_sessions s ON s.session_id=p.session_id WHERE p.guild_id=? AND s.date_key=? AND s.field_index<? ORDER BY s.field_index DESC LIMIT 1),0),
   seed_powerup_rate=COALESCE((SELECT p.seed_powerup_rate/4 FROM cn35_gw_participants p JOIN cn35_gw_sessions s ON s.session_id=p.session_id WHERE p.guild_id=? AND s.date_key=? AND s.field_index<? ORDER BY s.field_index DESC LIMIT 1),0)
   WHERE session_id=? AND guild_id=?`, gid, dateKey, fi, gid, dateKey, fi, sessID, gid)
		if err != nil {
			return nil, "", err
		}
	}

	return &cn35game.GWSession{SessionID: sessID, DateKey: dateKey, FieldIndex: fi, GroupKey: groupKey, Status: "open"}, groupKey, nil
}

// GWState 先结算，再匹配；窗外保留最近场次结果。
func (s *Store) GWState(userID string, now time.Time) (*cn35game.GWView, error) {
	local := now.In(guildLoc)
	view := &cn35game.GWView{FieldIndex: cn35game.GuildFieldIndex(local), SeedPowerup: 1}
	err := s.withTx(func(tx *sql.Tx) error {
		if err := s.settleDueSessionsTx(tx, now); err != nil {
			return err
		}
		gid, _, err := memberRoleTx(tx, userID)
		if err != nil || gid == "" {
			return err
		}
		sess, _, err := s.ensureGWSessionTx(tx, gid, local)
		if err != nil {
			return err
		}
		if sess == nil {
			sess = &cn35game.GWSession{}
			err = tx.QueryRow(`SELECT s.session_id,s.date_key,s.field_index,s.group_key,s.status
    FROM cn35_gw_sessions s JOIN cn35_gw_participants p ON p.session_id=s.session_id
    WHERE p.guild_id=? AND s.date_key<=? ORDER BY s.date_key DESC,s.field_index DESC LIMIT 1`,
				gid, local.Format("2006-01-02")).Scan(&sess.SessionID, &sess.DateKey, &sess.FieldIndex, &sess.GroupKey, &sess.Status)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		view.Session = sess
		view.Actable = sess.Status == "open" && sess.DateKey == local.Format("2006-01-02") && sess.FieldIndex == view.FieldIndex
		if err := tx.QueryRow(`SELECT initial_sp,seed_powerup_rate,help_count,pray_count FROM cn35_gw_participants WHERE session_id=? AND guild_id=?`, sess.SessionID, gid).Scan(&view.Power, &view.SeedPowerup, &view.HelpCount, &view.PrayCount); err != nil {
			return err
		}
		view.SeedPowerup += 1
		pr, err := tx.Query(`SELECT p.guild_id,g.name,p.color,g.rank_id,p.gp,p.rank_in_group
   FROM cn35_gw_participants p JOIN cn35_guilds g ON g.guild_id=p.guild_id
   WHERE p.session_id=? ORDER BY p.gp DESC,p.guild_id`, sess.SessionID)
		if err != nil {
			return err
		}
		for pr.Next() {
			var p cn35game.GWParticipant
			if err = pr.Scan(&p.GuildID, &p.Name, &p.Color, &p.RankID, &p.GP, &p.RankInGroup); err != nil {
				pr.Close()
				return err
			}
			p.Joined = p.GuildID == gid
			p.RankInGroup = len(view.Participants) + 1
			view.Participants = append(view.Participants, p)
		}
		return pr.Close()
	})
	return view, err
}

// bpSpendTx BP 条件扣减。
func bpSpendTx(tx *sql.Tx, userID string, cost int) error {
	res, err := tx.Exec(`UPDATE cn35_player_profiles SET bp=bp-?,updated_at=?
		WHERE user_id=? AND bp>=?`, cost, nowUTC(), userID, cost)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return cn35game.ErrGWBP
	}
	return nil
}

// gwMySessionTx 校验用户处于 open 场次（返回 session_id/己方 guild_id）。
func gwMySessionTx(tx *sql.Tx, userID string, local time.Time) (sessID, guildID string, err error) {
	guildID, _, err = memberRoleTx(tx, userID)
	if err != nil || guildID == "" {
		return "", "", cn35game.ErrGuildNoMember
	}
	fi := cn35game.GuildFieldIndex(local)
	if fi < 0 {
		return "", "", cn35game.ErrGWNoSession
	}
	err = tx.QueryRow(`SELECT p.session_id FROM cn35_gw_participants p
		JOIN cn35_gw_sessions s ON s.session_id=p.session_id
		WHERE p.guild_id=? AND s.date_key=? AND s.field_index=? AND s.status='open'`,
		guildID, local.Format("2006-01-02"), fi).Scan(&sessID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", cn35game.ErrGWNoSession
	}
	return sessID, guildID, err
}

// GWHelpPray 无键为新点击；显式键在同场同动作下回放，异参拒绝。
func (s *Store) GWHelpPray(userID, action, opKey string, now time.Time) (int64, int, error) {
	if action != "help" && action != "pray" {
		return 0, 0, cn35game.ErrGuildOp
	}
	local := now.In(guildLoc)
	var gp int64
	var cost int
	err := s.withTx(func(tx *sql.Tx) error {
		sessID, gid, err := gwMySessionTx(tx, userID, local)
		if err != nil {
			return err
		}
		f := cn35game.GuildRules.Fields[cn35game.GuildFieldIndex(local)]
		cost, gp = f.PrayBP, int64(f.PrayGP)
		if action == "help" {
			cost, gp = f.HelpBP, int64(f.HelpGP)
		}
		key := opKeyOr(opKey, action+":"+sessID)
		var oldSession, oldAction string
		err = tx.QueryRow(`SELECT session_id,action,gp_delta,bp_cost FROM cn35_gw_actions WHERE user_id=? AND op_key=?`, userID, key).Scan(&oldSession, &oldAction, &gp, &cost)
		if err == nil {
			if oldSession != sessID || oldAction != action {
				return cn35game.ErrGuildOp
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = bpSpendTx(tx, userID, cost); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO cn35_gw_actions (session_id,user_id,op_key,action,bp_cost,gp_delta,created_at) VALUES (?,?,?,?,?,?,?)`, sessID, userID, key, action, cost, gp, nowUTC()); err != nil {
			return err
		}
		sp, hc, pc := 0, 0, 0
		bonus := 0.0
		if action == "help" {
			sp = cn35game.GuildRules.HelpSP
			hc = 1
		} else {
			pc = 1
			bonus = cn35game.GuildRules.PrayHPBonus
		}
		if _, err = tx.Exec(`UPDATE cn35_gw_participants SET gp=gp+?,initial_sp=initial_sp+?,help_count=help_count+?,pray_count=pray_count+?,seed_powerup_rate=seed_powerup_rate+? WHERE session_id=? AND guild_id=?`, gp, sp, hc, pc, bonus, sessID, gid); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE cn35_guilds SET gp=gp+?,updated_at=? WHERE guild_id=?`, gp, nowUTC(), gid); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO cn35_gw_member_scores (session_id,user_id,guild_id,attack_count,win_count,gp_earned,updated_at) VALUES (?,?,?,0,0,?,?) ON CONFLICT(session_id,user_id) DO UPDATE SET gp_earned=gp_earned+excluded.gp_earned,updated_at=excluded.updated_at`, sessID, userID, gid, gp, nowUTC())
		return err
	})
	return gp, cost, err
}

type gwPartySnapshot struct {
	PartyID string                   `json:"party_id"`
	Units   []cn35game.UnitRecord    `json:"units"`
	Seeds   []cn35game.MonsterRecord `json:"seeds"`
}

// GWAttackOpen 开战建档：时窗/同组/BP/目标校验 + 防卫快照 + 扣 BP 同事务。
func (s *Store) GWAttackOpen(userID, targetGuildID string, extraTargets []string, difficulty string,
	ordinal, unitLimit int, opKey string, now time.Time) (*cn35game.GWAttack, []cn35game.UnitRecord, []cn35game.MonsterRecord, error) {
	local := now.In(guildLoc)
	switch difficulty {
	case "normal", "hard", "very_hard":
	case "":
		difficulty = "normal"
	default:
		return nil, nil, nil, cn35game.ErrGWDifficulty
	}
	targets := append([]string{targetGuildID}, extraTargets...)
	if len(targets) > 3 {
		return nil, nil, nil, cn35game.ErrGWTarget
	}
	requestBytes, err := json.Marshal(struct {
		Targets        []string
		Difficulty     string
		Ordinal, Limit int
	}{targets, difficulty, ordinal, unitLimit})
	if err != nil {
		return nil, nil, nil, err
	}
	request := string(requestBytes)
	var atk *cn35game.GWAttack
	var party gwPartySnapshot
	err = s.withTx(func(tx *sql.Tx) error {
		// 显式操作键跨终态也返回原战斗，不依赖当前日期/队伍状态。
		if opKey != "" {
			var op, payload, bid string
			e := tx.QueryRow(`SELECT op,payload_json,result_json FROM cn35_guild_ops WHERE user_id=? AND op_key=?`, userID, opKey).Scan(&op, &payload, &bid)
			if e == nil {
				if op != "gw_attack_open" || payload != request {
					return cn35game.ErrGuildOp
				}
				var e error
				atk, e = gwAttackScan(tx.QueryRow(`SELECT `+gwAttackCols+` FROM cn35_gw_attacks WHERE battle_id=? AND user_id=?`, bid, userID))
				if e != nil {
					return e
				}
				return json.Unmarshal([]byte(atk.PartyJSON), &party)
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		sessID, gid, err := gwMySessionTx(tx, userID, local)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, target := range targets {
			if target == "" || target == gid || seen[target] {
				return cn35game.ErrGWTarget
			}
			seen[target] = true
			var count int
			if err = tx.QueryRow(`SELECT count(*) FROM cn35_gw_participants WHERE session_id=? AND guild_id=?`, sessID, target).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return cn35game.ErrGWTarget
			}
		}
		// 原客户端没有 op_key：未结束的同参数请求可恢复。不同参数禁止叠加扣费。
		active, e := gwAttackScan(tx.QueryRow(`SELECT `+gwAttackCols+` FROM cn35_gw_attacks WHERE user_id=? AND session_id=? AND status IN ('prepared','bp_paid') ORDER BY created_at DESC,battle_id DESC LIMIT 1`, userID, sessID))
		if e == nil {
			if active.RequestJSON != request {
				return cn35game.ErrGWAttackState
			}
			atk = active
			if err = json.Unmarshal([]byte(atk.PartyJSON), &party); err != nil {
				return err
			}
		} else {
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if err = s.ensureDefaultDecksTx(tx, userID); err != nil {
				return err
			}
			party.PartyID, party.Units, party.Seeds, err = resolvePartyTx(tx, userID, ordinal, unitLimit)
			if err != nil {
				return err
			}
			var sp int
			if err = tx.QueryRow(`SELECT initial_sp FROM cn35_gw_participants WHERE session_id=? AND guild_id=?`, sessID, gid).Scan(&sp); err != nil {
				return err
			}
			effects := cn35game.GWEffects{Version: 1, InitialSP: 500 + sp, UnitHP: 1, UnitAT: 1, UnitToughness: 1}
			eq, err := tx.Query(`SELECT type_id,level FROM cn35_guild_equipment WHERE guild_id=?`, gid)
			if err != nil {
				return err
			}
			for eq.Next() {
				var typ string
				var level int
				if err = eq.Scan(&typ, &level); err != nil {
					eq.Close()
					return err
				}
				bonus := float64(level) * cn35game.GuildRules.EquipUnitBonusPerLevel
				switch typ {
				case "sp":
					effects.InitialSP += level * cn35game.GuildRules.EquipSPPerLevel
				case "hp":
					effects.UnitHP += bonus
				case "at":
					effects.UnitAT += bonus
				case "taghness":
					effects.UnitToughness += bonus
				}
			}
			if err = eq.Close(); err != nil {
				return err
			}
			snapshot := []cn35game.GWDefenseMonster{}
			for _, target := range targets {
				var prayer float64
				if err = tx.QueryRow(`SELECT seed_powerup_rate FROM cn35_gw_participants WHERE session_id=? AND guild_id=?`, sessID, target).Scan(&prayer); err != nil {
					return err
				}
				rows, err := tx.Query(`SELECT d.user_id,d.slot,d.monster_uuid,m.monster_id,m.level FROM cn35_guild_defense d JOIN cn35_player_monsters m ON m.user_id=d.user_id AND m.uuid=d.monster_uuid JOIN cn35_guild_members mem ON mem.user_id=d.user_id WHERE mem.guild_id=? ORDER BY d.user_id,d.slot`, target)
				if err != nil {
					return err
				}
				before := len(snapshot)
				for rows.Next() {
					var d cn35game.GWDefenseMonster
					if err = rows.Scan(&d.OwnerUserID, &d.Slot, &d.MonsterUUID, &d.MonsterID, &d.Level); err != nil {
						rows.Close()
						return err
					}
					d.GuildID = target
					d.HPBonus = prayer
					if cn35game.GuildRules.PrayHPBonus > 0 {
						d.ATBonus = prayer * cn35game.GuildRules.PrayATBonus / cn35game.GuildRules.PrayHPBonus
					}
					snapshot = append(snapshot, d)
				}
				if err = rows.Close(); err != nil {
					return err
				}
				if len(snapshot) == before {
					return cn35game.ErrGWNoDefense
				}
			}
			cost := cn35game.GuildRules.Fields[cn35game.GuildFieldIndex(local)].AttackBPCost(len(targets))
			if err = bpSpendTx(tx, userID, cost); err != nil {
				return err
			}
			snapJSON, _ := json.Marshal(snapshot)
			partyJSON, _ := json.Marshal(party)
			targetsJSON, _ := json.Marshal(targets)
			effectsJSON, _ := json.Marshal(effects)
			bid := "bg" + randHex(16)
			if _, err = tx.Exec(`INSERT INTO cn35_gw_attacks (battle_id,session_id,user_id,guild_id,target_guild_id,difficulty,status,bp_cost,snapshot_json,party_json,created_at,targets_json,effects_json,request_json) VALUES (?,?,?,?,?,?,'prepared',?,?,?,?,?,?,?)`, bid, sessID, userID, gid, targetGuildID, difficulty, cost, string(snapJSON), string(partyJSON), nowUTC(), string(targetsJSON), string(effectsJSON), request); err != nil {
				return err
			}
			atk = &cn35game.GWAttack{BattleID: bid, SessionID: sessID, UserID: userID, GuildID: gid, TargetGuildID: targetGuildID, TargetGuildIDs: targets, Difficulty: difficulty, Status: "prepared", BPCost: cost, DefenseSnapshot: snapshot, PartyJSON: string(partyJSON), Effects: effects, RequestJSON: request}
		}
		// 有键重试与无键恢复共用同一结果。
		if opKey != "" {
			_, err = tx.Exec(`INSERT INTO cn35_guild_ops (user_id,op_key,op,payload_json,result_json,created_at) VALUES (?,?,'gw_attack_open',?,?,?)`, userID, opKey, request, atk.BattleID, nowUTC())
			return err
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return atk, party.Units, party.Seeds, nil
}

// gwAttackRowTx / gwAttackRow 会话行读取。
func gwAttackScan(row interface{ Scan(...any) error }) (*cn35game.GWAttack, error) {
	var a cn35game.GWAttack
	var snap, party, targets, effects string
	err := row.Scan(&a.BattleID, &a.SessionID, &a.UserID, &a.GuildID,
		&a.TargetGuildID, &a.Difficulty, &a.Status, &a.BPCost, &snap, &party, &targets, &effects, &a.RequestJSON)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(snap), &a.DefenseSnapshot); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(targets), &a.TargetGuildIDs); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(effects), &a.Effects); err != nil {
		return nil, err
	}
	if len(a.TargetGuildIDs) == 0 {
		a.TargetGuildIDs = []string{a.TargetGuildID}
	}
	a.PartyJSON = party
	return &a, nil
}

const gwAttackCols = `battle_id,session_id,user_id,guild_id,target_guild_id,difficulty,status,bp_cost,snapshot_json,party_json,targets_json,effects_json,request_json`

// GWAttackSession 读取归属校验（跨账号同形 ErrGWAttackNotFound）。
func (s *Store) GWAttackSession(userID, battleID string) (*cn35game.GWAttack, error) {
	a, err := gwAttackScan(s.db.QueryRow(`SELECT `+gwAttackCols+`
		FROM cn35_gw_attacks WHERE battle_id=? AND user_id=?`, battleID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, cn35game.ErrGWAttackNotFound
	}
	return a, err
}

// GWAttackPay prepared→bp_paid（BP 已在开战扣；重放/终态视为成功）。
func (s *Store) GWAttackPay(userID, battleID string) (*cn35game.GWAttack, error) {
	var a *cn35game.GWAttack
	if err := s.withTx(func(tx *sql.Tx) error { return s.settleDueSessionsTx(tx, s.clockNowUTC()) }); err != nil {
		return nil, err
	}
	err := s.withTx(func(tx *sql.Tx) error {
		row, err := gwAttackScan(tx.QueryRow(`SELECT `+gwAttackCols+`
			FROM cn35_gw_attacks WHERE battle_id=? AND user_id=?`, battleID, userID))
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrGWAttackNotFound
		}
		if err != nil {
			return err
		}
		a = row
		if a.Status == "abandoned" {
			return cn35game.ErrGWAttackState
		}
		if a.Status == "prepared" {
			if _, err := tx.Exec(`UPDATE cn35_gw_attacks SET status='bp_paid'
				WHERE battle_id=? AND status='prepared'`, battleID); err != nil {
				return err
			}
			a.Status = "bp_paid"
		}
		return nil
	})
	return a, err
}

// GWAttackSettle 胜/负结算：GP 入账 + 个人分 + 奖励，条件 UPDATE 幂等。
func (s *Store) GWAttackSettle(userID, battleID string, win bool, reportTime int64) (*cn35game.GWSettleResult, error) {
	res := &cn35game.GWSettleResult{Win: win}
	if err := s.withTx(func(tx *sql.Tx) error { return s.settleDueSessionsTx(tx, s.clockNowUTC()) }); err != nil {
		return nil, err
	}
	err := s.withTx(func(tx *sql.Tx) error {
		// 先惰性关场：逾窗攻击已作废，拒绝迟入奖励。
		if err := s.settleDueSessionsTx(tx, s.clockNowUTC()); err != nil {
			return err
		}
		a, err := gwAttackScan(tx.QueryRow(`SELECT `+gwAttackCols+`
			FROM cn35_gw_attacks WHERE battle_id=? AND user_id=?`, battleID, userID))
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrGWAttackNotFound
		}
		if err != nil {
			return err
		}
		res.Attack = a
		newStatus := "lost"
		gp := cn35game.GuildRules.AttackGPLose
		if win {
			newStatus = "cleared"
			gp = cn35game.GuildRules.AttackGPWin
		}
		// 仅 bp_paid（走过 use_ap）可结算——prepared 直报不发奖。
		up, err := tx.Exec(`UPDATE cn35_gw_attacks SET status=?,settled_at=?
			WHERE battle_id=? AND status='bp_paid'`,
			newStatus, nowUTC(), battleID)
		if err != nil {
			return err
		}
		if n, _ := up.RowsAffected(); n == 0 {
			if a.Status == "cleared" || a.Status == "lost" {
				res.Already = true
				res.GPDelta = 0
				return nil
			}
			return cn35game.ErrGWAttackState
		}
		a.Status = newStatus
		res.GPDelta = gp
		if _, err := tx.Exec(`UPDATE cn35_gw_participants SET gp=gp+?
			WHERE session_id=? AND guild_id=?`, gp, a.SessionID, a.GuildID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE cn35_guilds SET gp=gp+?,updated_at=?
			WHERE guild_id=?`, gp, nowUTC(), a.GuildID); err != nil {
			return err
		}
		// 胜场掠夺：按被击败目标本场当前 GP 的比例转移给胜者。
		if plunderRate := cn35game.GuildRules.DifficultyPlunderRate[a.Difficulty]; win && plunderRate > 0 {
			for _, target := range a.TargetGuildIDs {
				var targetGP int64
				err = tx.QueryRow(`SELECT gp FROM cn35_gw_participants
				WHERE session_id=? AND guild_id=?`, a.SessionID, target).Scan(&targetGP)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				plunder := int64(float64(targetGP) * plunderRate)
				if plunder > targetGP {
					plunder = targetGP
				}
				if plunder > 0 {
					res.Plundered += plunder
					if _, err := tx.Exec(`UPDATE cn35_gw_participants SET gp=gp-?
					WHERE session_id=? AND guild_id=? AND gp>=?`,
						plunder, a.SessionID, target, plunder); err != nil {
						return err
					}
					if _, err := tx.Exec(`UPDATE cn35_gw_participants SET gp=gp+?
					WHERE session_id=? AND guild_id=?`,
						plunder, a.SessionID, a.GuildID); err != nil {
						return err
					}
					if _, err := tx.Exec(`UPDATE cn35_guilds SET gp=MAX(0,gp-?),updated_at=?
					WHERE guild_id=?`, plunder, nowUTC(), target); err != nil {
						return err
					}
					if _, err := tx.Exec(`UPDATE cn35_guilds SET gp=gp+?,updated_at=?
					WHERE guild_id=?`, plunder, nowUTC(), a.GuildID); err != nil {
						return err
					}
				}
			}
		}
		winCnt := 0
		if win {
			winCnt = 1
		}
		if _, err := tx.Exec(`INSERT INTO cn35_gw_member_scores
			(session_id,user_id,guild_id,attack_count,win_count,gp_earned,updated_at)
			VALUES (?,?,?,1,?,?,?)
			ON CONFLICT(session_id,user_id) DO UPDATE SET
			attack_count=attack_count+1,win_count=win_count+excluded.win_count,
			gp_earned=gp_earned+excluded.gp_earned,updated_at=excluded.updated_at`,
			a.SessionID, userID, a.GuildID, winCnt, gp+res.Plundered, nowUTC()); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_gw_actions
			(session_id,user_id,op_key,action,target_guild_id,bp_cost,gp_delta,result,created_at)
			VALUES (?,?,?,'attack',?,?,?,?,?)
			ON CONFLICT(user_id,op_key) DO NOTHING`,
			a.SessionID, userID, "atk:"+battleID, a.TargetGuildID, a.BPCost, gp+res.Plundered, newStatus, nowUTC()); err != nil {
			return err
		}
		if win {
			res.Rewards = []cn35game.BattleReward{
				{Key: "guild_asset", Count: cn35game.GuildRules.RewardGuildAsset},
			}
			if err := creditBalanceTx(tx, userID, "guild_asset",
				int64(cn35game.GuildRules.RewardGuildAsset)); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}

// GWRankings /guild_rankings：GP 总量降序。
func (s *Store) GWRankings() ([]cn35game.Guild, error) {
	rows, err := s.db.Query(`SELECT g.guild_id,g.name,g.rank_id,g.rating,g.gp_total,
		(SELECT count(*) FROM cn35_guild_members m WHERE m.guild_id=g.guild_id)
		FROM cn35_guilds g ORDER BY g.gp_total DESC, g.created_at LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.Guild{}
	rank := int64(0)
	for rows.Next() {
		var g cn35game.Guild
		if err = rows.Scan(&g.ID, &g.Name, &g.RankID, &g.Rating, &g.Point, &g.MemberCount); err != nil {
			return nil, err
		}
		rank++
		g.Rating = rank // 名次投影进 rating 位由 wire 层决定
		out = append(out, g)
	}
	return out, rows.Err()
}
