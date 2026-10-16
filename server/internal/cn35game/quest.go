// quest.go CN 3.5.0 剧情关卡目录读面的领域类型与账号进度接口
// （STORY350-03）。
//
// 分层约定：
//
//	静态目录 —— assets/cn350/serve_quest_catalog.json 的 stage/area/quest
//	  定义（quest_catalog.go 解析校验）。客户端不硬编码 stages/areas/quests
//	  端点路径；data_key/requireData.path 形状由本包组装进文档
//	  （area_select_option/quest_select_option 子文档，UnitFriendshipListParts
//	  .cs:248 先例形状），04 按 report 契约写 requireData.path。
//	账号进度 —— QuestStore 接口按账号返回 stage_id/area_id/quest_id/
//	  hard_mode_stage_id 指针与各难度通关计数。SQLite 实现在 cn35store
//	  （0006 cn35_quest_progress/cn35_quest_clears）；写面（通关推进、
//	  clear_count 递增）归 STORY350-07，本任务只读。
//
// 解锁规则（本实现定义，客户端行为已证）：
//
//	stage 解锁 ⟺ stage.id <= progress.StageID（客户端 ListFilterComponent
//	  同式再过滤一次，双门等价）；
//	area 解锁 ⟺ area.StageID < progress.StageID，或同 stage 时
//	  area.id <= progress.AreaID；area 未解锁则该区域不在 areas_* 列表，
//	  其 quests_* 键为空表——未解锁不能进入（客户端无区域/关卡锁渲染分支）；
//	quest 解锁 ⟺ quest.id <= progress.QuestID（catalog 约定 quest.id 按
//	  推进顺序全局递增，见 serve_quest_catalog.json _provenance）。
package cn35game

import (
	"sync"
)

// QuestProgress 一个账号的剧情进度快照（对应 cn35_quest_progress 行
// + cn35_quest_clears 聚合计数）。
type QuestProgress struct {
	StageID         int // 当前 stage 指针（quest0 初始实证 1）
	AreaID          int // 当前 area 指针（初始 1）
	QuestID         int // 前沿 quest id：id<=QuestID 的关卡可玩（初始 1）
	HardModeStageID int // HardMode tab 过滤指针（初始 0=未开放任何）
	// Clears[questID][difficulty] = 通关次数 → 关卡文档 {difficulty}_clear。
	// difficulty ∈ normal/hard/very_hard（0006 CHECK 白名单）。
	Clears map[int]map[string]int
}

// QuestStore 剧情进度读面。空进度（无表行）按初始值 (1,1,1,0)+无通关
// 返回，不算错误——"从未推进"与"初始行"语义等价。
type QuestStore interface {
	QuestProgress(userID string) (*QuestProgress, error)
}

// questDifficulty 客户端难度键全集（与 use_ap_map/{diff}_clear 键一致）。
// impossible 键型存在但本批目录未启用，0006 CHECK 未收纳。
var questDifficulties = []string{"normal", "hard", "very_hard"}

// ---- MemoryQuests：进程内受控桩 ----

// MemoryQuests 不落盘的 QuestStore（-mem 联调与单测用）。
// Seed* 方法仅供测试/开发构造进度状态；HTTP 面没有写入口——
// 通关写入属 STORY350-07 的事务路径。
type MemoryQuests struct {
	mu    sync.Mutex
	byUID map[string]*QuestProgress
}

// NewMemoryQuests 构造空进度表。
func NewMemoryQuests() *MemoryQuests {
	return &MemoryQuests{byUID: make(map[string]*QuestProgress)}
}

// QuestProgress 实现 QuestStore：无记录 → 初始进度副本。
func (m *MemoryQuests) QuestProgress(userID string) (*QuestProgress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.byUID[userID]; ok {
		out := *p
		out.Clears = make(map[int]map[string]int, len(p.Clears))
		for q, ds := range p.Clears {
			cp := make(map[string]int, len(ds))
			for d, n := range ds {
				cp[d] = n
			}
			out.Clears[q] = cp
		}
		return &out, nil
	}
	return &QuestProgress{StageID: 1, AreaID: 1, QuestID: 1, Clears: map[int]map[string]int{}}, nil
}

// SeedProgress 测试/联调用：覆盖账号进度指针。
func (m *MemoryQuests) SeedProgress(userID string, stageID, areaID, questID, hardModeStageID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.byUID[userID]
	if p == nil {
		p = &QuestProgress{Clears: map[int]map[string]int{}}
		m.byUID[userID] = p
	}
	p.StageID, p.AreaID, p.QuestID, p.HardModeStageID = stageID, areaID, questID, hardModeStageID
}

// SeedClear 测试/联调用：登记一条通关计数（幂等覆盖）。
func (m *MemoryQuests) SeedClear(userID string, questID int, difficulty string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.byUID[userID]
	if p == nil {
		p = &QuestProgress{StageID: 1, AreaID: 1, QuestID: 1, Clears: map[int]map[string]int{}}
		m.byUID[userID] = p
	}
	if p.Clears == nil {
		p.Clears = map[int]map[string]int{}
	}
	if p.Clears[questID] == nil {
		p.Clears[questID] = map[string]int{}
	}
	p.Clears[questID][difficulty] = count
}
