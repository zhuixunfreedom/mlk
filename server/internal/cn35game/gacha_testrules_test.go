// gacha_testrules_test.go 测试用默认规则构造（ROSTER350-03 起生产路径
// 只读已发布版本快照；本助手仅服务引擎/读面单测，与种子池数值同源）。
package cn35game

// gachaDefaultRules 构造与种子发布版一致的规则形态：
// 四槽齐全（1=限时停用占位、4=高级金币停用占位在测试里保持原四池结构，
// 引擎单测不经过发布校验的"停用池不入快照"裁剪），钻石池带保底组，
// 金币池带星级分布。数值语义与 GachaSeedPools 一致。
func gachaDefaultRules() gachaRules {
	return gachaRules{
		Version: gachaRulesVersion,
		Pools: []gachaPoolRules{
			{
				PoolID: "1", Slot: 1, ListTypeID: 0, Limited: true,
				// 规则面限时池必须带可开放时间窗（引擎不读时间，仅
				// rules.validate/读面使用）；生产侧停用占位池 begin/end=0
				// 不会进入快照，这里给固定窗口保持 validate 通过。
				BeginAt:            1700000000,
				EndAt:              2000000000,
				Name:               "限时招募",
				Description:        "本期推荐角色出现率提升。\\n本池为服务端重建规则，非原服数值。",
				Price:              gachaPrice{Coin: 5},
				MaxExecutableCount: 10,
				PityGroup:          gachaPityGroupLimitedUP,
			},
			{
				PoolID: "2", Slot: 2, ListTypeID: 1,
				Name:               "钻石招募",
				Description:        "使用钻石招募同伴。\\n每连续 10 次至少获得 1 名 SR 以上。",
				Price:              gachaPrice{Coin: 5},
				MaxExecutableCount: 10,
				PityGroup:          gachaPityGroupDiamond,
			},
			{
				PoolID: "3", Slot: 3, ListTypeID: 2,
				Name:               "金币招募",
				Description:        "使用金币招募同伴。\\n本池无保底，招募概率固定。",
				Price:              gachaPrice{Point: 200},
				MaxExecutableCount: 10,
				Distribution:       map[int]int{gachaRarityN: 70, gachaRarityR: 24, gachaRarityHR: 5, gachaRaritySR: 1},
			},
			{
				PoolID: "4", Slot: 4, ListTypeID: 3,
				Name:               "高级金币招募",
				Description:        "使用金币招募更高稀有度的同伴。\\n本池无保底，招募概率固定。",
				Price:              gachaPrice{Point: 2000},
				MaxExecutableCount: 10,
				Distribution:       map[int]int{gachaRarityR: 80, gachaRarityHR: 18, gachaRaritySR: 2},
			},
		},
		Pity: map[string]gachaPityRules{
			gachaPityGroupDiamond: {
				Enabled: true, URBasePercent: 2, URSoftStart: 50,
				URSoftStepPercent: 2, URHardPity: 80, SRPity: 10,
			},
			gachaPityGroupLimitedUP: {
				Enabled: true, URBasePercent: 2, URSoftStart: 50,
				URSoftStepPercent: 2, URHardPity: 80, SRPity: 10,
				UPGuarantee: true,
			},
		},
		DuplicateCompensation: map[int]gachaDuplicateReward{
			gachaRarityN:  {Medal: 10},
			gachaRarityR:  {Medal: 20},
			gachaRarityHR: {Medal: 50},
			gachaRaritySR: {Medal: 200},
			gachaRarityUR: {Medal: 1000},
		},
		DuplicateMaxCount: 15,
	}
}
