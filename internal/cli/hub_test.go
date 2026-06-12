package cli

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestReadExternalContributionsFileAcceptsWrappedJSONAndForcesTrustBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "external.json")
	body := `{
  "external_contributions": [
    {
      "title": "feat(sources): add Pi agent session support",
      "author_login": "jiangge",
      "repository": "catoncat/cxs",
      "pr_number": 56,
      "merged_commit": "8006baae417d3ac3c8fe646ad77f67527480a17f",
      "provenance": "github_pr_imported",
      "body_intent_note": "empty_template",
      "author_sealed": true,
      "verified": true
    }
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	contribs, err := readExternalContributionsFile(path)
	if err != nil {
		t.Fatalf("read external contributions: %v", err)
	}
	if len(contribs) != 1 {
		t.Fatalf("expected one contribution, got %+v", contribs)
	}
	got := contribs[0]
	if got.AuthorLogin != "jiangge" || got.Provenance != "github_pr_imported" || got.BodyIntentNote != "empty_template" {
		t.Fatalf("github provenance fields not preserved: %+v", got)
	}
	if got.AuthorSealed || !got.NotAuthorSealed || got.Verified {
		t.Fatalf("import must force not-author-sealed/unverified trust boundary, got %+v", got)
	}
}
