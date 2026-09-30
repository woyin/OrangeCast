package store

import (
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/woyin/orangecast/internal/models"
	"modernc.org/sqlite"
)

func init() {
	// Deterministic token projection is shared by SQL triggers and rebuilds.
	sqlite.MustRegisterDeterministicScalarFunction("cwp_search_tokens", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil {
			return "", nil
		}
		return knowledgeTokens(fmt.Sprint(args[0])), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("cwp_document_segments", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		doc := &models.Document{ID: fmt.Sprint(args[0]), Content: fmt.Sprint(args[1])}
		segments := DocumentSegments(doc)
		raw, err := json.Marshal(segments)
		return string(raw), err
	})
}
func knowledgeTokens(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	unique := map[string]bool{}
	var out []string
	add := func(token string) {
		if !unique[token] {
			unique[token] = true
			out = append(out, token)
		}
	}
	for _, word := range words {
		add(word)
		runes := []rune(word)
		for i, r := range runes {
			if unicode.Is(unicode.Han, r) {
				add("h" + hex.EncodeToString([]byte(string(r))))
				if i+1 < len(runes) && unicode.Is(unicode.Han, runes[i+1]) {
					add("h" + hex.EncodeToString([]byte(string(runes[i:i+2]))))
				}
			}
		}
	}
	return strings.Join(out, " ")
}
