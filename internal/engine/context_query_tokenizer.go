package engine

import (
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"
)

// QueryTokenizer is the context --query-only tokenization seam. It is
// intentionally not used by keywordsFromText or conflict scoring.
type QueryTokenizer interface {
	Terms(text string) []string
}

type gseQueryTokenizer struct{}

var defaultQueryTokenizer QueryTokenizer = gseQueryTokenizer{}

var mainlineCJKDomainTerms = []string{
	"继承约束",
	"重新引入",
	"反模式",
	"反模式传播",
	"确认机制",
	"中文分词",
	"上下文检索",
	"召回质量",
}

var mainlineCJKDomainTermSet = map[string]bool{
	"继承约束":  true,
	"重新引入":  true,
	"反模式":   true,
	"反模式传播": true,
	"确认机制":  true,
	"中文分词":  true,
	"上下文检索": true,
	"召回质量":  true,
}

var mainlineCJKDomainSubtermSet = map[string]bool{
	"继承":  true,
	"约束":  true,
	"重新":  true,
	"引入":  true,
	"传播":  true,
	"确认":  true,
	"机制":  true,
	"分词":  true,
	"上下文": true,
	"检索":  true,
	"召回":  true,
	"质量":  true,
}

var cjkQueryStopwords = map[string]bool{
	"的": true,
	"了": true,
	"和": true,
	"或": true,
	"与": true,
	"在": true,
	"是": true,
	"要": true,
	"不": true,
}

var (
	querySegmenterOnce sync.Once
	querySegmenter     *gse.Segmenter
	querySegmenterErr  error
)

func (gseQueryTokenizer) Terms(text string) []string {
	seg, err := contextQuerySegmenter()
	if err != nil {
		return cjkQueryTerms(text)
	}

	seen := map[string]bool{}
	out := []string{}
	add := func(term string) {
		term = normalizeCJKQueryTerm(term)
		if term == "" || !keepCJKQueryTerm(term) || seen[term] {
			return
		}
		seen[term] = true
		out = append(out, term)
	}

	// Search mode returns useful sub-words (for example 重新/引入) while
	// also preserving domain dictionary phrases such as 继承约束.
	for _, term := range seg.CutSearch(text, true) {
		term = normalizeCJKQueryTerm(term)
		if mainlineCJKDomainTermSet[term] || mainlineCJKDomainSubtermSet[term] {
			add(term)
		}
	}
	// Preserve the old exact CJK chunk behavior as a cheap substring term.
	for _, term := range cjkQueryTerms(text) {
		add(term)
	}

	sort.Strings(out)
	return out
}

func contextQuerySegmenter() (*gse.Segmenter, error) {
	querySegmenterOnce.Do(func() {
		seg, err := gse.NewEmbed("zh_s")
		if err != nil {
			querySegmenterErr = err
			return
		}
		for _, term := range mainlineCJKDomainTerms {
			if err := seg.AddTokenForce(term, 100000, "nz"); err != nil {
				querySegmenterErr = err
				return
			}
		}
		querySegmenter = &seg
	})
	return querySegmenter, querySegmenterErr
}

func normalizeCJKQueryTerm(term string) string {
	term = strings.ToLower(strings.TrimSpace(term))
	term = strings.TrimFunc(term, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	return term
}

func keepCJKQueryTerm(term string) bool {
	if term == "" || !containsCJKRune(term) || cjkQueryStopwords[term] {
		return false
	}
	if cjkRuneCount(term) == 1 {
		return false
	}
	return true
}

func cjkRuneCount(term string) int {
	count := 0
	for _, r := range term {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			count++
		}
	}
	return count
}
