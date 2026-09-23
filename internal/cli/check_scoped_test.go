package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check"
	"github.com/fuchigta/spotter/internal/exempt"
)

// fakeRunner は check.Runner の最小実装（Run は呼ばれない前提のテストでも安全に使える
// よう nil, nil を返す）。
type fakeRunner struct{}

func (fakeRunner) Granularity() check.Granularity               { return check.GranularitySquashed }
func (fakeRunner) Run(check.Context) ([]check.Violation, error) { return nil, nil }

// fakeScopedRunner は check.ScopedExemptable も実装する fakeRunner。
type fakeScopedRunner struct {
	fakeRunner
	targets []string
}

func (f fakeScopedRunner) ExemptTargets() []string { return f.targets }

func TestCollectExemptions(t *testing.T) {
	cfg := exempt.Config{Enable: true, Trailer: "Doc-Sync"}

	// squashed: 1コミット目の本文途中の skip は効かず、2コミット目の末尾段落の skip だけが
	// 拾われることを確認する（squashed で「範囲内のどれか1コミット」に書けば良い挙動）。
	messages := []string{
		"feat: 2nd\n\nDoc-Sync: skip[docs/b.md] 2コミット目の理由",
		"feat: 1st\n\nDoc-Sync: skip 本文途中の理由\n\n続きの説明文",
	}

	got, err := collectExemptions(cfg, messages)
	if err != nil {
		t.Fatalf("collectExemptions() error: %v", err)
	}
	want := []exempt.Exemption{{Targets: []string{"docs/b.md"}, Reason: "2コミット目の理由"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collectExemptions() = %#v, want %#v", got, want)
	}
}

func TestCollectExemptionsPropagatesError(t *testing.T) {
	cfg := exempt.Config{Enable: true, Trailer: ""}
	if _, err := collectExemptions(cfg, []string{"fix: 何か"}); err == nil {
		t.Fatal("trailer が空なら error を伝播するはず")
	}
}

func TestWholeExemptionReasons(t *testing.T) {
	exemptions := []exempt.Exemption{
		{Targets: []string{"docs/a.md"}, Reason: "範囲付き"},
		{Reason: "全体その1"},
		{Reason: "全体その2"},
	}
	got := wholeExemptionReasons(exemptions)
	want := []string{"全体その1", "全体その2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wholeExemptionReasons() = %v, want %v", got, want)
	}
}

func TestScopedExemptionsFrom(t *testing.T) {
	exemptions := []exempt.Exemption{
		{Targets: []string{"docs/a.md", "docs/b.md"}, Reason: "理由"},
		{Reason: "全体免除は対象なし"},
	}
	got := scopedExemptionsFrom(exemptions)
	want := []scopedExemption{
		{target: "docs/a.md", reason: "理由"},
		{target: "docs/b.md", reason: "理由"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scopedExemptionsFrom() = %#v, want %#v", got, want)
	}
}

func TestApplyScopedExemptionsRemovesMatchingTargetOnly(t *testing.T) {
	runner := fakeScopedRunner{targets: []string{"docs/a.md", "docs/b.md"}}
	scoped := []scopedExemption{{target: "docs/a.md", reason: "内部の最適化"}}
	violations := []check.Violation{
		{Summary: "a違反", Target: "docs/a.md"},
		{Summary: "b違反", Target: "docs/b.md"},
	}

	var buf bytes.Buffer
	remaining, err := applyScopedExemptions(&buf, "doc-sync", runner, scoped, violations)
	if err != nil {
		t.Fatalf("applyScopedExemptions() error: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Target != "docs/b.md" {
		t.Errorf("docs/a.md 宛ての違反だけが免除されて docs/b.md は残るはず, got %#v", remaining)
	}
	if !strings.Contains(buf.String(), "docs/a.md を免除しました（内部の最適化）") {
		t.Errorf("免除した旨の表示が出るはず, got %q", buf.String())
	}
}

func TestApplyScopedExemptionsUnrelatedViolationKept(t *testing.T) {
	// Target が空の violation は範囲付き免除では消えない。
	runner := fakeScopedRunner{targets: []string{"docs/a.md"}}
	scoped := []scopedExemption{{target: "docs/a.md", reason: "理由"}}
	violations := []check.Violation{{Summary: "対象なしの違反"}}

	var buf bytes.Buffer
	remaining, err := applyScopedExemptions(&buf, "doc-sync", runner, scoped, violations)
	if err != nil {
		t.Fatalf("applyScopedExemptions() error: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("Target が空の violation は免除されず残るはず, got %#v", remaining)
	}
}

func TestApplyScopedExemptionsErrorsWhenRunnerNotScoped(t *testing.T) {
	runner := fakeRunner{}
	scoped := []scopedExemption{{target: "docs/a.md", reason: "理由"}}

	if _, err := applyScopedExemptions(&bytes.Buffer{}, "unwanted-files", runner, scoped, nil); err == nil {
		t.Fatal("ScopedExemptable を実装していない検査に範囲付き免除を書いたら error になるはず")
	}
}

func TestApplyScopedExemptionsErrorsOnUnknownTarget(t *testing.T) {
	runner := fakeScopedRunner{targets: []string{"docs/a.md"}}
	scoped := []scopedExemption{{target: "docs/typo.md", reason: "理由"}}

	if _, err := applyScopedExemptions(&bytes.Buffer{}, "doc-sync", runner, scoped, nil); err == nil {
		t.Fatal("有効な対象一覧に無い対象を指定したら error になるはず")
	}
}

func TestApplyScopedExemptionsNoMatchingViolationIsNotError(t *testing.T) {
	// 対象は有効だが、その対象に違反が無い場合はエラーにしない
	// （全体免除で違反が無いのと同じ扱い）。
	runner := fakeScopedRunner{targets: []string{"docs/a.md"}}
	scoped := []scopedExemption{{target: "docs/a.md", reason: "理由"}}

	remaining, err := applyScopedExemptions(&bytes.Buffer{}, "doc-sync", runner, scoped, nil)
	if err != nil {
		t.Fatalf("対象に違反が無いだけなら error にならないはず: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("remaining = %#v, want 空", remaining)
	}
}
