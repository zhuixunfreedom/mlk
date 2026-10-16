package cn35store

import (
	"database/sql"
	"fmt"
	"time"
)

const apRecoveryIntervalSeconds int64 = 300

// apRecoveryClockSkewSeconds 是客户端 AP 显示时钟与服务端时钟之间的
// 容忍偏移。客户端 StatusPopup.UpdateDisplay 用 Common.Now——登录时经
// consts.stamp 一次性校准的本地时钟——推算 AP；随会话拉长产生漂移，
// 在 300s 恢复边界上会出现"客户端已显示 1 点 AP、服务端尚未入账"的
// 瞬间误报体力不足（实机 2026-09-30）。服务端按 now+skew 提前入账，
// 保证下发投影不晚于客户端自算，消除边界抖动的假拒绝。
// 漂移上界取登录校准误差+本地时钟漂移（典型 <1s，取 3s 富余）；
// 副作用是 AP 最多提前 skew 秒入账，测试边界断言按 ±skew 书写。
const apRecoveryClockSkewSeconds int64 = 3

// recoverAPTx 在调用方事务内按客户端可见的整数秒差惰性补 AP。
// 300 秒与当前 CN3.5 客户端 StatusPopup.UpdateDisplay 一致；服务端何时
// 触发补算属于本项目重建策略，不代表原服务端的离线入账语义。
func (s *Store) recoverAPTx(tx *sql.Tx, userID string, now time.Time) (int, int, time.Time, error) {
	var ap, maxAP int
	var rawAnchor string
	if err := tx.QueryRow(`SELECT ap,max_ap,ap_updated_at FROM cn35_player_profiles WHERE user_id=?`, userID).
		Scan(&ap, &maxAP, &rawAnchor); err != nil {
		return 0, 0, time.Time{}, err
	}
	anchor, err := time.Parse(time.RFC3339Nano, rawAnchor)
	if err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("cn35store: 无效 AP 时间锚点 user=%s: %w", userID, err)
	}
	anchor = anchor.UTC()
	now = now.UTC()

	// int_ap_updated_at 与 Common.GetServerTime 都按 Unix 整数秒比较；
	// 因而服务端也先取两端 Unix 秒，再计算完整周期，不用浮点持续时间。
	elapsed := now.Unix() + apRecoveryClockSkewSeconds - anchor.Unix()
	if maxAP <= ap || elapsed < apRecoveryIntervalSeconds {
		return ap, maxAP, anchor, nil
	}
	ticks := elapsed / apRecoveryIntervalSeconds
	room := maxAP - ap
	credited := ticks
	if credited > int64(room) {
		credited = int64(room)
	}
	if credited <= 0 {
		return ap, maxAP, anchor, nil
	}
	newAP := ap + int(credited)
	newAnchor := anchor.Add(time.Duration(credited) * time.Duration(apRecoveryIntervalSeconds) * time.Second)
	if newAP >= maxAP {
		// 触顶时丢弃超出上限的累计时长；随后扣点从当前锚点重新计时。
		newAnchor = now
	}
	stamp := newAnchor.UTC().Format(time.RFC3339Nano)
	updated := now.Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE cn35_player_profiles SET ap=?,ap_updated_at=?,updated_at=? WHERE user_id=?`,
		newAP, stamp, updated, userID); err != nil {
		return 0, 0, time.Time{}, err
	}
	return newAP, maxAP, newAnchor, nil
}
