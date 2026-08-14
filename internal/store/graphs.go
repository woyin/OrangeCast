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
func (s *Store) graphCollectionMap(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT ci.collection_id, ki.id
		 FROM collection_items ci
		 JOIN keypoint_index ki ON ki.source_type = ci.source_type
			                             AND ki.source_id = ci.source_id
		                             AND ki.citations_json = ci.segment_ids`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	collectionByKeyPoint := map[string]string{}
	for rows.Next() {
		var colID, kpID string
		if err := rows.Scan(&colID, &kpID); err != nil {
			return nil, err
		}
		collectionByKeyPoint[kpID] = colID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return collectionByKeyPoint, nil
}

func graphNodes(kps []*KeyPointRow, collectionByKeyPoint map[string]string) []KpGraphNode {
	nodes := make([]KpGraphNode, 0, len(kps))
	for _, kp := range kps {
		nodes = append(nodes, KpGraphNode{
			ID: kp.ID, Content: kp.Content, SourceTitle: kp.SourceTitle,
			SourceID: kp.SourceID, SourceType: string(kp.SourceType),
			TimeStart: kp.TimeStart, TimeEnd: kp.TimeEnd,
			Collection: collectionByKeyPoint[kp.ID],
		})
	}
	return nodes
}

func graphCollectionLinks(kps []*KeyPointRow, collectionByKeyPoint map[string]string) ([]KpGraphLink, map[string]bool) {
	collectionMembers := map[string][]string{}
	for _, kp := range kps {
		if collectionID := collectionByKeyPoint[kp.ID]; collectionID != "" {
			collectionMembers[collectionID] = append(collectionMembers[collectionID], kp.ID)
		}
	}
	links := []KpGraphLink{}
	edges := map[string]bool{}
	for _, ids := range collectionMembers {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				links = append(links, KpGraphLink{Source: ids[i], Target: ids[j], Type: "collection"})
				edges[graphEdgeKey(ids[i], ids[j])] = true
			}
		}
	}
	return links, edges
}

func graphSimilarLinks(kps []*KeyPointRow, existing map[string]bool) []KpGraphLink {
	tokenized := make([]map[string]int, len(kps))
	links := []KpGraphLink{}
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
				key := graphEdgeKey(kps[i].ID, kps[j].ID)
				if existing[key] {
					continue
				}
				existing[key] = true
				links = append(links, KpGraphLink{Source: kps[i].ID, Target: kps[j].ID, Type: "similar"})
			}
		}
	}
	return links
}

func graphEdgeKey(source, target string) string {
	if source > target {
		source, target = target, source
	}
	return source + "\x00" + target
}

func (s *Store) GetKpGraph(ctx context.Context) (*KpGraphData, error) {
	kps, _, err := s.ListKeyPoints(ctx, 1, 500)
	if err != nil {
		return nil, err
	}
	collectionByKeyPoint, err := s.graphCollectionMap(ctx)
	if err != nil {
		return nil, err
	}
	collections, err := s.ListCollections(ctx)
	if err != nil {
		return nil, err
	}
	collectionLinks, edges := graphCollectionLinks(kps, collectionByKeyPoint)
	return &KpGraphData{Nodes: graphNodes(kps, collectionByKeyPoint), Links: append(collectionLinks, graphSimilarLinks(kps, edges)...), Collections: collections}, nil
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
