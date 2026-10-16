package cn35game

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestActPagesIDsMatchAppConfBanners 断言 /act_pages 返回的 id 与 app_conf
// partials.CarouselBannerPartial.CarouselBanner.banners 的键集一致。
//
// 这是客户端 BasicParts(top_activity) 的硬要求：它取 activityList[0] 作键去查
// appConfBanners，查不到则 docHash 返回 null → 大图静默不显示
// （BasicParts.cs:216-232 有 try/catch，不会报错）。
// 二者是同一份配置的两半，漂移即静默失败，故必须由测试锁住。
//
// 2026-09-27（LEGACY-ASSETS-01）：真实关服前配置接入后，/act_pages 的 id 由
// app_conf 实况派生（loadActivityPageIDs），本测试即断言该派生结果与配置一致，
// 并锁住「首项必须可出图」与「资源走 common/*」两条客户端约束。
func TestActPagesIDsMatchAppConfBanners(t *testing.T) {
	assetsDir := filepath.Join("..", "..", "assets", "cn350")
	confPath := filepath.Join(assetsDir, "serve_app_conf_android.json")
	raw, err := os.ReadFile(confPath)
	if err != nil {
		t.Skipf("跳过：读不到 app_conf（%v）", err)
	}
	var conf struct {
		Partials struct {
			CarouselBannerPartial struct {
				CarouselBanner struct {
					Banners map[string]struct {
						AssetName    string `json:"asset_name"`
						BigAssetName string `json:"big_assetname"`
					} `json:"banners"`
				} `json:"CarouselBanner"`
			} `json:"CarouselBannerPartial"`
		} `json:"partials"`
	}
	if err := json.Unmarshal(raw, &conf); err != nil {
		t.Fatalf("解析 app_conf: %v", err)
	}
	banners := conf.Partials.CarouselBannerPartial.CarouselBanner.Banners
	if len(banners) == 0 {
		t.Fatal("app_conf 未定义 banners")
	}

	ids := loadActivityPageIDs(assetsDir)

	// 1) 每个 /act_pages id 必须命中 banners 键。
	for _, id := range ids {
		if _, ok := banners[id]; !ok {
			t.Fatalf("/act_pages 含 id %q，但 app_conf banners 无此键（大图将静默不显示）", id)
		}
	}

	// 2) 首项必须有 big_assetname + asset_name —— top_activity 只读 activityList[0]。
	//    真实配置里排在首位且不具备出图条件的键不能当首项，否则 Home 大图空白。
	//    故服务器取配置键序；若首项缺图，这里如实报出（不静默跳过）。
	if len(ids) > 0 {
		first := banners[ids[0]]
		if first.BigAssetName == "" {
			t.Errorf("banners[%q] 缺 big_assetname：top_activity 大图无图可取", ids[0])
		}
		if first.AssetName == "" {
			t.Errorf("banners[%q] 缺 asset_name：轮播无图可取", ids[0])
		}
	}

	// 3) 所有「实际会出图」的 banner 资源必须走 common/* ——
	//    项目已证只有 common/* 通道在 CN3.5 客户端可出图
	//    （background/* 走 InitBackgroundAssetBundle 恒中 blankTexture，
	//     见 artifacts/tasks/story350-01/run/baseline.md）。
	//    真实 2017 配置里部分 banner 的 asset_name 留空（由运营期填），
	//    空值不算违规；非空值必须 common/*。
	for id, b := range banners {
		for _, name := range []string{b.AssetName, b.BigAssetName} {
			if name == "" {
				continue
			}
			if len(name) < 7 || name[:7] != "common/" {
				t.Fatalf("banners[%s] 资源 %q 非 common/*：CN3.5 客户端可能不出图", id, name)
			}
		}
	}
}

// TestActPagesHandlerShape：端点返回形状与客户端消费键一致。
func TestActPagesHandlerShape(t *testing.T) {
	ids := loadActivityPageIDs(filepath.Join("..", "..", "assets", "cn350"))
	if len(ids) == 0 {
		t.Fatal("activityPageIDs 为空：activityList 空会导致大图分支不进入")
	}
	for _, id := range ids {
		if id == "" {
			t.Fatal("activityPageIDs 含空 id")
		}
	}
}
