package store

// 本文件定义 KeyPoint 粒度知识图谱（ADR-0017），与 KeyPoint 索引（keypoints.go）分离：
// 图谱是索引之上的只读投影，用于 /graph 页面的力导向可视化。

import (
	"context"
	"strings"
)

// KpGraphNode KeyPoint 图谱节点。
type KpGraphNode struct {
	ID          string  `json:"id"`
	Content     string  `json:"content"`
	SourceTitle string  `json:"source_title"`
	SourceID    string  `json:"source_id"`
	SourceType  string  `json:"source_type"`
	TimeStart   float64 `json:"time_start"`
	TimeEnd     float64 `json:"time_end"`
	Collection  string  `json:"collection"` // 所属 Collection ID（空=未归类）
}

// KpGraphLink KeyPoint 图谱边。
type KpGraphLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"` // "collection"（实线）| "similar"（虚线建议）
}

// KpGraphData KeyPoint 图谱完整数据。
type KpGraphData struct {
	Nodes       []KpGraphNode `json:"nodes"`
	Links       []KpGraphLink `json:"links"`
	Collections []*Collection `json:"collections"`
}

// GetKpGraph 返回 KeyPoint 粒度图谱（ADR-0017）。
// 节点 = keypoint_index 中的每行 KeyPoint。
// 实线边 = 同一 Collection 的 KeyPoint 两两连接（Owner 组织的跨 Source 主题集）。
// 虚线边 = 文本相似度建议（Jaccard 词重叠 ≥0.3，跨 Episode，且不重复已有实线）。
// 用于 /graph 页面的力导向可视化。
func (s *Store) GetKpGraph(ctx context.Context) (*KpGraphData, error) {
	// 1) 全部 KeyPoint
	kps, _, err := s.ListKeyPoints(ctx, 1, 500) // 上限 500 条
	if err != nil {
		return nil, err
	}

	// 2) Collection 成员映射：keypoint_id → collection_id
	//    collection_items 按 (source_type, source_id, segment_ids) 关联 keypoint_index
	ciRows, err := s.DB.QueryContext(ctx,
		`SELECT ci.collection_id, ki.id
		 FROM collection_items ci
		 JOIN keypoint_index ki ON ki.source_type = ci.source_type
		                             AND ki.source_id = ci.source_id
		                             AND ki.citations_json = ci.segment_ids`)
	if err != nil {
		return nil, err
	}
	defer ciRows.Close()
	kpToCol := map[string]string{}
	for ciRows.Next() {
		var colID, kpID string
		if err := ciRows.Scan(&colID, &kpID); err != nil {
			return nil, err
		}
		kpToCol[kpID] = colID
	}

	// 3) Collections 列表
	cols, err := s.ListCollections(ctx)
	if err != nil {
		return nil, err
	}

	gd := &KpGraphData{Collections: cols}

	// 4) 节点
	for _, kp := range kps {
		col := kpToCol[kp.ID]
		gd.Nodes = append(gd.Nodes, KpGraphNode{
			ID: kp.ID, Content: kp.Content, SourceTitle: kp.SourceTitle,
			SourceID: kp.SourceID, SourceType: string(kp.SourceType),
			TimeStart: kp.TimeStart, TimeEnd: kp.TimeEnd,
			Collection: col,
		})
	}

	// 5) 实线边：同一 Collection 的 KeyPoint 两两连接
	colMembers := map[string][]string{} // collection_id → []keypoint_id
	for _, kp := range kps {
		if c := kpToCol[kp.ID]; c != "" {
			colMembers[c] = append(colMembers[c], kp.ID)
		}
	}
	for _, ids := range colMembers {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				gd.Links = append(gd.Links, KpGraphLink{Source: ids[i], Target: ids[j], Type: "collection"})
			}
		}
	}

	// 6) 虚线边：文本相似度建议（词重叠 > 0.3，且不在同一 Episode）
	tokenized := make([]map[string]int, len(kps))
	for i, kp := range kps {
		tokenized[i] = tokenizeKp(kp.Content + " " + kp.Description)
	}
	for i := 0; i < len(kps); i++ {
		for j := i + 1; j < len(kps); j++ {
			if kps[i].SourceID == kps[j].SourceID {
				continue // 同一 Episode 不连
			}
			sim := jaccard(tokenized[i], tokenized[j])
			if sim >= 0.3 {
				// 避免和已有 collection 边重复
				alreadyLinked := false
				for _, l := range gd.Links {
					if (l.Source == kps[i].ID && l.Target == kps[j].ID) ||
						(l.Source == kps[j].ID && l.Target == kps[i].ID) {
						alreadyLinked = true
						break
					}
				}
				if !alreadyLinked {
					gd.Links = append(gd.Links, KpGraphLink{Source: kps[i].ID, Target: kps[j].ID, Type: "similar"})
				}
			}
		}
	}

	return gd, nil
}

// tokenizeKp 简单分词（中英文混合：英文按空格，中文 bigram）。
func tokenizeKp(s string) map[string]int {
	s = strings.ToLower(s)
	tokens := map[string]int{}
	var cjk []rune
	flushCJK := func() {
		for i := 0; i+1 < len(cjk); i++ {
			bg := string(cjk[i : i+2])
			if bg != "" {
				tokens[bg]++
			}
		}
		cjk = cjk[:0]
	}
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk = append(cjk, r)
		} else if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cjk = append(cjk, r) // 复用 cjk 缓冲区收集拉丁
		} else {
			flushCJK()
			// 处理已收集的拉丁部分
		}
	}
	flushCJK()
	// 简单英文分词
	for _, w := range strings.Fields(s) {
		w = strings.Trim(w, ".,!?;:\"'()[]{}")
		if len(w) > 1 {
			tokens[strings.ToLower(w)]++
		}
	}
	return tokens
}

// jaccard Jaccard 相似度（交集/并集）。
func jaccard(a, b map[string]int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersect := 0
	union := 0
	for k, va := range a {
		union += va
		if vb, ok := b[k]; ok {
			if va < vb {
				intersect += va
			} else {
				intersect += vb
			}
		}
	}
	for k, vb := range b {
		if _, ok := a[k]; !ok {
			union += vb
		}
	}
	if union == 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}
