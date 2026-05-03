package engine

import "testing"

func TestQueryTermsFromTextSegmentsUnspacedCJK(t *testing.T) {
	terms := queryTermsFromText("不要重新引入继承约束")

	for _, want := range []string{"重新", "引入", "继承", "约束"} {
		assertQueryEffectiveKeyword(t, terms, want)
	}
}

func TestQueryTermsFromTextKeepsDomainCJKTerms(t *testing.T) {
	terms := queryTermsFromText("继承约束确认机制")

	for _, want := range []string{"继承约束", "确认机制"} {
		assertQueryEffectiveKeyword(t, terms, want)
	}
}

func TestQueryTermsFromTextDropsGenericCJKSubtermsInLongNonsense(t *testing.T) {
	terms := queryTermsFromText("完全不存在的中文主题测试词")

	for _, generic := range []string{"中文", "主题", "测试", "完全", "存在"} {
		assertQueryMissingEffectiveKeyword(t, terms, generic)
	}
	assertQueryEffectiveKeyword(t, terms, "完全不存在的中文主题测试词")
}

func assertQueryMissingEffectiveKeyword(t *testing.T, terms queryTerms, keyword string) {
	t.Helper()
	for _, got := range terms.EffectiveKeywords {
		if got == keyword {
			t.Fatalf("keyword %q should not be effective, got %+v", keyword, terms.EffectiveKeywords)
		}
	}
}

func assertQueryEffectiveKeyword(t *testing.T, terms queryTerms, keyword string) {
	t.Helper()
	for _, got := range terms.EffectiveKeywords {
		if got == keyword {
			return
		}
	}
	t.Fatalf("expected effective keyword %q, got %+v", keyword, terms.EffectiveKeywords)
}
