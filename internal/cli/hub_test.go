package cli

import "testing"

// isLikelyHighRisk is a deliberately conservative subject scanner.
// These tests fence both directions: the keyword set is wide enough
// to catch the obvious risk surfaces, but does not fire on routine
// commits like docs / tests / formatting.

func TestIsLikelyHighRisk_EnglishKeywords(t *testing.T) {
	cases := []struct {
		subj string
		want bool
	}{
		{"fix(auth): rotate JWT secret", true},
		{"chore: add migration for users table", true},
		{"hotfix: rollback payment flow", true},
		{"feat: production-ready logging", true},
		{"docs: tweak README typo", false},
		{"test: cover edge case in parser", false},
	}
	for _, c := range cases {
		got := isLikelyHighRisk(c.subj)
		if got != c.want {
			t.Errorf("%q: want %v, got %v", c.subj, c.want, got)
		}
	}
}

func TestIsLikelyHighRisk_ChineseKeywords(t *testing.T) {
	cases := []struct {
		subj string
		want bool
	}{
		{"fix: 修复登录鉴权问题", true},
		{"feat: 数据库 schema 迁移", true},
		{"refactor: 计费模块重构", true},
		{"hotfix: 紧急回滚生产环境配置", true},
		{"chore: 删除已废弃接口", true},
		{"docs: 更新 README 中的安装步骤", false},
		{"test: 增加边界用例覆盖", false},
		{"polish: 优化样式排版", false},
	}
	for _, c := range cases {
		got := isLikelyHighRisk(c.subj)
		if got != c.want {
			t.Errorf("%q: want %v, got %v", c.subj, c.want, got)
		}
	}
}
