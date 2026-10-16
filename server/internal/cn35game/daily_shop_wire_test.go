// daily_shop_wire_test.go SHOP350-01：面包店每日 0 元购钻石包协议回放：
// 列表行契约、buy 领取入账、限购置灰与刷新回推。
package cn35game_test

import (
	"testing"
)

func TestDailyFreeDiamondShop(t *testing.T) {
	ts, st, _ := itemServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-daily-shop")

	// 列表：每日免费钻石行（0 元购、未购）+ 店铺信息刷新倒计时。
	data := gameData(t, ts.URL, "/red_diamond_shop/", uid, sk, s0, s1, "")
	rows := data["red_diamond_shop"].([]any)
	if len(rows) != 1 {
		t.Fatalf("商品行 = %v", rows)
	}
	row := rows[0].(map[string]any)
	if row["cost"] != float64(0) || row["cost_type"] != float64(0) {
		t.Fatalf("0 元购价格字段: %v", row)
	}
	if row["bought"] != false {
		t.Fatalf("未购行 bought 应为 false: %v", row)
	}
	info := data["red_diamond_shop_info"].(map[string]any)
	if info["refresh_time"].(float64) <= 0 {
		t.Fatalf("refresh_time 应为正: %v", info)
	}

	// 未知商品 → result:false。
	bad := gameJSON(t, ts.URL, "/red_diamond_shop/buy", uid, sk, s0, s1,
		"id=999", "_method=GET")
	if bad["result"] != false {
		t.Fatalf("未知商品应 result=false: %v", bad)
	}

	// 领取：result:true、回推行集 bought=true、user_coin 入账 100。
	buy := gameJSON(t, ts.URL, "/red_diamond_shop/buy", uid, sk, s0, s1,
		"id=1", "_method=GET")
	if buy["result"] != true {
		t.Fatalf("首购失败: %v", buy)
	}
	bdata := buy["data"].(map[string]any)
	if bdata["user_coin"].(map[string]any)["amount"] != float64(100) {
		t.Fatalf("入账金额: %v", bdata["user_coin"])
	}
	brow := bdata["red_diamond_shop"].([]any)[0].(map[string]any)
	if brow["bought"] != true {
		t.Fatalf("购买后行应为已购: %v", brow)
	}

	// 同周期再领 → result:false（客户端弹 msg），余额不重复入账。
	again := gameJSON(t, ts.URL, "/red_diamond_shop/buy", uid, sk, s0, s1,
		"id=1", "_method=GET")
	if again["result"] != false || again["msg"] == nil {
		t.Fatalf("重复购买应 result=false+msg: %v", again)
	}
	coin := gameData(t, ts.URL, "/users/user_coin", uid, sk, s0, s1, "")["user_coin"].(map[string]any)["amount"]
	if coin != float64(100) {
		t.Fatalf("重复领取后余额 = %v", coin)
	}

	// 重读列表：bought 标记持久化（4 点周期内已购置灰）。
	row2 := gameData(t, ts.URL, "/red_diamond_shop/", uid, sk, s0, s1, "")["red_diamond_shop"].([]any)[0].(map[string]any)
	if row2["bought"] != true {
		t.Fatalf("重读行应为已购: %v", row2)
	}

	// 主人公日常入口：function_portal_list 五入口（对齐原版），面包店行
	// message 带 conf 页键+base。
	fp := gameData(t, ts.URL, "/users/function_portal", uid, sk, s0, s1, "")
	frows := fp["function_portal_list"].([]any)
	if len(frows) != 5 {
		t.Fatalf("function_portal_list = %v", frows)
	}
	var frow map[string]any
	for _, r := range frows {
		if r.(map[string]any)["texture_asset_name"] == "common/bnr_red_diamond_shop" {
			frow = r.(map[string]any)
		}
	}
	if frow == nil ||
		frow["show_page_message"] != "name=RedDiamondShop&base=Common/Pages/RedDiamondShopPage&method=Stack" {
		t.Fatalf("面包店入口行: %v", frows)
	}

	// 手动刷新：result:true、行集回推（库存周期不变仍已购）。
	ref := gameJSON(t, ts.URL, "/red_diamond_shop/refresh", uid, sk, s0, s1,
		"remain=60", "_method=GET")
	if ref["result"] != true {
		t.Fatalf("刷新失败: %v", ref)
	}
}
