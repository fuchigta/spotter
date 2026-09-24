package prepush_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/prepush"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []prepush.Update
		wantErr bool
	}{
		{
			name:  "1 行",
			input: "refs/heads/main aaa refs/heads/main bbb\n",
			want: []prepush.Update{
				{LocalRef: "refs/heads/main", LocalSHA: "aaa", RemoteRef: "refs/heads/main", RemoteSHA: "bbb"},
			},
		},
		{
			name:  "複数行と空行",
			input: "refs/heads/a aaa refs/heads/a bbb\n\nrefs/heads/c ccc refs/heads/c ddd\n",
			want: []prepush.Update{
				{LocalRef: "refs/heads/a", LocalSHA: "aaa", RemoteRef: "refs/heads/a", RemoteSHA: "bbb"},
				{LocalRef: "refs/heads/c", LocalSHA: "ccc", RemoteRef: "refs/heads/c", RemoteSHA: "ddd"},
			},
		},
		{
			name:  "CRLF を吸収する",
			input: "refs/heads/main aaa refs/heads/main bbb\r\n",
			want: []prepush.Update{
				{LocalRef: "refs/heads/main", LocalSHA: "aaa", RemoteRef: "refs/heads/main", RemoteSHA: "bbb"},
			},
		},
		{
			name:  "入力が空なら空スライス",
			input: "",
			want:  nil,
		},
		{
			name:    "フィールド数が合わない行は error",
			input:   "refs/heads/main aaa refs/heads/main\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := prepush.Parse(strings.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("エラーになるはずが nil でした")
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Parse() = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Parse()[%d] = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// fakeDeps は prepush.Deps を固定値・固定マップで組み立てる。resolve は ref -> sha の
// peel 結果、exists はローカルに実在する sha の集合。
func fakeDeps(resolve map[string]string, exists map[string]bool, head string) prepush.Deps {
	return prepush.Deps{
		ResolveCommit: func(ref string) (string, bool, error) {
			sha, ok := resolve[ref]
			return sha, ok, nil
		},
		CommitExists: func(sha string) (bool, error) {
			return exists[sha], nil
		},
		Head: func() (string, error) {
			return head, nil
		},
	}
}

func TestPlanRef(t *testing.T) {
	const headSHA = "head1"

	tests := []struct {
		name        string
		update      prepush.Update
		resolve     map[string]string
		exists      map[string]bool
		wantChecked bool
		wantReason  string
		wantRange   string
	}{
		{
			name:        "削除 push は検査しない",
			update:      prepush.Update{LocalRef: "refs/heads/feature", LocalSHA: "0000000000000000000000000000000000000000", RemoteRef: "refs/heads/feature", RemoteSHA: "aaa"},
			wantChecked: false,
			wantReason:  "refs/heads/feature は削除 push のため検査しません",
		},
		{
			name:        "commit に peel できない sha は検査しない",
			update:      prepush.Update{LocalRef: "refs/tags/v1", LocalSHA: "tagsha", RemoteRef: "refs/tags/v1", RemoteSHA: "0000000000000000000000000000000000000000"},
			resolve:     map[string]string{}, // peel できない = マップに無い
			wantChecked: false,
			wantReason:  "refs/tags/v1 はコミットを指していないため検査しません",
		},
		{
			name:        "peel した local が HEAD と異なるなら検査しない",
			update:      prepush.Update{LocalRef: "refs/heads/old-branch", LocalSHA: "oldsha", RemoteRef: "refs/heads/old-branch", RemoteSHA: "0000000000000000000000000000000000000000"},
			resolve:     map[string]string{"oldsha": "notHead"},
			wantChecked: false,
			wantReason:  "refs/heads/old-branch は HEAD ではないため検査しません（CI で検査されます）",
		},
		{
			name:        "新規ブランチ（remote sha が全 0）は remote sha を付けない",
			update:      prepush.Update{LocalRef: "refs/heads/main", LocalSHA: "localsha", RemoteRef: "refs/heads/main", RemoteSHA: "0000000000000000000000000000000000000000"},
			resolve:     map[string]string{"localsha": headSHA},
			wantChecked: true,
			wantRange:   headSHA + " --not --remotes",
		},
		{
			name:        "remote sha がローカルに実在すれば範囲式に含める",
			update:      prepush.Update{LocalRef: "refs/heads/main", LocalSHA: "localsha", RemoteRef: "refs/heads/main", RemoteSHA: "remotesha"},
			resolve:     map[string]string{"localsha": headSHA},
			exists:      map[string]bool{"remotesha": true},
			wantChecked: true,
			wantRange:   headSHA + " --not --remotes remotesha",
		},
		{
			name:        "remote sha がローカルに無ければ付けない（force push 先が未知の場合など）",
			update:      prepush.Update{LocalRef: "refs/heads/main", LocalSHA: "localsha", RemoteRef: "refs/heads/main", RemoteSHA: "remotesha"},
			resolve:     map[string]string{"localsha": headSHA},
			exists:      map[string]bool{"remotesha": false},
			wantChecked: true,
			wantRange:   headSHA + " --not --remotes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := fakeDeps(tt.resolve, tt.exists, headSHA)
			got, err := prepush.PlanRef(tt.update, deps)
			if err != nil {
				t.Fatalf("PlanRef() error: %v", err)
			}
			if got.Checked != tt.wantChecked {
				t.Fatalf("Checked = %v, want %v", got.Checked, tt.wantChecked)
			}
			if !tt.wantChecked {
				if got.SkipReason != tt.wantReason {
					t.Errorf("SkipReason = %q, want %q", got.SkipReason, tt.wantReason)
				}
				return
			}
			if got.RangeExpr != tt.wantRange {
				t.Errorf("RangeExpr = %q, want %q", got.RangeExpr, tt.wantRange)
			}
		})
	}
}

func TestPlanRefResolveCommitError(t *testing.T) {
	wantErr := errors.New("boom")
	deps := prepush.Deps{
		ResolveCommit: func(ref string) (string, bool, error) { return "", false, wantErr },
		CommitExists:  func(sha string) (bool, error) { return false, nil },
		Head:          func() (string, error) { return "head", nil },
	}

	_, err := prepush.PlanRef(prepush.Update{LocalRef: "refs/heads/main", LocalSHA: "sha", RemoteSHA: "0000000000000000000000000000000000000000"}, deps)
	if err == nil {
		t.Fatal("ResolveCommit の失敗はそのまま error になるはず")
	}
}

func TestPlanAllAndUniqueRangeExprs(t *testing.T) {
	const headSHA = "head1"

	updates := []prepush.Update{
		{LocalRef: "refs/heads/a", LocalSHA: "shaA", RemoteRef: "refs/heads/a", RemoteSHA: "0000000000000000000000000000000000000000"},
		{LocalRef: "refs/heads/b", LocalSHA: "shaA", RemoteRef: "refs/heads/b", RemoteSHA: "0000000000000000000000000000000000000000"}, // a と同じ commit を指す（squashed push）
		{LocalRef: "refs/heads/deleted", LocalSHA: "0000000000000000000000000000000000000000", RemoteRef: "refs/heads/deleted", RemoteSHA: "shaC"},
	}
	deps := fakeDeps(map[string]string{"shaA": headSHA}, nil, headSHA)

	plans, err := prepush.PlanAll(updates, deps)
	if err != nil {
		t.Fatalf("PlanAll() error: %v", err)
	}
	if len(plans) != len(updates) {
		t.Fatalf("PlanAll() は updates と 1 対 1 のはず, got %d want %d", len(plans), len(updates))
	}
	if !plans[0].Checked || !plans[1].Checked || plans[2].Checked {
		t.Fatalf("Checked の組み合わせが想定外です: %+v", plans)
	}

	exprs := prepush.UniqueRangeExprs(plans)
	if len(exprs) != 1 {
		t.Fatalf("同じ範囲式は重複除去されるはず, got %v", exprs)
	}
	if exprs[0] != headSHA+" --not --remotes" {
		t.Errorf("UniqueRangeExprs()[0] = %q", exprs[0])
	}
}
