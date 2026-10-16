// resource_config.go ITEM350-03：资源副本+徽章商品的草稿/发布版本治理。
//
// 草稿表（cn35_resource_quest_drafts/_drops/_waves、cn35_medal_goods_drafts）
// 可自由编辑；Publish 把全量草稿规范化为不可变 JSON 快照入
// cn35_resource_config_versions 并同事务切换 cn35_resource_config_release
// 单行指针 + 写 cn35_admin_audit。运行期读面只认生效快照。
package cn35store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.ResourceAdminStore = (*Store)(nil)

// resourceSnapshot 发布快照的规范化结构（序列化字段序稳定）。
type resourceSnapshot struct {
	Quests []cn35game.ResourceQuestDraft `json:"quests"`
	Goods  []cn35game.MedalGoodDraft     `json:"goods"`
}

func resourceDrafts(q sqlQueryer) (*cn35game.ResourceConfig, error) {
	rows, err := q.Query(`SELECT quest_key,quest_id,area_id,stage_id,name,ap,exp,
		sort,open,initial_power,powerup_rate_bp,time_limit_sec,slash_rate_bp,
		gun_rate_bp FROM cn35_resource_quest_drafts ORDER BY sort,quest_id`)
	if err != nil {
		return nil, err
	}
	quests := []cn35game.ResourceQuestDraft{}
	byKey := map[string]int{} // quest_key → quests 下标（append 扩容后仍有效）
	for rows.Next() {
		var q cn35game.ResourceQuestDraft
		var open int
		var timeLimit sql.NullInt64
		if err = rows.Scan(&q.QuestKey, &q.QuestID, &q.AreaID, &q.StageID,
			&q.Name, &q.AP, &q.Exp, &q.Sort, &open, &q.InitialPower,
			&q.PowerupRateBP, &timeLimit, &q.SlashRateBP, &q.GunRateBP); err != nil {
			rows.Close()
			return nil, err
		}
		q.Open = open == 1
		if timeLimit.Valid {
			q.TimeLimitSec = int(timeLimit.Int64)
		}
		quests = append(quests, q)
		byKey[q.QuestKey] = len(quests) - 1
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	drows, err := q.Query(`SELECT quest_key,seq,reward_type,COALESCE(item_key,''),
		chance_bp,min_count,max_count FROM cn35_resource_quest_drops
		ORDER BY quest_key,seq`)
	if err != nil {
		return nil, err
	}
	for drows.Next() {
		var qk string
		var d cn35game.ResourceDrop
		if err = drows.Scan(&qk, &d.Seq, &d.RewardType, &d.ItemKey,
			&d.ChanceBP, &d.MinCount, &d.MaxCount); err != nil {
			drows.Close()
			return nil, err
		}
		if idx, ok := byKey[qk]; ok {
			quests[idx].Drops = append(quests[idx].Drops, d)
		}
	}
	if err = drows.Close(); err != nil {
		return nil, err
	}
	wrows, err := q.Query(`SELECT quest_key,wave_no,slot,monster_id,scale_bp,
		hp_rate_bp,at_rate_bp,is_boss,hp,at,kill_sp
		FROM cn35_resource_quest_waves
		ORDER BY quest_key,wave_no,slot`)
	if err != nil {
		return nil, err
	}
	for wrows.Next() {
		var qk string
		var w cn35game.ResourceWave
		var boss, hp, at, killSp sql.NullInt64
		if err = wrows.Scan(&qk, &w.WaveNo, &w.Slot, &w.MonsterID, &w.ScaleBP,
			&w.HpRateBP, &w.AtRateBP, &boss, &hp, &at, &killSp); err != nil {
			wrows.Close()
			return nil, err
		}
		w.IsBoss = boss.Valid && boss.Int64 == 1
		if hp.Valid {
			w.Hp = int(hp.Int64)
		}
		if at.Valid {
			w.At = int(at.Int64)
		}
		if killSp.Valid {
			w.KillSp = int(killSp.Int64)
		}
		if idx, ok := byKey[qk]; ok {
			quests[idx].Waves = append(quests[idx].Waves, w)
		}
	}
	if err = wrows.Close(); err != nil {
		return nil, err
	}
	rrows, err := q.Query(`SELECT quest_key,mob_chance_bp,boss_chance_bp,
		mob_q1_bp,mob_q2_bp,mob_q3_bp,mob_q4_bp,boss_q1_bp,boss_q2_bp,boss_q3_bp,boss_q4_bp
		FROM cn35_resource_quest_rune_drops`)
	if err != nil {
		return nil, err
	}
	for rrows.Next() {
		var qk string
		var rd cn35game.ResourceRuneDrop
		if err = rrows.Scan(&qk, &rd.MobChanceBP, &rd.BossChanceBP,
			&rd.MobQBP[0], &rd.MobQBP[1], &rd.MobQBP[2], &rd.MobQBP[3],
			&rd.BossQBP[0], &rd.BossQBP[1], &rd.BossQBP[2], &rd.BossQBP[3]); err != nil {
			rrows.Close()
			return nil, err
		}
		if idx, ok := byKey[qk]; ok {
			quests[idx].RuneDrop = &rd
		}
	}
	if err = rrows.Close(); err != nil {
		return nil, err
	}
	goods, err := medalGoodDrafts(q)
	if err != nil {
		return nil, err
	}
	return &cn35game.ResourceConfig{Quests: quests, Goods: goods}, nil
}

// ResourceDrafts 见 cn35game.ResourceAdminStore。
func (s *Store) ResourceDrafts() (*cn35game.ResourceConfig, error) {
	return resourceDrafts(s.db)
}

func medalGoodDrafts(q sqlQueryer) ([]cn35game.MedalGoodDraft, error) {
	rows, err := q.Query(`SELECT goods_key,goods_id,item_key,name,price,
		purchase_limit,sort,group_key,enabled FROM cn35_medal_goods_drafts
		ORDER BY sort,goods_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.MedalGoodDraft{}
	for rows.Next() {
		var g cn35game.MedalGoodDraft
		var limit sql.NullInt64
		var enabled int
		if err = rows.Scan(&g.GoodsKey, &g.GoodsID, &g.ItemKey, &g.Name,
			&g.Price, &limit, &g.Sort, &g.Group, &enabled); err != nil {
			return nil, err
		}
		g.Enabled = enabled == 1
		if limit.Valid {
			v := int(limit.Int64)
			g.PurchaseLimit = &v
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func decodeResourceSnapshot(version int64, snap, digest string) (*cn35game.ResourceConfig, error) {
	sum := sha256.Sum256([]byte(snap))
	if got := hex.EncodeToString(sum[:]); got != digest {
		return nil, fmt.Errorf("cn35store: 资源快照 digest 不一致 version=%d", version)
	}
	var body resourceSnapshot
	if err := json.Unmarshal([]byte(snap), &body); err != nil {
		return nil, fmt.Errorf("cn35store: 资源快照损坏 version=%d: %w", version, err)
	}
	return &cn35game.ResourceConfig{
		Version: version, Quests: body.Quests, Goods: body.Goods, Digest: digest,
	}, nil
}

// activeResourceConfigTx 在既有写事务内读生效快照；调用方不得退回草稿，
// 运行期消费面必须以发布版本为准。
func activeResourceConfigTx(tx *sql.Tx) (*cn35game.ResourceConfig, error) {
	var snap string
	var version int64
	var digest string
	err := tx.QueryRow(`SELECT v.version,v.snapshot_json,v.sha256
		FROM cn35_resource_config_release r
		JOIN cn35_resource_config_versions v ON v.version=r.version
		WHERE r.id=1`).Scan(&version, &snap, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeResourceSnapshot(version, snap, digest)
}

// resourceQuestKeyForVersionTx 从不可变版本快照反查 quest_key；不能用
// cn35_resource_quest_drafts 反查，否则结算台账会随未发布草稿漂移。
func resourceQuestKeyForVersionTx(tx *sql.Tx, version int64, questID int) (string, error) {
	var snap, digest string
	err := tx.QueryRow(`SELECT snapshot_json,sha256 FROM cn35_resource_config_versions
		WHERE version=?`, version).Scan(&snap, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", cn35game.ErrResourceVersionUnknown
	}
	if err != nil {
		return "", err
	}
	cfg, err := decodeResourceSnapshot(version, snap, digest)
	if err != nil {
		return "", err
	}
	for _, q := range cfg.Quests {
		if q.QuestID == questID {
			return q.QuestKey, nil
		}
	}
	return "", fmt.Errorf("%w: version=%d 无 quest_id=%d",
		cn35game.ErrResourceVersionUnknown, version, questID)
}

// ActiveResourceConfig 见 cn35game.ResourceConfigReadStore：读生效指针指向的
// 不可变快照；无发布 → nil,nil。
func (s *Store) ActiveResourceConfig() (*cn35game.ResourceConfig, error) {
	var snap string
	var version int64
	var digest string
	err := s.db.QueryRow(`SELECT v.version,v.snapshot_json,v.sha256
		FROM cn35_resource_config_release r
		JOIN cn35_resource_config_versions v ON v.version=r.version
		WHERE r.id=1`).Scan(&version, &snap, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeResourceSnapshot(version, snap, digest)
}

// SaveResourceQuest 见 cn35game.ResourceAdminStore。drops/waves 整体替换
// （QuestKey 稳定键语义）；校验在发布侧做，这里只做形状级检查。
func (s *Store) SaveResourceQuest(q cn35game.ResourceQuestDraft, actor string) error {
	if q.QuestKey == "" || q.QuestID <= 0 || q.AreaID <= 0 || q.StageID <= 0 || q.AP <= 0 {
		return fmt.Errorf("%w: quest_key/quest_id/area_id/stage_id/ap 非法",
			cn35game.ErrResourceDraft)
	}
	return s.withTx(func(tx *sql.Tx) error {
		now := nowUTC()
		var timeLimit any
		if q.TimeLimitSec > 0 {
			timeLimit = q.TimeLimitSec
		}
		if _, err := tx.Exec(`INSERT INTO cn35_resource_quest_drafts
			(quest_key,quest_id,area_id,stage_id,name,ap,exp,sort,open,
			initial_power,powerup_rate_bp,time_limit_sec,slash_rate_bp,
			gun_rate_bp,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(quest_key) DO UPDATE SET quest_id=excluded.quest_id,
			area_id=excluded.area_id,stage_id=excluded.stage_id,name=excluded.name,
			ap=excluded.ap,exp=excluded.exp,sort=excluded.sort,open=excluded.open,
			initial_power=excluded.initial_power,
			powerup_rate_bp=excluded.powerup_rate_bp,
			time_limit_sec=excluded.time_limit_sec,
			slash_rate_bp=excluded.slash_rate_bp,
			gun_rate_bp=excluded.gun_rate_bp,
			updated_at=excluded.updated_at`,
			q.QuestKey, q.QuestID, q.AreaID, q.StageID, q.Name, q.AP, q.Exp,
			q.Sort, boolInt(q.Open), q.InitialPower, q.PowerupRateBP, timeLimit,
			q.SlashRateBP, q.GunRateBP, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM cn35_resource_quest_drops WHERE quest_key=?`,
			q.QuestKey); err != nil {
			return err
		}
		for _, d := range q.Drops {
			var itemKey any
			if d.ItemKey != "" {
				itemKey = d.ItemKey
			}
			if _, err := tx.Exec(`INSERT INTO cn35_resource_quest_drops
				(quest_key,seq,reward_type,item_key,chance_bp,min_count,max_count)
				VALUES (?,?,?,?,?,?,?)`,
				q.QuestKey, d.Seq, d.RewardType, itemKey, d.ChanceBP,
				d.MinCount, d.MaxCount); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM cn35_resource_quest_waves WHERE quest_key=?`,
			q.QuestKey); err != nil {
			return err
		}
		for _, w := range q.Waves {
			var hp, at, killSp any
			if w.Hp > 0 {
				hp = w.Hp
			}
			if w.At > 0 {
				at = w.At
			}
			if w.KillSp > 0 {
				killSp = w.KillSp
			}
			if _, err := tx.Exec(`INSERT INTO cn35_resource_quest_waves
				(quest_key,wave_no,slot,monster_id,scale_bp,hp_rate_bp,at_rate_bp,
				is_boss,hp,at,kill_sp)
				VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				q.QuestKey, w.WaveNo, w.Slot, w.MonsterID, w.ScaleBP,
				w.HpRateBP, w.AtRateBP, boolInt(w.IsBoss), hp, at, killSp); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM cn35_resource_quest_rune_drops
			WHERE quest_key=?`, q.QuestKey); err != nil {
			return err
		}
		if q.RuneDrop != nil {
			rd := q.RuneDrop
			if _, err := tx.Exec(`INSERT INTO cn35_resource_quest_rune_drops
				(quest_key,mob_chance_bp,boss_chance_bp,
				mob_q1_bp,mob_q2_bp,mob_q3_bp,mob_q4_bp,boss_q1_bp,boss_q2_bp,boss_q3_bp,boss_q4_bp)
				VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				q.QuestKey, rd.MobChanceBP, rd.BossChanceBP,
				rd.MobQBP[0], rd.MobQBP[1], rd.MobQBP[2], rd.MobQBP[3],
				rd.BossQBP[0], rd.BossQBP[1], rd.BossQBP[2], rd.BossQBP[3]); err != nil {
				return err
			}
		}
		return auditTx(tx, actor, "resource.quest.save", q.QuestKey,
			fmt.Sprintf(`{"quest_id":%d,"ap":%d,"drops":%d,"waves":%d}`,
				q.QuestID, q.AP, len(q.Drops), len(q.Waves)))
	})
}

// DeleteResourceQuest 见 cn35game.ResourceAdminStore。
func (s *Store) DeleteResourceQuest(questKey, actor string) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM cn35_resource_quest_drafts WHERE quest_key=?`,
			questKey)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrResourceDraft
		}
		return auditTx(tx, actor, "resource.quest.delete", questKey, "")
	})
}

// SaveMedalGood 见 cn35game.ResourceAdminStore。
func (s *Store) SaveMedalGood(g cn35game.MedalGoodDraft, actor string) error {
	switch g.Group {
	case "", "item", "powerup", "story_unit", "rare_medal", "friend_medal":
	default:
		return fmt.Errorf("%w: group_key=%q 非法", cn35game.ErrResourceDraft, g.Group)
	}
	if g.GoodsKey == "" || g.GoodsID <= 0 || g.ItemKey == "" || g.Price < 0 ||
		g.Sort < 0 || (g.PurchaseLimit != nil && *g.PurchaseLimit <= 0) {
		return fmt.Errorf("%w: goods_key/goods_id/item_key/price/limit/sort 非法",
			cn35game.ErrResourceDraft)
	}
	return s.withTx(func(tx *sql.Tx) error {
		var limit any
		if g.PurchaseLimit != nil {
			limit = *g.PurchaseLimit
		}
		if _, err := tx.Exec(`INSERT INTO cn35_medal_goods_drafts
			(goods_key,goods_id,item_key,name,price,currency,purchase_limit,sort,group_key,enabled,updated_at)
			VALUES (?,?,?,?,?,'medal',?,?,?,?,?)
			ON CONFLICT(goods_key) DO UPDATE SET goods_id=excluded.goods_id,
			item_key=excluded.item_key,name=excluded.name,price=excluded.price,
			purchase_limit=excluded.purchase_limit,sort=excluded.sort,
			group_key=COALESCE(NULLIF(excluded.group_key,''),
				cn35_medal_goods_drafts.group_key),
			enabled=excluded.enabled,updated_at=excluded.updated_at`,
			g.GoodsKey, g.GoodsID, g.ItemKey, g.Name, g.Price, limit,
			g.Sort, g.Group, boolInt(g.Enabled), nowUTC()); err != nil {
			return err
		}
		return auditTx(tx, actor, "resource.goods.save", g.GoodsKey,
			fmt.Sprintf(`{"goods_id":%d,"price":%d,"enabled":%v}`,
				g.GoodsID, g.Price, g.Enabled))
	})
}

// DeleteMedalGood 见 cn35game.ResourceAdminStore。只删草稿行；已发布
// 快照独立保留，限购累计表也不绑定可变草稿行。
func (s *Store) DeleteMedalGood(goodsKey, actor string) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM cn35_medal_goods_drafts WHERE goods_key=?`,
			goodsKey)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrResourceDraft
		}
		return auditTx(tx, actor, "resource.goods.delete", goodsKey, "")
	})
}

// expectedResourceQuest 本轮固定的 24 关命名空间：素材 5 属性×4 档 +
// 金币 4 档。quest_key/quest_id/area_id/stage_id 是协议下发边界，不允许
// 管理草稿漂移或与剧情关 ID 冲突。
func expectedResourceQuest(questKey string) (cn35game.ResourceQuestDraft, int, bool) {
	elems := []struct {
		key  string
		elem int
	}{
		{"fire", 1}, {"aqua", 2}, {"wind", 3}, {"light", 4}, {"dark", 5},
	}
	for _, e := range elems {
		for tier := 1; tier <= 4; tier++ {
			key := fmt.Sprintf("mat_%s_%d", e.key, tier)
			if key == questKey {
				return cn35game.ResourceQuestDraft{
					QuestKey: key, QuestID: 90000 + e.elem*10 + tier,
					AreaID: 9000 + e.elem, StageID: 900,
				}, e.elem, true
			}
		}
	}
	for tier := 1; tier <= 4; tier++ {
		key := fmt.Sprintf("gold_%d", tier)
		if key == questKey {
			return cn35game.ResourceQuestDraft{
				QuestKey: key, QuestID: 90600 + tier,
				AreaID: 9006, StageID: 901,
			}, 0, true
		}
	}
	// RUNE350-03 符文本 3 档命名空间（elem=0：不限制符文掉落 item 白名单，
	// 符文走 rune_drops 表而非 drops）。
	for tier := 1; tier <= 3; tier++ {
		key := fmt.Sprintf("rune_%d", tier)
		if key == questKey {
			return cn35game.ResourceQuestDraft{
				QuestKey: key, QuestID: 90700 + tier,
				AreaID: 9007, StageID: 902,
			}, 0, true
		}
	}
	return cn35game.ResourceQuestDraft{}, 0, false
}

// expectedMedalGood 本轮固定的 20 个商品命名空间：medal_book_<rare>
// （goods_id=rare-1，item=awakening_book_<rare>，kind=book）、
// medal_mat_<content_id 1..15>（goods_id=4+cid，item=powerup_item_<cid>，
// kind=material）与 medal_potion_ap（goods_id=20，kind=consumable，
// 归组 rare_medal 页签）。medal_potion_bp 已于 0033 下架（2026-10-09
// 需求：商店下架 BP 药水）。返回期望商品行、kind、模板序号与期望分组。
func expectedMedalGood(goodsKey string) (cn35game.MedalGoodDraft, string, int, string, bool) {
	for rare := 2; rare <= 5; rare++ {
		key := fmt.Sprintf("medal_book_%d", rare)
		if key == goodsKey {
			return cn35game.MedalGoodDraft{
				GoodsKey: key, GoodsID: rare - 1,
				ItemKey: fmt.Sprintf("awakening_book_%d", rare),
			}, "book", rare, "", true
		}
	}
	for cid := 1; cid <= 15; cid++ {
		key := fmt.Sprintf("medal_mat_%d", cid)
		if key == goodsKey {
			return cn35game.MedalGoodDraft{
				GoodsKey: key, GoodsID: 4 + cid,
				ItemKey: fmt.Sprintf("powerup_item_%d", cid),
			}, "material", cid, "", true
		}
	}
	for _, p := range []struct {
		key     string
		goodsID int
		itemKey string
	}{
		{"medal_potion_ap", 20, "ap_potion"},
	} {
		if p.key == goodsKey {
			return cn35game.MedalGoodDraft{
				GoodsKey: p.key, GoodsID: p.goodsID,
				ItemKey: p.itemKey, Group: "rare_medal",
			}, "consumable", 0, "rare_medal", true
		}
	}
	// RUNE350-01：符文商品 medal_skill_<id>（goods_id=100+id，
	// item=skill_rune_<id>）。锚点物品 kind='consumable'（0024：目录
	// CHECK 不可重建，item_key 前缀即符文商品判据），免费，道具页签。
	for _, id := range medalSkillGoodsIDs {
		key := fmt.Sprintf("medal_skill_%d", id)
		if key == goodsKey {
			return cn35game.MedalGoodDraft{
				GoodsKey: key, GoodsID: 100 + id,
				ItemKey: fmt.Sprintf("skill_rune_%d", id), Group: "item",
			}, "consumable", id, "item", true
		}
	}
	return cn35game.MedalGoodDraft{}, "", 0, "", false
}

// medalSkillGoodsIDs 上架的基础符文 skill_id 集合：1-25、27-29（0026
// 起 25 ガッツ/30 ルーン收集/31 シード收集无服务端生效通路，下架）。
var medalSkillGoodsIDs = func() []int {
	ids := []int{}
	for id := 1; id <= 29; id++ {
		if id == 25 || id == 26 {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}()

// ValidateResourceDrafts 见 cn35game.ResourceAdminStore。校验是发布门禁：
// 24 关/4 商品命名空间完整，素材本只掉同属性素材、金币本只掉金币，物品
// 启用且波次怪物可由当前 cardcatalog 加载。
func (s *Store) ValidateResourceDrafts(validateMonster cn35game.ResourceMonsterValidator) ([]string, error) {
	drafts, err := s.ResourceDrafts()
	if err != nil {
		return nil, err
	}
	cat, err := s.ItemCatalog()
	if err != nil {
		return nil, err
	}
	kinds := map[string]cn35game.ItemDef{}
	for _, d := range cat {
		kinds[d.ItemKey] = d
	}
	return validateResourceDrafts(drafts, kinds, validateMonster), nil
}

func validateResourceDrafts(cfg *cn35game.ResourceConfig, itemKinds map[string]cn35game.ItemDef,
	validateMonster cn35game.ResourceMonsterValidator) []string {
	var errs []string
	if validateMonster == nil {
		errs = append(errs, "魔宠模板校验器未装配")
	}
	if len(cfg.Quests) != 27 {
		errs = append(errs, fmt.Sprintf("资源副本必须恰好包含 27 关，当前 %d", len(cfg.Quests)))
	}
	seenQuests := map[string]bool{}
	for _, q := range cfg.Quests {
		want, elem, known := expectedResourceQuest(q.QuestKey)
		if !known {
			errs = append(errs, fmt.Sprintf("%s: 非资源副本命名空间", q.QuestKey))
			continue
		}
		if seenQuests[q.QuestKey] {
			errs = append(errs, fmt.Sprintf("%s: 草稿重复", q.QuestKey))
		}
		seenQuests[q.QuestKey] = true
		if q.QuestID != want.QuestID || q.AreaID != want.AreaID || q.StageID != want.StageID {
			errs = append(errs, fmt.Sprintf("%s: quest/area/stage 必须为 %d/%d/%d",
				q.QuestKey, want.QuestID, want.AreaID, want.StageID))
		}
		if q.AP <= 0 || q.Exp < 0 || q.Sort < 0 {
			errs = append(errs, fmt.Sprintf("%s: ap/exp/sort 非法", q.QuestKey))
		}
		isRune := q.StageID == 902
		if len(q.Waves) == 0 {
			errs = append(errs, fmt.Sprintf("%s: 无波次配置", q.QuestKey))
		}
		if !isRune && len(q.Drops) == 0 {
			errs = append(errs, fmt.Sprintf("%s: 无掉落配置", q.QuestKey))
		}
		if isRune {
			if len(q.Drops) != 0 {
				errs = append(errs, fmt.Sprintf("%s: 符文本不配置常规掉落", q.QuestKey))
			}
			if q.RuneDrop == nil {
				errs = append(errs, fmt.Sprintf("%s: 符文本缺少符文掉落配置", q.QuestKey))
			}
			if q.TimeLimitSec <= 0 {
				errs = append(errs, fmt.Sprintf("%s: 符文本必须配置波次限时", q.QuestKey))
			}
		}
		waveNos := map[int]bool{}
		for _, w := range q.Waves {
			if w.WaveNo <= 0 || w.Slot <= 0 || w.MonsterID <= 0 ||
				w.ScaleBP <= 0 || w.HpRateBP <= 0 || w.AtRateBP <= 0 {
				errs = append(errs, fmt.Sprintf("%s: 波次配置非法", q.QuestKey))
				continue
			}
			waveNos[w.WaveNo] = true
			if validateMonster != nil {
				if err := validateMonster(w.MonsterID); err != nil {
					errs = append(errs, fmt.Sprintf("%s: 魔宠 %d 无模板: %v",
						q.QuestKey, w.MonsterID, err))
				}
			}
		}
		for waveNo := 1; waveNo <= len(waveNos); waveNo++ {
			if !waveNos[waveNo] {
				errs = append(errs, fmt.Sprintf("%s: 波次必须从 1 连续编号", q.QuestKey))
				break
			}
		}
		if isRune && len(waveNos) != 1 {
			errs = append(errs, fmt.Sprintf("%s: 符文本只设单波次", q.QuestKey))
		}
		if isRune {
			continue // 符文本无常规掉落规则，跳过 drops 校验段
		}
		seenDropSeq := map[int]bool{}
		for _, d := range q.Drops {
			if d.Seq <= 0 || seenDropSeq[d.Seq] {
				errs = append(errs, fmt.Sprintf("%s: drop seq 非法/重复", q.QuestKey))
			}
			seenDropSeq[d.Seq] = true
			if d.ChanceBP < 0 || d.ChanceBP > 10000 ||
				d.MinCount <= 0 || d.MaxCount < d.MinCount {
				errs = append(errs, fmt.Sprintf("%s: 掉落概率/数量区间非法", q.QuestKey))
			}
			if elem == 0 {
				if d.RewardType != "gacha_point" || d.ItemKey != "" {
					errs = append(errs, fmt.Sprintf("%s: 金币本只能掉落 gacha_point", q.QuestKey))
				}
				continue
			}
			if d.RewardType != "item" {
				errs = append(errs, fmt.Sprintf("%s: 素材本只能掉落 item", q.QuestKey))
				continue
			}
			allowed := map[string]bool{
				fmt.Sprintf("powerup_item_%d", elem):    true,
				fmt.Sprintf("powerup_item_%d", elem+5):  true,
				fmt.Sprintf("powerup_item_%d", elem+10): true,
			}
			def, ok := itemKinds[d.ItemKey]
			if !allowed[d.ItemKey] {
				errs = append(errs, fmt.Sprintf("%s: 不得掉落非同属性素材 %q", q.QuestKey, d.ItemKey))
			} else if !ok || !def.Enabled || def.Kind != "material" || def.Element != elem {
				errs = append(errs, fmt.Sprintf("%s: 掉落物品 %q 未启用/类型不符", q.QuestKey, d.ItemKey))
			}
		}
	}
	for key := range map[string]bool{
		"mat_fire_1": true, "mat_fire_2": true, "mat_fire_3": true, "mat_fire_4": true,
		"mat_aqua_1": true, "mat_aqua_2": true, "mat_aqua_3": true, "mat_aqua_4": true,
		"mat_wind_1": true, "mat_wind_2": true, "mat_wind_3": true, "mat_wind_4": true,
		"mat_light_1": true, "mat_light_2": true, "mat_light_3": true, "mat_light_4": true,
		"mat_dark_1": true, "mat_dark_2": true, "mat_dark_3": true, "mat_dark_4": true,
		"gold_1": true, "gold_2": true, "gold_3": true, "gold_4": true,
		"rune_1": true, "rune_2": true, "rune_3": true,
	} {
		if !seenQuests[key] {
			errs = append(errs, fmt.Sprintf("缺少资源副本 %s", key))
		}
	}

	if want := 20 + len(medalSkillGoodsIDs); len(cfg.Goods) != want {
		errs = append(errs, fmt.Sprintf("徽章商品必须恰好包含 %d 项，当前 %d", want, len(cfg.Goods)))
	}
	seenGoods := map[string]bool{}
	for _, g := range cfg.Goods {
		want, kind, num, group, known := expectedMedalGood(g.GoodsKey)
		if !known {
			errs = append(errs, fmt.Sprintf("%s: 非徽章商品命名空间", g.GoodsKey))
			continue
		}
		if seenGoods[g.GoodsKey] {
			errs = append(errs, fmt.Sprintf("%s: 商品重复", g.GoodsKey))
		}
		seenGoods[g.GoodsKey] = true
		if g.GoodsID != want.GoodsID || g.ItemKey != want.ItemKey {
			errs = append(errs, fmt.Sprintf("%s: goods_id/item_key 必须为 %d/%s",
				g.GoodsKey, want.GoodsID, want.ItemKey))
		}
		if g.Group != group {
			errs = append(errs, fmt.Sprintf("%s: group_key 必须为 %q",
				g.GoodsKey, group))
		}
		if g.Price < 0 || g.Sort < 0 ||
			(g.PurchaseLimit != nil && *g.PurchaseLimit <= 0) ||
			(kind == "consumable" && g.Price != 0) ||
			(kind != "consumable" && g.Price <= 0) {
			errs = append(errs, fmt.Sprintf("%s: price/purchase_limit/sort 非法", g.GoodsKey))
		}
		def, ok := itemKinds[g.ItemKey]
		switch {
		case !ok || !def.Enabled || def.Kind != kind:
			errs = append(errs, fmt.Sprintf("%s: 商品物品 %q 未启用/类型非 %s",
				g.GoodsKey, g.ItemKey, kind))
		case kind == "book" && def.Rare != num:
			errs = append(errs, fmt.Sprintf("%s: 商品物品 %q 不是对应星级觉醒书",
				g.GoodsKey, g.ItemKey))
		case kind == "material" && def.ContentID != num:
			errs = append(errs, fmt.Sprintf("%s: 商品物品 %q 不是对应进化素材",
				g.GoodsKey, g.ItemKey))
		}
	}
	for rare := 2; rare <= 5; rare++ {
		key := fmt.Sprintf("medal_book_%d", rare)
		if !seenGoods[key] {
			errs = append(errs, fmt.Sprintf("缺少徽章商品 %s", key))
		}
	}
	for cid := 1; cid <= 15; cid++ {
		key := fmt.Sprintf("medal_mat_%d", cid)
		if !seenGoods[key] {
			errs = append(errs, fmt.Sprintf("缺少徽章商品 %s", key))
		}
	}
	for _, key := range []string{"medal_potion_ap"} {
		if !seenGoods[key] {
			errs = append(errs, fmt.Sprintf("缺少徽章商品 %s", key))
		}
	}
	for _, id := range medalSkillGoodsIDs {
		key := fmt.Sprintf("medal_skill_%d", id)
		if !seenGoods[key] {
			errs = append(errs, fmt.Sprintf("缺少徽章商品 %s", key))
		}
	}
	return errs
}

// PublishResourceConfig 见 cn35game.ResourceAdminStore。草稿读取、物品目录
// 校验、不可变快照和生效指针切换在同一事务，避免并发草稿造成校验/发布漂移。
func (s *Store) PublishResourceConfig(author, note string,
	validateMonster cn35game.ResourceMonsterValidator) (int64, string, error) {
	var version int64
	var digest string
	err := s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		drafts, err := resourceDrafts(tx)
		if err != nil {
			return err
		}
		cat, err := itemCatalog(tx)
		if err != nil {
			return err
		}
		kinds := map[string]cn35game.ItemDef{}
		for _, d := range cat {
			kinds[d.ItemKey] = d
		}
		if errs := validateResourceDrafts(drafts, kinds, validateMonster); len(errs) > 0 {
			return fmt.Errorf("%w: %s", cn35game.ErrResourceDraft,
				strings.Join(errs, "; "))
		}
		sort.Slice(drafts.Quests, func(i, j int) bool {
			return drafts.Quests[i].QuestID < drafts.Quests[j].QuestID
		})
		sort.Slice(drafts.Goods, func(i, j int) bool {
			if drafts.Goods[i].Sort != drafts.Goods[j].Sort {
				return drafts.Goods[i].Sort < drafts.Goods[j].Sort
			}
			return drafts.Goods[i].GoodsID < drafts.Goods[j].GoodsID
		})
		body, err := json.Marshal(resourceSnapshot{Quests: drafts.Quests, Goods: drafts.Goods})
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		digest = hex.EncodeToString(sum[:])
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(version),0)+1 FROM cn35_resource_config_versions`).
			Scan(&version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cn35_resource_config_versions
			(version,snapshot_json,sha256,author,note,created_at) VALUES (?,?,?,?,?,?)`,
			version, string(body), digest, author, note, nowUTC()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cn35_resource_config_release
			(id,version,updated_at) VALUES (1,?,?)
			ON CONFLICT(id) DO UPDATE SET version=excluded.version,updated_at=excluded.updated_at`,
			version, nowUTC()); err != nil {
			return err
		}
		return auditTx(tx, author, "resource.publish",
			fmt.Sprintf("version=%d", version),
			fmt.Sprintf(`{"sha256":%q,"note":%q}`, digest, note))
	})
	if err != nil {
		return 0, "", err
	}
	return version, digest, nil
}

// DropActiveConfigGoods 从已发布快照中删除指定 goods_key 并作为新不可变
// 版本生效；不经过草稿校验，避免管理员未完成的草稿阻断升级启动。
// 返回 found=false 表示激活快照本就不含目标商品（无需动作）。
func (s *Store) DropActiveConfigGoods(author, note string, keys ...string) (int64, string, bool, error) {
	var version int64
	var digest string
	found := false
	err := s.withTx(func(tx *sql.Tx) error {
		cfg, err := activeResourceConfigTx(tx)
		if err != nil || cfg == nil {
			return err
		}
		drop := map[string]bool{}
		for _, k := range keys {
			drop[k] = true
		}
		goods := make([]cn35game.MedalGoodDraft, 0, len(cfg.Goods))
		for _, g := range cfg.Goods {
			if drop[g.GoodsKey] {
				found = true
				continue
			}
			goods = append(goods, g)
		}
		if !found {
			return nil
		}
		body, err := json.Marshal(resourceSnapshot{Quests: cfg.Quests, Goods: goods})
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		digest = hex.EncodeToString(sum[:])
		ctx := context.Background()
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(version),0)+1 FROM cn35_resource_config_versions`).
			Scan(&version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cn35_resource_config_versions
			(version,snapshot_json,sha256,author,note,created_at) VALUES (?,?,?,?,?,?)`,
			version, string(body), digest, author, note, nowUTC()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cn35_resource_config_release
			(id,version,updated_at) VALUES (1,?,?)
			ON CONFLICT(id) DO UPDATE SET version=excluded.version,updated_at=excluded.updated_at`,
			version, nowUTC()); err != nil {
			return err
		}
		return auditTx(tx, author, "resource.drop_goods",
			fmt.Sprintf("version=%d", version),
			fmt.Sprintf(`{"sha256":%q,"note":%q,"goods":%q}`, digest, note,
				strings.Join(keys, ",")))
	})
	if err != nil {
		return 0, "", false, err
	}
	return version, digest, found, nil
}

// ActivateResourceVersion 见 cn35game.ResourceAdminStore。
func (s *Store) ActivateResourceVersion(version int64, actor, note string) error {
	return s.withTx(func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_resource_config_versions
			WHERE version=?`, version).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return cn35game.ErrResourceVersionUnknown
		}
		if _, err := tx.Exec(`INSERT INTO cn35_resource_config_release
			(id,version,updated_at) VALUES (1,?,?)
			ON CONFLICT(id) DO UPDATE SET version=excluded.version,updated_at=excluded.updated_at`,
			version, nowUTC()); err != nil {
			return err
		}
		return auditTx(tx, actor, "resource.activate",
			fmt.Sprintf("version=%d", version), fmt.Sprintf(`{"note":%q}`, note))
	})
}

// ResourceVersions 见 cn35game.ResourceAdminStore。
func (s *Store) ResourceVersions(limit int) ([]cn35game.ResourceVersionRow, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	var active int64
	_ = s.db.QueryRow(`SELECT version FROM cn35_resource_config_release WHERE id=1`).
		Scan(&active)
	rows, err := s.db.Query(`SELECT version,sha256,author,note,created_at
		FROM cn35_resource_config_versions ORDER BY version DESC LIMIT ?`, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []cn35game.ResourceVersionRow{}
	for rows.Next() {
		var r cn35game.ResourceVersionRow
		if err = rows.Scan(&r.Version, &r.SHA256, &r.Author, &r.Note, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, active, rows.Err()
}

// ResourceMigrationMarkers 返回历史版本中出现过的 migrate-* 作者标记
// （DISTINCT 扫描无 LIMIT，版本数不受清单分页影响）。
func (s *Store) ResourceMigrationMarkers() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT DISTINCT author FROM cn35_resource_config_versions
		WHERE author LIKE 'migrate-%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var author string
		if err = rows.Scan(&author); err != nil {
			return nil, err
		}
		out[author] = true
	}
	return out, rows.Err()
}

// ResourceVersionsCount 见 cn35game.ResourceAdminStore。
func (s *Store) ResourceVersionsCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM cn35_resource_config_versions`).Scan(&n)
	return n, err
}

// auditTx 写管理审计行（cn35_admin_audit，0013 表结构
// actor/action/target/detail/created_at）。
func auditTx(tx *sql.Tx, actor, action, target, detail string) error {
	if actor == "" {
		actor = "system"
	}
	if detail == "" || !json.Valid([]byte(detail)) {
		detail = "{}"
	}
	_, err := tx.Exec(`INSERT INTO cn35_admin_audit (actor,action,target,detail,created_at)
		VALUES (?,?,?,?,?)`, actor, action, target, detail, nowUTC())
	return err
}
