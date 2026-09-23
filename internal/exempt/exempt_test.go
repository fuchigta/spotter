package exempt

import "testing"

func TestCheck(t *testing.T) {
	cfg := Config{Enable: true, Trailer: "Doc-Sync"}

	tests := []struct {
		name       string
		message    string
		wantSkip   bool
		wantReason string
	}{
		{"理由つきスキップは免除される", "fix: 何か\n\nDoc-Sync: skip 内部リファクタリングのみ", true, "内部リファクタリングのみ"},
		{"大文字小文字は無視する", "fix: 何か\n\ndoc-sync: SKIP 理由", true, "理由"},
		{"理由が無ければ免除されない", "fix: 何か\n\nDoc-Sync: skip", false, ""},
		{"理由が空白のみなら免除されない", "fix: 何か\n\nDoc-Sync: skip   ", false, ""},
		{"トレーラが無ければ免除されない", "fix: 何か\n\n本文のみ", false, ""},
		{"別のトレーラ名には反応しない", "fix: 何か\n\nUnwanted-Files: skip 理由", false, ""},
		{"本文途中の skip は効かない（最後の段落ではない）", "fix: 何か\n\nDoc-Sync: skip 途中に書いた理由\n\n続きの説明文", false, ""},
		{"最後の段落なら効く", "fix: 何か\n\n説明\n\nDoc-Sync: skip 最後の段落の理由", true, "最後の段落の理由"},
		{"最後の段落に非トレーラ行が混じると効かない", "fix: 何か\n\nDoc-Sync: skip 理由\n通常の説明文（コロンが無い）", false, ""},
		{"コメント行に書いた skip は効かない", "fix: 何か\n\n# Doc-Sync: skip コメントに書いた理由", false, ""},
		{
			"scissors 行以降に書いた skip は効かない",
			"fix: 何か\n\n本文\n\n# ------------------------ >8 ------------------------\ndiff --git a/x b/x\nDoc-Sync: skip scissors 以降の理由",
			false, "",
		},
		{"CRLF でも最後の段落を判定できる", "fix: 何か\r\n\r\nDoc-Sync: skip CRLF の理由\r\n", true, "CRLF の理由"},
		{"subject しか無いメッセージ（段落が1つ）はトレーラ無し", "Doc-Sync: skip 理由", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skip, reason, err := Check(cfg, tt.message)
			if err != nil {
				t.Fatalf("Check() error: %v", err)
			}
			if skip != tt.wantSkip || reason != tt.wantReason {
				t.Errorf("Check() = (%v, %q), want (%v, %q)", skip, reason, tt.wantSkip, tt.wantReason)
			}
		})
	}
}

func TestCheckDisabled(t *testing.T) {
	cfg := Config{Enable: false, Trailer: "Doc-Sync"}
	skip, _, err := Check(cfg, "Doc-Sync: skip 理由")
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if skip {
		t.Errorf("Enable: false のときは免除されないはず")
	}
}

func TestCheckEmptyTrailer(t *testing.T) {
	cfg := Config{Enable: true, Trailer: ""}
	if _, _, err := Check(cfg, "fix: 何か\n\nDoc-Sync: skip 理由"); err == nil {
		t.Fatal("trailer が空なら error になるはず")
	}
}
