package companionfiles_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/check/companionfiles"
	"github.com/fuchigta/spotter/internal/config"
)

type fakeSource struct {
	changed []string
	deleted []string
	exists  map[string]bool

	errChanged error
	errDeleted error
	errExists  error
}

func (f fakeSource) ChangedFiles() ([]string, error)       { return f.changed, f.errChanged }
func (f fakeSource) DiffLines(path string) (string, error) { return "", nil }
func (f fakeSource) BlobSize(path string) (int64, error)   { return 0, nil }
func (f fakeSource) Stats() ([]check.FileStat, error)      { return nil, nil }
func (f fakeSource) DeletedFiles() ([]string, error)       { return f.deleted, f.errDeleted }
func (f fakeSource) Exists(path string) (bool, error)      { return f.exists[path], f.errExists }

func mustNew(t *testing.T, cc config.CheckConfig) *companionfiles.Check {
	t.Helper()
	c, err := companionfiles.New(cc)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

// TestNewValidation は New() の起動時バリデーションをまとめて確認する（不正な設定は
// 1 パターンごとに 1 分岐ではなく、ここに追加する）。
func TestNewValidation(t *testing.T) {
	tests := []struct {
		name    string
		cc      config.CheckConfig
		wantErr string
	}{
		{
			name:    "companions が 0 件",
			cc:      config.CheckConfig{},
			wantErr: "少なくとも 1 件",
		},
		{
			name: "reason が無い",
			cc: config.CheckConfig{
				Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test.ts"}}},
			},
			wantErr: "paths / companion / reason",
		},
		{
			name: "companion が 0 件",
			cc: config.CheckConfig{
				Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Reason: "テストが無い"}},
			},
			wantErr: "paths / companion / reason",
		},
		{
			name: "companion の候補に空文字",
			cc: config.CheckConfig{
				Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test.ts", ""}, Reason: "テストが無い"}},
			},
			wantErr: "companion に空文字",
		},
		{
			name: "未知のテンプレート変数",
			cc: config.CheckConfig{
				Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/{basename}.test.ts"}, Reason: "テストが無い"}},
			},
			wantErr: "未知の変数",
		},
		{
			name: "paths が不正な doublestar パターン",
			cc: config.CheckConfig{
				Companions: []config.CompanionRule{{Paths: "[", Companion: []string{"{name}.test.ts"}, Reason: "テストが無い"}},
			},
			wantErr: "パターン",
		},
		{
			name: "companion に .. セグメント",
			cc: config.CheckConfig{
				Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/../{name}.test.ts"}, Reason: "テストが無い"}},
			},
			wantErr: "セグメント",
		},
		{
			name: "companion が絶対パス",
			cc: config.CheckConfig{
				Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"/etc/{name}.test.ts"}, Reason: "テストが無い"}},
			},
			wantErr: "絶対パス",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := companionfiles.New(tt.cc)
			if err == nil {
				t.Fatal("New() はエラーになるはず")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestGranularity(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test.ts"}, Reason: "テストが無い"}},
	})
	if c.Granularity() != check.GranularitySquashed {
		t.Errorf("companion-files の granularity は squashed 固定のはず, got %v", c.Granularity())
	}
}

// TestRunSingleFileTemplateExpansion は、1 ファイルだけが変更されたときの
// companion テンプレート（{dir}/{name}/{ext}/{stem}）の展開結果をまとめて確認する。
func TestRunSingleFileTemplateExpansion(t *testing.T) {
	tests := []struct {
		name    string
		rule    config.CompanionRule
		changed string
		want    string
	}{
		{
			name:    "テンプレート変数一式",
			rule:    config.CompanionRule{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test{ext}"}, Reason: "テストが無い"},
			changed: "src/api/client.ts",
			want:    "src/api/client.ts → src/api/client.test.ts",
		},
		{
			name:    "ルート直下のファイルで先頭に / が残らない",
			rule:    config.CompanionRule{Paths: "*.ts", Companion: []string{"{dir}/{name}.test{ext}"}, Reason: "テストが無い"},
			changed: "client.ts",
			want:    "client.ts → client.test.ts",
		},
		{
			// {stem} は複合拡張子（001.up.sql）で最初の "." より前だけを取る
			// （{name}/{ext} の「最後の .」基準とは異なる）。
			name:    "stem は複合拡張子の最初の . より前",
			rule:    config.CompanionRule{Paths: "db/migrations/**/*.up.sql", Companion: []string{"{dir}/{stem}.down.sql"}, Reason: "ロールバック用のマイグレーションが無い"},
			changed: "db/migrations/001.up.sql",
			want:    "db/migrations/001.up.sql → db/migrations/001.down.sql",
		},
		{
			// 拡張子なし（ドットを含まない）ファイル名では stem がベース名全体になる。
			name:    "拡張子なしファイルの stem はベース名全体",
			rule:    config.CompanionRule{Paths: "Makefile", Companion: []string{"{dir}/{stem}.lock"}, Reason: "ロックファイルが無い"},
			changed: "Makefile",
			want:    "Makefile → Makefile.lock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mustNew(t, config.CheckConfig{Companions: []config.CompanionRule{tt.rule}})

			violations, err := c.Run(check.Context{
				Source: fakeSource{changed: []string{tt.changed}},
			})
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if len(violations) != 1 {
				t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
			}
			if got := violations[0].Files; len(got) != 1 || got[0] != tt.want {
				t.Errorf("Files = %v, want [%s]", got, tt.want)
			}
		})
	}
}

func TestRunCompanionExists(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test{ext}"}, Reason: "テストが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{
			changed: []string{"src/api/client.ts"},
			exists:  map[string]bool{"src/api/client.test.ts": true},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("相方が既にあれば違反 0 件のはず, got %v", violations)
	}
}

func TestRunExcludeSkipsRule(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{
				Paths:     "src/**/*.ts",
				Companion: []string{"{dir}/{name}.test{ext}"},
				Reason:    "テストが無い",
				Exclude:   []string{"src/types/**"},
			},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{changed: []string{"src/types/foo.d.ts"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("exclude に一致するファイルは対象外のはず, got %v", violations)
	}
}

func TestRunMultipleRules(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test{ext}"}, Reason: "テストが無い"},
			{Paths: "db/migrations/**/*.up.sql", Companion: []string{"{dir}/{name}.down.sql"}, Reason: "ロールバック用のマイグレーションが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{changed: []string{"src/api/client.ts", "db/migrations/001.up.sql"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("両方のルールが違反するはず, got %d: %v", len(violations), violations)
	}
}

// TestRunCompanionListAnyCandidateSatisfies は companion をリストで書いたとき、
// いずれか 1 つの候補が存在すれば違反にならないことを確認する。
func TestRunCompanionListAnyCandidateSatisfies(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{
				Paths:     "internal/**/*.go",
				Companion: []string{"{dir}/{name}_test.go", "{dir}/testdata/{name}"},
				Reason:    "テストが無い",
			},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{
			changed: []string{"internal/foo/bar.go"},
			exists:  map[string]bool{"internal/foo/testdata/bar": true},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("候補のどれか 1 つがあれば違反 0 件のはず, got %v", violations)
	}
}

// TestRunCompanionListAllMissingListsAllCandidates は companion をリストで書いたとき、
// どの候補も無ければ違反表示に全候補を並べることを確認する。
func TestRunCompanionListAllMissingListsAllCandidates(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{
				Paths:     "internal/**/*.go",
				Companion: []string{"{dir}/{name}_test.go", "{dir}/testdata/{name}"},
				Reason:    "テストが無い",
			},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{changed: []string{"internal/foo/bar.go"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	want := "internal/foo/bar.go → internal/foo/bar_test.go, internal/foo/testdata/bar"
	if got := violations[0].Files; len(got) != 1 || got[0] != want {
		t.Errorf("Files = %v, want [%q]", got, want)
	}
}

func TestRunNoChangedFilesIsSkipped(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test{ext}"}, Reason: "テストが無い"}},
	})
	violations, err := c.Run(check.Context{Source: fakeSource{changed: nil}})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("変更ファイルが 0 件なら違反 0 件のはず, got %v", violations)
	}
}

// TestRunOrphanDetected は本体が削除された後も相方が比較の終点に残っていれば
// 孤児として違反にすることを確認する。
func TestRunOrphanDetected(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "internal/**/*.go", Companion: []string{"{dir}/{name}_test.go"}, Reason: "テストが無い", Exclude: []string{"**/*_test.go"}},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{
			deleted: []string{"internal/foo/bar.go"},
			exists:  map[string]bool{"internal/foo/bar_test.go": true},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("孤児の違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "internal/foo/bar.go → internal/foo/bar_test.go" {
		t.Errorf("Files = %v", got)
	}
}

// TestRunOrphanNotDetectedWhenCompanionAlsoRemoved は相方も一緒に無くなっていれば
// 孤児にならないことを確認する。
func TestRunOrphanNotDetectedWhenCompanionAlsoRemoved(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "internal/**/*.go", Companion: []string{"{dir}/{name}_test.go"}, Reason: "テストが無い", Exclude: []string{"**/*_test.go"}},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{deleted: []string{"internal/foo/bar.go"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("相方も削除済みなら違反 0 件のはず, got %v", violations)
	}
}

// TestRunOrphanSkippedForSharedTemplate は {dir} だけの共有型テンプレート（複数の本体が
// 同じ相方を指しうる）が孤児検出の対象外であることを確認する。
func TestRunOrphanSkippedForSharedTemplate(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "src/components/**/*.tsx", Companion: []string{"{dir}/README.md"}, Reason: "コンポーネントの説明が無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{
			deleted: []string{"src/components/button/Button.tsx"},
			exists:  map[string]bool{"src/components/button/README.md": true},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("{dir} だけの共有型テンプレートは孤児検出の対象外のはず, got %v", violations)
	}
}

// TestRunRenameProducesBothMissingAndOrphan は foo.go → bar.go のリネームで、
// 「bar_test.go が無い（missing）」と「foo_test.go が孤児として残っている（orphan）」の
// 両方が別々の violation として出ることを確認する。
func TestRunRenameProducesBothMissingAndOrphan(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: "internal/**/*.go", Companion: []string{"{dir}/{name}_test.go"}, Reason: "テストが無い", Exclude: []string{"**/*_test.go"}},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{
			changed: []string{"internal/foo/bar.go"},
			deleted: []string{"internal/foo/foo.go"},
			exists:  map[string]bool{"internal/foo/foo_test.go": true},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("missing と orphan の 2 件になるはず, got %d: %v", len(violations), violations)
	}

	var sawMissing, sawOrphan bool
	for _, v := range violations {
		for _, f := range v.Files {
			switch f {
			case "internal/foo/bar.go → internal/foo/bar_test.go":
				sawMissing = true
			case "internal/foo/foo.go → internal/foo/foo_test.go":
				sawOrphan = true
			}
		}
	}
	if !sawMissing {
		t.Errorf("bar_test.go の missing 違反が無い: %v", violations)
	}
	if !sawOrphan {
		t.Errorf("foo_test.go の orphan 違反が無い: %v", violations)
	}
}

// TestRunOrphanNoDeletedFilesIsSkipped は削除ファイルが 0 件なら孤児検出も動かないことを
// 確認する（DeletedFiles を無駄に評価しない）。
func TestRunOrphanNoDeletedFilesIsSkipped(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "internal/**/*.go", Companion: []string{"{dir}/{name}_test.go"}, Reason: "テストが無い"}},
	})
	violations, err := c.Run(check.Context{Source: fakeSource{deleted: nil}})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if violations != nil {
		t.Errorf("削除ファイルが 0 件なら違反 0 件のはず, got %v", violations)
	}
}

func TestRunChangedFilesErrorIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test.ts"}, Reason: "テストが無い"}},
	})
	if _, err := c.Run(check.Context{Source: fakeSource{errChanged: errors.New("boom")}}); err == nil {
		t.Fatal("ChangedFiles がエラーを返したら Run() はエラーになるはず")
	}
}

func TestRunDeletedFilesErrorIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test.ts"}, Reason: "テストが無い"}},
	})
	if _, err := c.Run(check.Context{Source: fakeSource{errDeleted: errors.New("boom")}}); err == nil {
		t.Fatal("DeletedFiles がエラーを返したら Run() はエラーになるはず")
	}
}

func TestRunExistsErrorInMissingIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test.ts"}, Reason: "テストが無い"}},
	})
	src := fakeSource{changed: []string{"src/api/client.ts"}, errExists: errors.New("boom")}
	if _, err := c.Run(check.Context{Source: src}); err == nil {
		t.Fatal("相方の存在確認で Exists がエラーを返したら Run() はエラーになるはず")
	}
}

func TestRunExistsErrorInOrphansIsError(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{{Paths: "src/**/*.ts", Companion: []string{"{dir}/{name}.test.ts"}, Reason: "テストが無い"}},
	})
	src := fakeSource{deleted: []string{"src/api/client.ts"}, errExists: errors.New("boom")}
	if _, err := c.Run(check.Context{Source: src}); err == nil {
		t.Fatal("孤児検出で Exists がエラーを返したら Run() はエラーになるはず")
	}
}

// TestRunStemVariableDotLeadingFile は、ドット始まりの（隠し）ファイルの {stem} が
// 先頭のドットを除いた残りの中の最初の "." までを、先頭のドットごと含めることを確認する。
func TestRunStemVariableDotLeadingFile(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{Paths: ".env*", Companion: []string{"{dir}/{stem}.example"}, Reason: "サンプルが無い"},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{changed: []string{".env", ".env.local"}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	want := []string{
		".env → .env.example",
		".env.local → .env.example",
	}
	got := violations[0].Files
	if len(got) != len(want) {
		t.Fatalf("Files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Files[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRunOrphanUsesOnlyOneToOneCandidatesWhenMixed は、1 つのルールに 1:1 対応の候補
// （{name} を含む）と共有型の候補（{dir} だけ）が混ざっている場合、孤児検出には 1:1 対応の
// 候補だけが使われることを確認する。
func TestRunOrphanUsesOnlyOneToOneCandidatesWhenMixed(t *testing.T) {
	c := mustNew(t, config.CheckConfig{
		Companions: []config.CompanionRule{
			{
				Paths:     "src/components/**/*.tsx",
				Companion: []string{"{dir}/{name}.test.tsx", "{dir}/README.md"},
				Reason:    "コンポーネントの相方が無い",
			},
		},
	})

	violations, err := c.Run(check.Context{
		Source: fakeSource{
			deleted: []string{"src/components/button/Button.tsx"},
			exists: map[string]bool{
				"src/components/button/Button.test.tsx": true,
				"src/components/button/README.md":       true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("孤児の違反は 1 件のはず, got %d: %v", len(violations), violations)
	}
	if got := violations[0].Files; len(got) != 1 || got[0] != "src/components/button/Button.tsx → src/components/button/Button.test.tsx" {
		t.Errorf("Files = %v（1:1 対応の候補だけが孤児検出に使われ、README.md は混ざらないはず）", got)
	}
}
