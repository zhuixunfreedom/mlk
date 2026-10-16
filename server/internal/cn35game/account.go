// account.go CN 3.5.0 verify 通道入口：POST /account?uid=666666。
//
// 契约：docs/协议对接/cn350/contracts/00-transport.md §1-3 +
// 01~05 方法卡。请求 type3 帧 → DecodeVerifyRequest 拆信封 → 按 method
// 分发 → WriteVerifyResponse 组 type300 帧（chooseServer 成功时播种 S0）。
//
// 分发语义对齐契约 05 汇总表：
//   - 必经：metaInfo / heQuickLogin / heLogin / getServerList / chooseServer
//   - 条件：ucLogin / heRegister / heRegCheck / heAccountBind（已实现，验收走 HE 侧）
//   - 兜底：未知 method（含平台 identifier 动态方法）回 {method,retCode:200}
package cn35game

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// verifyHandler 处理一条 /account 请求。解码失败直接错误返回——
// 客户端 executeVerify 对非 200 只记失败回调，安全。
func verifyHandler(dep Deps) httpapi.Handler {
	return func(c *httpapi.Ctx) error {
		req, err := cn35wire.DecodeVerifyRequest(c.Body)
		if err != nil {
			return err // ErrDecode/ErrEnvelopeJSON → 500 诊断（帧非法无有效响应形态）
		}
		results := make([]cn35wire.VerifyResult, 0, len(req.Business))
		events := make([]map[string]any, 0, 1)
		var seed string
		for i, raw := range req.Business {
			method := req.Methods[i]
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				fields = map[string]any{}
			}
			res, isNew, s0, err := dep.dispatchVerify(method, fields, req)
			if err != nil {
				dep.log().Error("CN3.5 /account 业务失败", "method", method, "err", err)
				res = cn35wire.VerifyResult{"method": method, "retCode": 501}
			}
			if _, ok := res["method"]; !ok {
				res["method"] = method
			}
			results = append(results, res)
			if isNew {
				events = append(events, map[string]any{"firstActive": true})
			}
			if s0 != "" {
				seed = s0
			}
		}
		c.Decoded = map[string]any{
			"methods": req.Methods, "wrapped": req.Wrapped,
			"sub_v": req.SubHeader["v"], "seeded": seed != "",
		}
		return cn35wire.WriteVerifyResponse(c.W, results, events, req.Methods, "", seed)
	}
}

// dispatchVerify 按 method 分发一条业务文档。
// 返回 (result, isNewAccount, s0Seed, err)；s0Seed 仅 chooseServer 成功时非空。
func (dep Deps) dispatchVerify(method string, f map[string]any, req *cn35wire.VerifyRequest) (cn35wire.VerifyResult, bool, string, error) {
	str := func(k string) string { v, _ := f[k].(string); return v }
	switch method {
	case "metaInfo":
		return cn35wire.VerifyResult{}, false, "", nil

	case "heQuickLogin":
		r, err := dep.Accounts.QuickLogin(str("deviceId"))
		if err != nil {
			return loginFail(err), false, "", nil
		}
		return cn35wire.VerifyResult{
			"success": true, "randomStr": str("randomStr"),
			"heAccountId": r.HeAccountID, "token": r.Token, "msg": "",
		}, r.New, "", nil

	case "heLogin":
		r, err := dep.Accounts.Login(str("userName"), str("pwd"), str("deviceId"))
		if err != nil {
			return loginFail(err), false, "", nil
		}
		return cn35wire.VerifyResult{
			"success": true, "heAccountId": r.HeAccountID,
			"token": r.Token, "msg": "",
		}, false, "", nil

	case "heRegister":
		r, err := dep.Accounts.Register(str("accountName"), str("pwd"), str("deviceId"), str("pf"), str("bindEmail"))
		if err != nil {
			return loginFail(err), false, "", nil
		}
		return cn35wire.VerifyResult{
			"success": true, "heAccountId": r.HeAccountID,
			"token": r.Token, "msg": "",
		}, r.New, "", nil

	case "heAccountBind":
		// 绑定/转正/换机继承三面板共用入口（契约 05）。客户端 success 分支
		// 回写 heAccountId+token 并清除游客标记；失败只展示 msg——响应必须
		// 恒带 success/msg，否则旧缺陷（缺 success → 客户端 NRE 软锁）复现。
		r, err := dep.Accounts.AccountBind(str("heAccountId"), str("token"),
			str("accountName"), str("accountPwd"), str("deviceId"), str("pf"), str("bindEmail"))
		if err != nil {
			return loginFail(err), false, "", nil
		}
		return cn35wire.VerifyResult{
			"success": true, "heAccountId": r.HeAccountID,
			"token": r.Token, "msg": "绑定成功",
		}, false, "", nil

	case "ucLogin":
		r, err := dep.Accounts.SDKLogin(str("accountId"), str("accessToken"), str("loginExtend"), str("deviceId"))
		if err != nil {
			return loginFail(err), false, "", nil
		}
		return cn35wire.VerifyResult{
			"success": true, "randomStr": str("randomStr"),
			"heAccountId": r.HeAccountID, "token": r.Token, "msg": "",
		}, r.New, "", nil

	case "heRegCheck":
		return cn35wire.VerifyResult{"ok": dep.Accounts.NameAvailable(str("accountName"))}, false, "", nil

	case "getServerList":
		// 服表是部署配置（契约 03：static configuration）；三个 host 同指
		// 本进程——asset_info/app_conf/bundle 均由本服务出包。
		entry := map[string]any{
			"serverIP":          dep.ServerHost,
			"serverId":          "1",
			"serverName":        "CN350 本地服",
			"serverStatus":      "1",
			"assetInfoServerIp": dep.ServerHost,
			"staticHostIp":      dep.ServerHost,
		}
		last := dep.Accounts.LastServer(str("heAccountId"))
		lastList := []map[string]any{}
		if last != "" {
			lastEntry := map[string]any{}
			for k, v := range entry {
				lastEntry[k] = v
			}
			lastEntry["serverId"] = last
			lastList = append(lastList, lastEntry)
		}
		return cn35wire.VerifyResult{
			"success":         true,
			"servers":         []map[string]any{entry},
			"lastLoginServer": lastList,
		}, false, "", nil

	case "chooseServer":
		userID, sk, err := dep.Accounts.ChooseServer(str("heAccountId"), str("token"), str("serverId"))
		if err != nil {
			return loginFail(err), false, "", nil
		}
		s0, err := cn35wire.NewS0()
		if err != nil {
			return nil, false, "", err
		}
		if err := dep.Sessions.Save(cn35wire.Session{
			UserID: userID, SK: sk, S0: s0, S1: req.S1,
		}); err != nil {
			return nil, false, "", err
		}
		dep.log().Info("CN3.5 chooseServer 会话绑定",
			"userId", userID, "serverId", str("serverId"), "s1_len", len(req.S1))
		return cn35wire.VerifyResult{"success": true, "userId": userID, "sk": sk}, false, s0, nil
	}
	// 兜底：平台动态 method（platform.identifier 等）回 echo+200（契约 05）。
	return cn35wire.VerifyResult{}, false, "", nil
}

// loginFail 把受控账号错误映射为客户端可显示的 success:false 回执。
func loginFail(err error) cn35wire.VerifyResult {
	msg := "登录失败"
	switch {
	case errors.Is(err, ErrNoAccount):
		msg = "账号不存在"
	case errors.Is(err, ErrBadCredential):
		msg = "账号或凭据错误"
	case errors.Is(err, ErrNameTaken):
		msg = "账号名已占用"
	case errors.Is(err, ErrAlreadyBound):
		msg = "该账号已完成绑定"
	}
	return cn35wire.VerifyResult{"success": false, "msg": msg}
}

func (dep Deps) log() *slog.Logger {
	if dep.Log != nil {
		return dep.Log
	}
	return slog.Default()
}
