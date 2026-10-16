// appconf_admin.go app_conf 页面配置编辑器（PAGE-EDITOR）的存储契约与
// 传输类型。契约由 cn35store.Store 实现，消费方是 admin35 页面编辑器；
// 放本包与 RosterAdminStore 同层——管理面与存储层共享的稳定定义。
package cn35game

import "errors"

// ErrAppConfVersionUnknown 请求的归档版本不存在。
var ErrAppConfVersionUnknown = errors.New("cn35game: app_conf 归档版本不存在")

// AppConfVersionMeta 一份已归档旧文档的元数据（不带文档体）。
type AppConfVersionMeta struct {
	Version   int64  `json:"version"`
	DocSHA256 string `json:"doc_sha256"`
	Size      int    `json:"size"`
	Actor     string `json:"actor"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

// AppConfVersion 一份完整归档：元数据 + 保存前的旧文档原文
// （源 fixture 形态，含 "_" 注解键；非 RenderWire 出包字节）。
type AppConfVersion struct {
	Meta AppConfVersionMeta `json:"meta"`
	Doc  string             `json:"doc"`
}

// AppConfArchiveStore app_conf 版本归档存取契约。
//
// ArchiveAppConf 把「即将被覆盖的旧文档」追加进版本史并写审计行，
// 归档与审计必须同事务提交；doc_sha256 去重时 inserted=false 且返回
// 既有版本号。实现方：cn35store.Store（迁移 0014）。
type AppConfArchiveStore interface {
	// ArchiveAppConf 归档旧文档。prevDoc 为保存前文件完整字节。
	// 返回 (归档版本号, 是否新插入, error)。
	ArchiveAppConf(prevDoc []byte, actor, note string) (int64, bool, error)
	// AppConfVersions 按版本倒序列出归档元数据；limit<=0 由实现层套默认上限。
	AppConfVersions(limit int) ([]AppConfVersionMeta, error)
	// AppConfVersionDoc 取某版本的完整归档；版本不存在返回错误。
	AppConfVersionDoc(version int64) (*AppConfVersion, error)
}
