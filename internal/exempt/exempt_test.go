package exempt

import (
	"reflect"
	"testing"
)

func TestCheck(t *testing.T) {
	cfg := Config{Enable: true, Trailer: "Doc-Sync"}

	tests := []struct {
		name    string
		message string
		want    []Exemption
	}{
		{
			"理由つきスキップは免除される",
			"fix: 何か\n\nDoc-Sync: skip 内部リファクタリングのみ",
			[]Exemption{{Reason: "内部リファクタリングのみ"}},
		},
		{
			"大文字小文字は無視する",
			"fix: 何か\n\ndoc-sync: SKIP 理由",
			[]Exemption{{Reason: "理由"}},
		},
		{
			"理由が無ければ免除されない",
			"fix: 何か\n\nDoc-Sync: skip",
			nil,
		},
		{
			"理由が空白のみなら免除されない",
			"fix: 何か\n\nDoc-Sync: skip   ",
			nil,
		},
		{
			"トレーラが無ければ免除されない",
			"fix: 何か\n\n本文のみ",
			nil,
		},
		{
			"別のトレーラ名には反応しない",
			"fix: 何か\n\nUnwanted-Files: skip 理由",
			nil,
		},
		{
			"本文途中の skip は効かない（最後の段落ではない）",
			"fix: 何か\n\nDoc-Sync: skip 途中に書いた理由\n\n続きの説明文",
			nil,
		},
		{
			"最後の段落なら効く",
			"fix: 何か\n\n説明\n\nDoc-Sync: skip 最後の段落の理由",
			[]Exemption{{Reason: "最後の段落の理由"}},
		},
		{
			"最後の段落に非トレーラ行が混じると効かない",
			"fix: 何か\n\nDoc-Sync: skip 理由\n通常の説明文（コロンが無い）",
			nil,
		},
		{
			"コメント行に書いた skip は効かない",
			"fix: 何か\n\n# Doc-Sync: skip コメントに書いた理由",
			nil,
		},
		{
			"scissors 行以降に書いた skip は効かない",
			"fix: 何か\n\n本文\n\n# ------------------------ >8 ------------------------\ndiff --git a/x b/x\nDoc-Sync: skip scissors 以降の理由",
			nil,
		},
		{
			"CRLF でも最後の段落を判定できる",
			"fix: 何か\r\n\r\nDoc-Sync: skip CRLF の理由\r\n",
			[]Exemption{{Reason: "CRLF の理由"}},
		},
		{
			"subject しか無いメッセージ（段落が1つ）はトレーラ無し",
			"Doc-Sync: skip 理由",
			nil,
		},
		{
			"角括弧で対象を1つ絞れる",
			"fix: 何か\n\nDoc-Sync: skip[docs/granularity.md] 内部の最適化",
			[]Exemption{{Targets: []string{"docs/granularity.md"}, Reason: "内部の最適化"}},
		},
		{
			"角括弧内の複数対象はカンマ区切りで、前後の空白を無視する",
			"fix: 何か\n\nDoc-Sync: skip[docs/a.md, docs/b.md ,docs/c.md] 複数対象",
			[]Exemption{{Targets: []string{"docs/a.md", "docs/b.md", "docs/c.md"}, Reason: "複数対象"}},
		},
		{
			"角括弧の有無が混在するトレーラは両方とも集める",
			"fix: 何か\n\nDoc-Sync: skip[docs/a.md] 理由A\nDoc-Sync: skip 全体の理由",
			[]Exemption{
				{Targets: []string{"docs/a.md"}, Reason: "理由A"},
				{Reason: "全体の理由"},
			},
		},
		{
			"skip の直後に空白を挟んだ角括弧は範囲付きとして認識せず理由の一部になる",
			"fix: 何か\n\nDoc-Sync: skip [docs/a.md] 理由",
			[]Exemption{{Reason: "[docs/a.md] 理由"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Check(cfg, tt.message)
			if err != nil {
				t.Fatalf("Check() error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Check() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCheckDisabled(t *testing.T) {
	cfg := Config{Enable: false, Trailer: "Doc-Sync"}
	got, err := Check(cfg, "Doc-Sync: skip 理由")
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if got != nil {
		t.Errorf("Enable: false のときは免除されないはず, got %#v", got)
	}
}

func TestCheckEmptyTrailer(t *testing.T) {
	cfg := Config{Enable: true, Trailer: ""}
	if _, err := Check(cfg, "fix: 何か\n\nDoc-Sync: skip 理由"); err == nil {
		t.Fatal("trailer が空なら error になるはず")
	}
}

func TestCheckScopedEmptyTargetList(t *testing.T) {
	cfg := Config{Enable: true, Trailer: "Doc-Sync"}
	if _, err := Check(cfg, "fix: 何か\n\nDoc-Sync: skip[ , ] 理由"); err == nil {
		t.Fatal("角括弧内が空（カンマだけ）なら error になるはず")
	}
}
