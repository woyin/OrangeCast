package provider

// MapCitedToSources 把模型返回的 cited 索引映射为实际引用的 Segment Source（ADR-0008 / Phase 7）。
// 只保留模型明确引用的片段；索引越界或重复的忽略。绝不附加"被检索到"但未被引用的片段。
func MapCitedToSources(chunks []Chunk, cited []int) []Source {
	seen := map[int]bool{}
	var out []Source
	for _, idx := range cited {
		if idx < 0 || idx >= len(chunks) || seen[idx] {
			continue
		}
		seen[idx] = true
		out = append(out, Source{
			SegmentID: chunks[idx].SegmentID,
			Content:   chunks[idx].Text,
			Start:     chunks[idx].Start,
			End:       chunks[idx].End,
		})
	}
	return out
}

// HasReliableSources 判断 QAResult 是否具备可核验的引用（Phase 7 拒答依据）。
func HasReliableSources(res *QAResult) bool {
	return res != nil && len(res.Sources) > 0 && res.Answer != ""
}
