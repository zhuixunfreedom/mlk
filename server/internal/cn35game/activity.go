// activity.go CN 3.5.0 活动页/横幅端点（CN35-UI-RESTORE）。
//
// 契约依据（客户端消费侧，ilspy-cs 反编译）：
//   - DataCache.cs:265-267：缓存键 "act_pages" → common.activityList = _value.docList
//   - Common.cs:713-714：appConfBanners =
//     app_conf.partials.CarouselBannerPartial.CarouselBanner.banners（docHash）
//   - BasicParts.cs:207-250（texture widget，top_activity=true）：
//     取 activityList[0]（字符串键）→ appConfBanners[key] → 读其 big_assetname
//     覆盖本 widget 的 asset_name，并把该 banner 的 OnClick 复制过来。
//     故服务端必须保证 activityList[0] 是 **banners 里存在的键**，
//     否则 docHash 取到 null（客户端有 try/catch，表现为大图静默不显示）。
//   - CarouselBannerParts：数据源同为 appConfBanners，按 banners 键序渲染轮播。
//
// 因此 /act_pages 返回的 id 必须与 app_conf partials 的 banners 键一一对应。
// 二者是同一份配置的两半：改一处必须改另一处（本文件与 serve_app_conf_android.json）。
package cn35game

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// fallbackActivityPageIDs 仅在 app_conf 读取失败时使用的兜底 id 列表。
//
// 2026-09-27（LEGACY-ASSETS-01）起，/act_pages 的 id **改为从 app_conf 实况派生**
// （见 loadActivityPageIDs）：关服前真实配置里 banners 有 55 个生产键
// （banner_raid / banner_collection / banner_minievent ...），此前服务端硬编码的
// act1/act2/act3 并不存在于真实配置，会让 top_activity 大图静默不显示。
// 配置是唯一真源，本变量只在读不到 fixture 时兜底，不再作为常规取值。
var fallbackActivityPageIDs = []string{"banner_raid", "banner_collection", "banner_minievent"}

// activityIDsCache 缓存从 app_conf 解析出的 banners 键序（按资产目录粒度）。
// app_conf 是静态 fixture，进程内缓存即可；测试与重启会重新读取。
var (
	activityIDsMu    sync.Mutex
	activityIDsCache = map[string][]string{}
)

// loadActivityPageIDs 按 app_conf 文档键序返回 CarouselBanner.banners 的键。
//
// 顺序有语义：客户端 BasicParts 的 top_activity 只消费 activityList[0]，
// 故首项决定左侧「活动大图」展示哪张——真源即配置里的键序，不再由服务端指定。
func loadActivityPageIDs(assetsDir string) []string {
	activityIDsMu.Lock()
	defer activityIDsMu.Unlock()
	if ids, ok := activityIDsCache[assetsDir]; ok && len(ids) > 0 {
		return ids
	}
	raw, err := os.ReadFile(filepath.Join(assetsDir, appConfFile))
	if err != nil {
		return fallbackActivityPageIDs
	}
	ids := bannersKeyOrder(raw)
	if len(ids) == 0 {
		return fallbackActivityPageIDs
	}
	activityIDsCache[assetsDir] = ids
	return ids
}

// bannersKeyOrder 从 app_conf 原文中提取 partials.CarouselBannerPartial.
// CarouselBanner.banners 的键序。
//
// 为什么要保序：客户端 BasicParts 的 top_activity 只消费 activityList[0]，
// 键序即展示优先级，map[string]any 会丢序，故用 json.Decoder 流式下降。
func bannersKeyOrder(raw []byte) []string {
	root, err := decodeOrdered(raw)
	if err != nil {
		return nil
	}
	partials := childObject(root, "partials")
	cbp := childObject(partials, "CarouselBannerPartial")
	cb := childObject(cbp, "CarouselBanner")
	bn := childObject(cb, "banners")
	if bn == nil {
		return nil
	}
	keys := make([]string, 0, len(bn.pairs))
	for _, p := range bn.pairs {
		keys = append(keys, p.key)
	}
	return keys
}

// orderedObject 是保序的 JSON 对象节点；仅需要键序，故值只区分「对象/非对象」。
type orderedObject struct {
	pairs []orderedPair
}

type orderedPair struct {
	key string
	val *orderedObject // 非对象为 nil
}

// decodeOrdered 解析 JSON 顶层对象并保留成员顺序。
func decodeOrdered(raw []byte) (*orderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("app_conf 顶层不是对象")
	}
	return readObject(dec)
}

// readObject 读取一个对象体（'{' 已消费），递归保留键序。
func readObject(dec *json.Decoder) (*orderedObject, error) {
	obj := &orderedObject{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		vt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if d, ok := vt.(json.Delim); ok && d == '{' {
			sub, err := readObject(dec)
			if err != nil {
				return nil, err
			}
			obj.pairs = append(obj.pairs, orderedPair{key: key, val: sub})
			continue
		}
		obj.pairs = append(obj.pairs, orderedPair{key: key})
		if err := skipRemainder(dec, vt); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil { // 消费 '}'
		return nil, err
	}
	return obj, nil
}

// skipRemainder 消费非对象值；若首 token 是 '[' 则跳过整个数组。
func skipRemainder(dec *json.Decoder, first json.Token) error {
	d, ok := first.(json.Delim)
	if !ok || d != '[' {
		return nil
	}
	for dec.More() {
		vt, err := dec.Token()
		if err != nil {
			return err
		}
		if inner, ok := vt.(json.Delim); ok && inner == '{' {
			if _, err := readObject(dec); err != nil {
				return err
			}
			continue
		}
		if err := skipRemainder(dec, vt); err != nil {
			return err
		}
	}
	_, err := dec.Token() // ']'
	return err
}

// childObject 按键取子对象，未命中返回 nil。
func childObject(o *orderedObject, key string) *orderedObject {
	if o == nil {
		return nil
	}
	for _, p := range o.pairs {
		if p.key == key {
			return p.val
		}
	}
	return nil
}

// actPagesHandler GET /act_pages：活动页 id 列表 + 红点标志。
//
// 为什么返回非空：BasicParts 的 top_activity 分支要求
// activityList.Count > 0 才会去 appConfBanners 取图；返回空数组会让
// 左侧活动大图恒为空白。act_has_new 驱动 Home 的活动图标红点。
func actPagesHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		ids := loadActivityPageIDs(dep.AssetsDir)
		pages := make([]any, 0, len(ids))
		for _, id := range ids {
			pages = append(pages, id)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "act_pages",
			"act_page_count": len(pages), "act_has_new": true,
			"first": ids[0], "source": "app_conf.banners",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"act_pages":   pages,
				"act_has_new": true,
			},
		})
	})
}
