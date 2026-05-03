package engine

import (
	"sort"
	"strings"
	"unicode"
)

type queryTerms struct {
	EffectiveKeywords []string
	DroppedTerms      []ContextDroppedTerm
	ExpandedTerms     map[string][]string
}

var queryShortTokenAllowlist = map[string]bool{
	"ack": true,
	"api": true,
	"cjk": true,
	"cli": true,
	"d1":  true,
	"jwt": true,
	"kv":  true,
	"pbt": true,
	"s3":  true,
	"ui":  true,
}

var queryAliasExpansions = map[string][]string{
	"ack": {"acknowledge", "acknowledged", "acknowledgement", "acknowledgment"},
}

func queryTermsFromText(text string) queryTerms {
	terms := queryTerms{
		EffectiveKeywords: []string{},
		DroppedTerms:      []ContextDroppedTerm{},
		ExpandedTerms:     map[string][]string{},
	}
	if text == "" {
		return terms
	}

	seenEffective := map[string]bool{}
	seenDropped := map[string]bool{}
	addDropped := func(term, reason string) {
		key := term + "\x00" + reason
		if seenDropped[key] {
			return
		}
		seenDropped[key] = true
		terms.DroppedTerms = append(terms.DroppedTerms, ContextDroppedTerm{Term: term, Reason: reason})
	}
	addEffective := func(term string) {
		if seenEffective[term] {
			addDropped(term, "duplicate")
			return
		}
		seenEffective[term] = true
		terms.EffectiveKeywords = append(terms.EffectiveKeywords, term)
	}

	for _, f := range asciiQueryFields(text) {
		reason := ""
		switch {
		case len(f) < 4 && !queryShortTokenAllowlist[f]:
			reason = "too_short"
		case stopwords[f]:
			reason = "stopword"
		}
		if reason != "" {
			addDropped(f, reason)
			continue
		}
		addEffective(f)
	}

	for _, term := range defaultQueryTokenizer.Terms(text) {
		addEffective(term)
	}
	for _, term := range unsupportedNonASCIIQueryTerms(text) {
		addDropped(term, "unsupported_non_ascii")
	}

	sort.Strings(terms.EffectiveKeywords)
	sort.Slice(terms.DroppedTerms, func(i, j int) bool {
		if terms.DroppedTerms[i].Term == terms.DroppedTerms[j].Term {
			return terms.DroppedTerms[i].Reason < terms.DroppedTerms[j].Reason
		}
		return terms.DroppedTerms[i].Term < terms.DroppedTerms[j].Term
	})

	for _, kw := range terms.EffectiveKeywords {
		aliases, ok := queryAliasExpansions[kw]
		if !ok {
			continue
		}
		cp := append([]string(nil), aliases...)
		sort.Strings(cp)
		terms.ExpandedTerms[kw] = cp
	}
	return terms
}

func (qt queryTerms) scoringKeywords() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(qt.EffectiveKeywords))
	for _, kw := range qt.EffectiveKeywords {
		if kw == "" || seen[kw] {
			continue
		}
		seen[kw] = true
		out = append(out, kw)
	}
	for _, aliases := range qt.ExpandedTerms {
		for _, kw := range aliases {
			if kw == "" || seen[kw] {
				continue
			}
			seen[kw] = true
			out = append(out, kw)
		}
	}
	sort.Strings(out)
	return out
}

func asciiQueryFields(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "_-")
		if f == "" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func cjkQueryTerms(text string) []string {
	return nonASCIIQueryTerms(text, containsCJKRune)
}

func unsupportedNonASCIIQueryTerms(text string) []string {
	return nonASCIIQueryTerms(text, func(s string) bool {
		return !containsCJKRune(s)
	})
}

func nonASCIIQueryTerms(text string, keep func(string) bool) []string {
	fields := strings.Fields(strings.ToLower(text))
	seen := map[string]bool{}
	out := []string{}
	for _, f := range fields {
		f = strings.TrimFunc(f, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
		})
		if f == "" || !containsNonASCII(f) || !keep(f) || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func containsNonASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return false
}

func containsCJKRune(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			return true
		}
	}
	return false
}
