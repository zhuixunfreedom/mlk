// adminview.go 管理后台数据读面（cn35game.AdminViewStore 的 SQLite 实现）。
//
// 全部为既有 cn35_* 表的只读 SELECT：不写库、不开事务（单语句读
// 在 WAL 下自一致）。玩家账号敏感列（token/sk/password_hash）不出现在
// 任何投影中；名称为空串的账号按 guest 语义显示。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

const (
	adminDefaultPageSize = 50
	adminMaxPageSize     = 200
	adminLikeMax         = 64
)

// adminPage 归一化分页：page<1→1；size<=0→默认；超上限截断。
func adminPage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = adminDefaultPageSize
	}
	if size > adminMaxPageSize {
		size = adminMaxPageSize
	}
	return page, size
}

// adminLike 截断模糊匹配输入并转义 LIKE 通配符（%_\\），
// 用户输入按字面子串匹配；配合 WHERE 子句的 ESCAPE '\'。
func adminLike(s string) string {
	if r := []rune(s); len(r) > adminLikeMax {
		s = string(r[:adminLikeMax])
	}
	s = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
	return "%" + s + "%"
}

// elementName 1炎2水3风4光5暗（cardcatalog.Unit.Element 枚举注释同口径）。
func elementName(e int) string {
	switch e {
	case 1:
		return "炎"
	case 2:
		return "水"
	case 3:
		return "风"
	case 4:
		return "光"
	case 5:
		return "暗"
	}
	return ""
}

// accountKind 由账号列推导账号类型：SDK 通道 > HE 注册号 > 游客。
func accountKind(accountName, sdkID string) string {
	switch {
	case sdkID != "":
		return "sdk"
	case accountName != "":
		return "he"
	default:
		return "guest"
	}
}

func (s *Store) AdminOverview() (*cn35game.AdminOverview, error) {
	o := &cn35game.AdminOverview{}
	counts := []struct {
		dst *int64
		sql string
	}{
		{&o.Accounts, `SELECT COUNT(*) FROM cn35_accounts`},
		{&o.Units, `SELECT COUNT(*) FROM cn35_player_units`},
		{&o.Monsters, `SELECT COUNT(*) FROM cn35_player_monsters`},
		{&o.Presents, `SELECT COUNT(*) FROM cn35_user_presents WHERE received_at=0`},
		{&o.Infos, `SELECT COUNT(*) FROM cn35_infos`},
		{&o.GachaOps, `SELECT COUNT(*) FROM cn35_gacha_operations`},
		{&o.Decks, `SELECT COUNT(*) FROM cn35_player_decks`},
	}
	for _, c := range counts {
		if err := s.db.QueryRow(c.sql).Scan(c.dst); err != nil {
			return nil, err
		}
	}
	return o, nil
}

const adminPlayerCols = `a.user_id,a.user_name,a.account_name,a.sdk_account_id,
	a.is_new_user,a.tutorial_finish,a.last_server_id,a.created_at,a.updated_at,
	COALESCE(p.level,0),COALESCE(p.exp,0),COALESCE(p.ap,0),COALESCE(p.bp,0),
	COALESCE((SELECT amount FROM cn35_player_balances b WHERE b.user_id=a.user_id AND b.currency='coin'),0),
	COALESCE((SELECT amount FROM cn35_player_balances b WHERE b.user_id=a.user_id AND b.currency='gacha_point'),0)`

// scanPlayerRow 读一行账号+资料+余额投影；coin/gp 无行回退 0（与 Snapshot 口径一致）。
func scanPlayerRow(row interface{ Scan(...any) error }) (cn35game.AdminPlayerRow, error) {
	var r cn35game.AdminPlayerRow
	var accountName, sdkID string
	var isNew, tut int
	err := row.Scan(&r.UserID, &r.Nickname, &accountName, &sdkID,
		&isNew, &tut, &r.LastServerID, &r.CreatedAt, &r.UpdatedAt,
		&r.Level, &r.Exp, &r.AP, &r.BP, &r.Coin, &r.GachaPoint)
	if err != nil {
		return r, err
	}
	r.AccountKind = accountKind(accountName, sdkID)
	r.TutorialDone = tut == 1
	_ = isNew // 详情 raw 面再带
	return r, nil
}

func (s *Store) AdminPlayers(f cn35game.AdminPlayerFilter) ([]cn35game.AdminPlayerRow, int, error) {
	page, size := adminPage(f.Page, f.PageSize)
	where := `WHERE (?='' OR a.user_id LIKE ?) AND (?='' OR a.user_name LIKE ?)`
	args := []any{f.UID, adminLike(f.UID), f.Nickname, adminLike(f.Nickname)}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cn35_accounts a `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT `+adminPlayerCols+`
		FROM cn35_accounts a LEFT JOIN cn35_player_profiles p ON p.user_id=a.user_id
		`+where+` ORDER BY a.created_at, a.user_id LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []cn35game.AdminPlayerRow{}
	for rows.Next() {
		r, err := scanPlayerRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *Store) AdminPlayerDetail(userID string) (*cn35game.AdminPlayerDetail, error) {
	row, err := scanPlayerRow(s.db.QueryRow(`SELECT `+adminPlayerCols+`
		FROM cn35_accounts a LEFT JOIN cn35_player_profiles p ON p.user_id=a.user_id
		WHERE a.user_id=?`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, cn35game.ErrNoAccount
	}
	if err != nil {
		return nil, err
	}
	d := &cn35game.AdminPlayerDetail{Player: row}

	units, _, err := s.AdminCharacters(cn35game.AdminCharFilter{UserID: userID, Page: 1, PageSize: adminMaxPageSize})
	if err != nil {
		return nil, err
	}
	d.Units = units
	monsters, err := s.adminMonsters(userID)
	if err != nil {
		return nil, err
	}
	d.Monsters = monsters
	decks, err := s.adminDecks(userID)
	if err != nil {
		return nil, err
	}
	d.Decks = decks
	balances, err := s.adminBalances(userID)
	if err != nil {
		return nil, err
	}
	d.Balances = balances
	d.Quest, err = s.adminQuestProgress(userID)
	if err != nil {
		return nil, err
	}
	raw, err := s.adminRawAccount(userID)
	if err != nil {
		return nil, err
	}
	d.Raw = raw
	return d, nil
}

func (s *Store) AdminCharacters(f cn35game.AdminCharFilter) ([]cn35game.AdminUnitRow, int, error) {
	page, size := adminPage(f.Page, f.PageSize)
	where := `WHERE (?='' OR t.name LIKE ?) AND (?=0 OR u.unit_id=?) AND (?='' OR u.user_id=?)`
	args := []any{f.Name, adminLike(f.Name), f.UnitID, f.UnitID, f.UserID, f.UserID}
	var total int
	cnt := `SELECT COUNT(*) FROM cn35_player_units u
		LEFT JOIN cn35_unit_templates t ON t.unit_id=u.unit_id ` + where
	if err := s.db.QueryRow(cnt, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT u.user_id,u.unit_id,u.level,u.duplicated_count,
		u.protected,u.newcomer,u.monster_uuid,
		COALESCE(t.name,''),COALESCE(t.element,0),COALESCE(t.rare,0),
		COALESCE(a.user_name,'')
		FROM cn35_player_units u
		LEFT JOIN cn35_unit_templates t ON t.unit_id=u.unit_id
		LEFT JOIN cn35_accounts a ON a.user_id=u.user_id
		`+where+` ORDER BY u.unit_id LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []cn35game.AdminUnitRow{}
	for rows.Next() {
		var r cn35game.AdminUnitRow
		var prot, newc int
		if err = rows.Scan(&r.OwnerUID, &r.UnitID, &r.Level, &r.Duplicated,
			&prot, &newc, &r.MonsterUUID, &r.Name, &r.Element, &r.Rare,
			&r.OwnerName); err != nil {
			return nil, 0, err
		}
		r.Protected = prot == 1
		r.Newcomer = newc == 1
		r.ElementName = elementName(r.Element)
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// AdminItems 资产/礼物合并视图：余额行（currency）与礼物箱行（present）。
// 两源分别过滤后在内存合并排序（id 稳定：bal:<uid>:<cur> / present:<uuid>），
// 行数为管理台规模，不做库内 UNION。
func (s *Store) AdminItems(f cn35game.AdminItemFilter) ([]cn35game.AdminItemRow, int, error) {
	page, size := adminPage(f.Page, f.PageSize)
	wantCur := f.Kind == "" || f.Kind == "currency"
	wantPre := f.Kind == "" || f.Kind == "present"
	out := []cn35game.AdminItemRow{}
	if wantCur {
		rows, err := s.db.Query(`SELECT b.user_id,b.currency,b.amount,b.updated_at,
			COALESCE(a.user_name,'')
			FROM cn35_player_balances b LEFT JOIN cn35_accounts a ON a.user_id=b.user_id
			WHERE (?='' OR b.currency LIKE ?) AND (?='' OR b.user_id LIKE ?)
			ORDER BY b.user_id,b.currency`,
			f.Name, adminLike(f.Name), f.UserID, adminLike(f.UserID))
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var r cn35game.AdminItemRow
			if err = rows.Scan(&r.OwnerUID, &r.Name, &r.Count, &r.UpdatedAt, &r.OwnerName); err != nil {
				rows.Close()
				return nil, 0, err
			}
			r.ID = "bal:" + r.OwnerUID + ":" + r.Name
			r.Kind = "currency"
			r.Note = "货币余额"
			out = append(out, r)
		}
		if err = rows.Close(); err != nil {
			return nil, 0, err
		}
	}
	if wantPre {
		rows, err := s.db.Query(`SELECT pr.uuid,pr.user_id,pr.content_type,pr.content_id,
			pr.amount,pr.expire_at,pr.received_at,pr.created_at,
			COALESCE(a.user_name,'')
			FROM cn35_user_presents pr LEFT JOIN cn35_accounts a ON a.user_id=pr.user_id
			WHERE (?='' OR pr.content_type LIKE ?) AND (?='' OR pr.user_id LIKE ?)
			ORDER BY pr.created_at DESC`,
			f.Name, adminLike(f.Name), f.UserID, adminLike(f.UserID))
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var uuid, contentType, updated string
			var contentID, amount, expireAt, receivedAt, createdAt int64
			var r cn35game.AdminItemRow
			if err = rows.Scan(&uuid, &r.OwnerUID, &contentType, &contentID,
				&amount, &expireAt, &receivedAt, &createdAt, &r.OwnerName); err != nil {
				rows.Close()
				return nil, 0, err
			}
			r.ID = "present:" + uuid
			r.Kind = "present"
			r.Name = fmt.Sprintf("%s#%d", contentType, contentID)
			r.Count = amount
			updated = time.Unix(createdAt, 0).UTC().Format(time.RFC3339)
			r.UpdatedAt = updated
			state := "待领"
			if receivedAt > 0 {
				state = "已领取"
			}
			r.Note = fmt.Sprintf("礼物箱 %s；过期=%d", state, expireAt)
			out = append(out, r)
		}
		if err = rows.Close(); err != nil {
			return nil, 0, err
		}
	}
	total := len(out)
	start := (page - 1) * size
	if start > total {
		return []cn35game.AdminItemRow{}, total, nil
	}
	end := start + size
	if end > total {
		end = total
	}
	return out[start:end], total, nil
}

// AdminActivities 公告/活动窗口全量（cn35_infos），status 按当前时间推导：
// enabled=0→disabled；now<begin→upcoming；end>0 且 now>end→ended；其余 active。
func (s *Store) AdminActivities() ([]cn35game.AdminActivityRow, error) {
	rows, err := s.db.Query(`SELECT id,category,title,length(body),begin_at,end_at,enabled,weight
		FROM cn35_infos ORDER BY weight DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := s.clockNowUTC().Unix()
	out := []cn35game.AdminActivityRow{}
	for rows.Next() {
		var r cn35game.AdminActivityRow
		var enabled int
		if err = rows.Scan(&r.ID, &r.Type, &r.Name, &r.BodyLen, &r.BeginAt, &r.EndAt, &enabled, &r.Weight); err != nil {
			return nil, err
		}
		switch {
		case enabled == 0:
			r.Status = "disabled"
		case r.BeginAt > 0 && now < r.BeginAt:
			r.Status = "upcoming"
		case r.EndAt > 0 && now > r.EndAt:
			r.Status = "ended"
		default:
			r.Status = "active"
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) adminMonsters(userID string) ([]cn35game.AdminMonsterRow, error) {
	rows, err := s.db.Query(`SELECT uuid,monster_id,unit_id,level,protected
		FROM cn35_player_monsters WHERE user_id=? ORDER BY monster_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.AdminMonsterRow{}
	for rows.Next() {
		var r cn35game.AdminMonsterRow
		var unitID sql.NullInt64
		var prot int
		if err = rows.Scan(&r.UUID, &r.MonsterID, &unitID, &r.Level, &prot); err != nil {
			return nil, err
		}
		if unitID.Valid {
			v := int(unitID.Int64)
			r.UnitID = &v
		}
		r.Protected = prot == 1
		r.OwnerUID = userID
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) adminDecks(userID string) ([]cn35game.AdminDeckRow, error) {
	drows, err := s.db.Query(`SELECT party_id,display_order FROM cn35_player_decks
		WHERE user_id=? ORDER BY display_order`, userID)
	if err != nil {
		return nil, err
	}
	decks := []cn35game.AdminDeckRow{}
	defer drows.Close()
	for drows.Next() {
		var d cn35game.AdminDeckRow
		if err = drows.Scan(&d.PartyID, &d.DisplayOrder); err != nil {
			return nil, err
		}
		decks = append(decks, d)
	}
	if err = drows.Close(); err != nil {
		return nil, err
	}
	for i := range decks {
		slots, err := s.db.Query(`SELECT s.slot_index,s.unit_id,
			COALESCE(t.name,''),COALESCE(u.level,0)
			FROM cn35_player_deck_slots s
			LEFT JOIN cn35_player_units u ON u.user_id=s.user_id AND u.unit_id=s.unit_id
			LEFT JOIN cn35_unit_templates t ON t.unit_id=s.unit_id
			WHERE s.user_id=? AND s.party_id=? ORDER BY s.slot_index`, userID, decks[i].PartyID)
		if err != nil {
			return nil, err
		}
		for slots.Next() {
			var slot cn35game.AdminDeckSlot
			var uid sql.NullInt64
			if err = slots.Scan(&slot.SlotIndex, &uid, &slot.UnitName, &slot.Level); err != nil {
				slots.Close()
				return nil, err
			}
			if uid.Valid {
				v := int(uid.Int64)
				slot.UnitID = &v
			}
			decks[i].Slots = append(decks[i].Slots, slot)
		}
		if err = slots.Close(); err != nil {
			return nil, err
		}
	}
	return decks, nil
}

func (s *Store) adminBalances(userID string) ([]cn35game.AdminBalanceRow, error) {
	rows, err := s.db.Query(`SELECT currency,amount,updated_at FROM cn35_player_balances
		WHERE user_id=? ORDER BY currency`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.AdminBalanceRow{}
	for rows.Next() {
		var r cn35game.AdminBalanceRow
		if err = rows.Scan(&r.Currency, &r.Amount, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) adminQuestProgress(userID string) (*cn35game.AdminQuestProgress, error) {
	var q cn35game.AdminQuestProgress
	err := s.db.QueryRow(`SELECT stage_id,area_id,quest_id,hard_mode_stage_id,updated_at
		FROM cn35_quest_progress WHERE user_id=?`, userID).
		Scan(&q.StageID, &q.AreaID, &q.QuestID, &q.HardModeStageID, &q.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// adminRawAccount 账号+资料+旗帜的脱敏原文投影：token/sk/password_hash
// 一律不落；其余列原值输出便于后台对照排查。
func (s *Store) adminRawAccount(userID string) (map[string]any, error) {
	raw := map[string]any{}
	var heID, accountName, sdkID, deviceID, lastServer, createdAt, updatedAt string
	var isNew, tut int
	err := s.db.QueryRow(`SELECT he_account_id,account_name,sdk_account_id,device_id,
		is_new_user,tutorial_finish,last_server_id,created_at,updated_at
		FROM cn35_accounts WHERE user_id=?`, userID).
		Scan(&heID, &accountName, &sdkID, &deviceID, &isNew, &tut, &lastServer, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	raw["account"] = map[string]any{
		"he_account_id": heID, "user_id": userID, "account_name": accountName,
		"sdk_account_id": sdkID, "device_id": deviceID, "is_new_user": isNew == 1,
		"tutorial_finish": tut == 1, "last_server_id": lastServer,
		"created_at": createdAt, "updated_at": updatedAt,
	}
	var lvl, exp, maxExp, ap, maxAP, bp, maxBP, lbSec int64
	var leader, helper, apAt, guild string
	err = s.db.QueryRow(`SELECT level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,
		leader_unit_id,helper_unit_id,login_bonus_receivable_sec,guild_name_snapshot
		FROM cn35_player_profiles WHERE user_id=?`, userID).
		Scan(&lvl, &exp, &maxExp, &ap, &maxAP, &apAt, &bp, &maxBP, &leader, &helper, &lbSec, &guild)
	if err == nil {
		raw["profile"] = map[string]any{
			"level": lvl, "exp": exp, "max_exp": maxExp, "ap": ap, "max_ap": maxAP,
			"ap_updated_at": apAt, "bp": bp, "max_bp": maxBP,
			"leader_unit_id": leader, "helper_unit_id": helper,
			"login_bonus_receivable_sec": lbSec, "guild_name_snapshot": guild,
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var questFailed int64
	_ = s.db.QueryRow(`SELECT quest_failed FROM cn35_player_flags WHERE user_id=?`, userID).
		Scan(&questFailed)
	raw["flags"] = map[string]any{"quest_failed": questFailed}
	return raw, nil
}

// 编译期断言：Store 实现管理读面契约。
var _ cn35game.AdminViewStore = (*Store)(nil)
