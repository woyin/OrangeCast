package server

import "strings"

type revisionDiffLine struct{ Kind, Text string }

// lineDiff returns a stable line-oriented LCS diff suitable for editorial review.
func lineDiff(before, after string) []revisionDiffLine {
	a, b := diffTextLines(before), diffTextLines(after)
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []revisionDiffLine
	for i, j := 0, 0; i < len(a) || j < len(b); {
		switch {
		case i == len(a):
			out = append(out, revisionDiffLine{"added", b[j]})
			j++
		case j == len(b):
			out = append(out, revisionDiffLine{"removed", a[i]})
			i++
		case a[i] == b[j]:
			out = append(out, revisionDiffLine{"unchanged", a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, revisionDiffLine{"removed", a[i]})
			i++
		default:
			out = append(out, revisionDiffLine{"added", b[j]})
			j++
		}
	}
	return out
}
func diffTextLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
