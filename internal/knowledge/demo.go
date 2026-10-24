package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
)

// SeedDemo is explicit and only succeeds for an empty library. These invented
// community activity records are fixtures, never a user's personal corpus.
func (s *Store) SeedDemo(id string) (*Library, error) {
	return s.mutate(id, func(lib *Library) error {
		if len(lib.Sources) > 0 || len(lib.Pages) > 0 || len(lib.Proposals) > 0 {
			return problem(409, "demo can only be loaded into an empty library")
		}
		source := func(title, filename, body string) Source {
			h := sha256.Sum256([]byte(body))
			return Source{newID("src"), title, filename, body, hex.EncodeToString(h[:]), "active", now()}
		}
		old := source("阅读活动初稿", "reading-draft.md", "# 青松阅读计划 · 虚构样本\n\n初稿：11 月 1 日 14:00，东区 101 室，预计 18 人。物料预算上限 120 元，尚未采购。\n")
		confirmed := source("阅读活动确认单", "reading-confirmed.txt", "【虚构样本】最终确认：阅读交流改为 11 月 3 日 15:30，在西区 203 室举行，参加人数为 24 人。需准备 24 份阅读卡；预算仍为 120 元。此前时间和地点已被本确认单替代。\n")
		material := source("物料准备记录", "materials.md", "# 物料准备 · 虚构样本\n\n阅读卡待打印，签字笔待清点。120 元是计划预算，不代表已支出。活动结束后整理共读笔记。\n")
		lib.Sources = []Source{old, confirmed, material}
		activity := createPage("活动安排", "# 活动安排\n\n> 虚构样本 · 初稿，待确认单审阅\n\n初步定于 **11 月 1 日 14:00** 在 **东区 101 室** 举行，预计 **18 人**。[source:"+old.ID+"]\n\n准备事项见 [[物料准备]]，整体计划见 [[青松阅读计划]]。", []string{old.ID})
		materials := createPage("物料准备", "# 物料准备\n\n按初稿准备 **18 份阅读卡**。[source:"+old.ID+"]\n\n- [ ] 打印阅读卡\n- [ ] 清点签字笔\n\n预算上限 **120 元**，尚未发生支出。[source:"+material.ID+"]\n\n关联 [[活动安排]]。", []string{old.ID, material.ID})
		overview := createPage("青松阅读计划", "# 青松阅读计划\n\n> 此知识库全部为虚构样本，可体验真实保存、审阅与导出。\n\n每一次记录，都成为下一次行动的依据。\n\n## 项目导航\n\n- [[活动安排]]：时间、地点与参加人数\n- [[物料准备]]：阅读卡、物料与计划预算\n\n## 资料处理规则\n\n保留原件 → 生成待审提案 → 人工核对 → 更新 Wiki。新版活动确认单尚未接受，请从「待审提案」查看两份页面的差异。", []string{})
		lib.Pages = []Page{overview, activity, materials}
		lib.Proposals = []Proposal{{ID: newID("proposal"), Title: "根据确认单更新活动与物料", Kind: "demo", Status: "pending", Created: now(), Changes: []Change{
			{PageID: activity.ID, Title: activity.Title, BaseVersion: 1, SourceIDs: []string{confirmed.ID}, Content: "# 活动安排\n\n> 虚构样本 · 确认版本\n\n活动定于 **11 月 3 日 15:30** 在 **西区 203 室** 举行，参加人数为 **24 人**。本确认单替代此前初稿中的时间、地点与人数。[source:" + confirmed.ID + "]\n\n准备事项见 [[物料准备]]，整体计划见 [[青松阅读计划]]。"},
			{PageID: materials.ID, Title: materials.Title, BaseVersion: 1, SourceIDs: []string{confirmed.ID, material.ID}, Content: "# 物料准备\n\n按确认人数准备 **24 份阅读卡**。[source:" + confirmed.ID + "]\n\n- [ ] 打印 24 份阅读卡\n- [ ] 清点签字笔\n\n预算上限仍为 **120 元**。这是计划预算，不代表已支出。[source:" + material.ID + "]\n\n关联 [[活动安排]]。"},
		}}}
		addLog(lib, "手动载入虚构演示资料：3 份原件、3 篇 Wiki、1 份两页更新提案")
		return nil
	})
}
