// Package cn35roster 把名册清单（unit_roster_manifest.json）与卡牌目录
// （cardcatalog）装配成 cn35game.RosterImport——mercdb cn35-roster 子命令
// 与 mercd35 启动种子/管理端再导入共用同一条装载路径，避免两处各写。
package cn35roster

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// ManifestFileName 是 assets 目录内清单的固定文件名。
const ManifestFileName = "unit_roster_manifest.json"

// LoadManifest 读并校验清单文件。返回解析产物与文件内容 SHA-256。
func LoadManifest(assetsDir string) (*cn35game.RosterManifest, string, string, error) {
	path := filepath.Join(assetsDir, ManifestFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", path, fmt.Errorf("cn35roster: 读清单 %s 失败: %w", path, err)
	}
	var m cn35game.RosterManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, "", path, fmt.Errorf("cn35roster: 解析清单 %s 失败: %w", path, err)
	}
	if m.ManifestVersion == "" || len(m.Units) == 0 || len(m.Families.Required) == 0 {
		return nil, "", path, fmt.Errorf("cn35roster: 清单 %s 结构不全（version/units/families 必填）", path)
	}
	sum := sha256.Sum256(raw)
	return &m, hex.EncodeToString(sum[:]), path, nil
}

// LoadTemplates 把卡牌目录 units.json 全量记录转成名册模板行。
// 同时返回卡片目录（供调用方继续用于目录校验/兜底）。模板的 Missing
// 语义与 cardcatalog 一致：缺键而非填零。
func LoadTemplates(cardsDir string) ([]cn35game.UnitTemplate, *cardcatalog.Catalog, error) {
	cat, err := cardcatalog.Load(cardsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("cn35roster: 卡牌目录加载失败: %w", err)
	}
	meta := cat.Meta()
	out := make([]cn35game.UnitTemplate, 0, len(cat.UnitIDs()))
	for _, id := range cat.UnitIDs() {
		u, err := cat.Unit(id)
		if err != nil || u == nil {
			continue
		}
		rawJSON := ""
		if u.Raw != nil {
			if b, jerr := json.Marshal(u.Raw); jerr == nil {
				rawJSON = string(b)
			}
		}
		out = append(out, cn35game.UnitTemplate{
			UnitID: u.ID, Name: u.Name, NameJP: u.NameJP, Title: u.Title,
			TitleJP: u.TitleJP, Rare: u.Rare, Element: u.Element, Weapon: u.Weapon,
			Type: u.Type, ANum: u.ANum, Life: u.Life, Atk: u.Atk, MSpd: u.MSpd,
			ASPd: u.ASPd, AArea: u.AArea, Tenacity: u.Tenacity, Hits: u.Hits,
			Fire: u.Rates.Fire, Aqua: u.Rates.Aqua, Wind: u.Rates.Wind,
			Light: u.Rates.Light, Dark: u.Rates.Dark,
			Gender: u.Gender, Age: u.Age, Country: u.Country, Career: u.Career,
			Interest: u.Interest, Nature: u.Nature, Obtain: u.Obtain,
			Remark: u.Remark, WikiGacha: u.Gacha, Contributors: u.Contributors,
			Missing: u.Missing, RawJSON: rawJSON,
			SourceVersion: meta.UnitsVersion,
		})
	}
	return out, cat, nil
}

// LoadImport 组装完整导入输入：清单 + 文件哈希 + 模板 + 来源哈希。
// 校验 units_source 指纹：清单记录的 units.json 哈希必须与当前卡片目录
// 实测一致——不一致说明清单不是针对这份数据生成的，拒绝导入（溯源绑定）。
func LoadImport(assetsDir, cardsDir string) (*cn35game.RosterImport, *cardcatalog.Catalog, error) {
	m, mSHA, mPath, err := LoadManifest(assetsDir)
	if err != nil {
		return nil, nil, err
	}
	tpls, cat, err := LoadTemplates(cardsDir)
	if err != nil {
		return nil, nil, err
	}
	meta := cat.Meta()
	if m.UnitsSource.SHA256 != "" && !strings.EqualFold(m.UnitsSource.SHA256, meta.UnitsSHA256) {
		return nil, nil, fmt.Errorf("cn35roster: 清单绑定的 units.json 指纹 %s 与当前卡片目录实测 %s 不一致（重新生成清单或核对卡片源）",
			m.UnitsSource.SHA256, meta.UnitsSHA256)
	}
	return &cn35game.RosterImport{
		Manifest:     m,
		ManifestPath: mPath,
		ManifestSHA:  mSHA,
		Templates:    tpls,
		UnitsPath:    filepath.Join(cardsDir, "units.json"),
		UnitsSHA:     meta.UnitsSHA256,
		UnitsVersion: meta.UnitsVersion,
	}, cat, nil
}
