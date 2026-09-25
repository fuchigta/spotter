package configdiff_test

import (
	"testing"

	"github.com/fuchigta/spotter/internal/configdiff"
)

// monotonicConfigs is a fixed set of valid .spotter.yml contents used to check that Diff
// behaves as a preorder over "no loosening between a and b": Diff(a,a) must always be
// empty, and Diff(a,b)=∅ together with Diff(b,c)=∅ must imply Diff(a,c)=∅. That is the
// property a range check relies on: if every commit in a range passes against its parent
// with no loosening, the whole range (compared start to end) must also pass with no
// loosening (issue 37, section 6). The set is fixed and holds no randomness so the test
// stays deterministic.
var monotonicConfigs = []string{
	// 設定が無い。
	"",

	// diff-size の上限を少しずつ緩めていく（省略は無限大として扱われる）。
	"checks: {diff-size: {type: diff-size, max_lines: 100, max_files: 5}}\n",
	"checks: {diff-size: {type: diff-size, max_lines: 300, max_files: 5}}\n",
	"checks: {diff-size: {type: diff-size, max_lines: 300, max_files: 20}}\n",
	"checks: {diff-size: {type: diff-size, max_files: 20}}\n",
	"checks: {diff-size: {type: diff-size}}\n",

	// diff-size に doc-sync を足し、pairs を削り、最後に doc-sync 自体を削る。
	"checks: {diff-size: {type: diff-size, max_lines: 300, max_files: 20}, doc-sync: {type: doc-sync, pairs: [{paths: 'internal/**/*.go', doc: docs/foo.md}]}}\n",
	"checks: {diff-size: {type: diff-size, max_lines: 300, max_files: 20}, doc-sync: {type: doc-sync, pairs: [{paths: 'internal/**/*.go', doc: docs/foo.md}], exclude: ['**/*_test.go']}}\n",
	"checks: {diff-size: {type: diff-size, max_lines: 300, max_files: 20}, doc-sync: {type: doc-sync, pairs: [], exclude: ['**/*_test.go']}}\n",
	"checks: {diff-size: {type: diff-size, max_lines: 300, max_files: 20}}\n",

	// doc-links の check_anchors と doc-paths の path_prefixes を同時に緩める。
	"checks: {doc-links: {type: doc-links, check_anchors: true}, doc-paths: {type: doc-paths, path_prefixes: [internal, cmd]}}\n",
	"checks: {doc-links: {type: doc-links}, doc-paths: {type: doc-paths, path_prefixes: [internal]}}\n",

	// commit-subject の allowed_types を緩める。
	"checks: {commit-subject: {type: commit-subject, allowed_types: [feat, fix]}}\n",
	"checks: {commit-subject: {type: commit-subject, allowed_types: [feat, fix, docs]}}\n",

	// 検査キーの改名（同じ設定のまま a → b）。
	"checks: {a: {type: doc-sync, exclude: [x]}}\n",
	"checks: {b: {type: doc-sync, exclude: [x]}}\n",

	// required_version を上げ下げする。
	"required_version: v1.0.0\n",
	"required_version: v2.0.0\n",

	// exempt.enable を無効から既定（有効）に戻す。
	"checks: {a: {type: doc-sync, exempt: {enable: false}}}\n",
	"checks: {a: {type: doc-sync}}\n",
}

func TestDiffIsReflexive(t *testing.T) {
	for i, a := range monotonicConfigs {
		if ls := configdiff.Diff([]byte(a), []byte(a)); len(ls) != 0 {
			t.Errorf("monotonicConfigs[%d]: Diff(a, a) が空ではありません: %+v", i, ls)
		}
	}
}

func TestDiffIsTransitive(t *testing.T) {
	n := len(monotonicConfigs)
	clean := make([][]bool, n)
	for i, a := range monotonicConfigs {
		clean[i] = make([]bool, n)
		for j, b := range monotonicConfigs {
			clean[i][j] = len(configdiff.Diff([]byte(a), []byte(b))) == 0
		}
	}

	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if !clean[i][j] {
				continue
			}
			for k := 0; k < n; k++ {
				if clean[j][k] && !clean[i][k] {
					t.Errorf(
						"Diff(configs[%d], configs[%d])=∅ かつ Diff(configs[%d], configs[%d])=∅ なのに Diff(configs[%d], configs[%d])≠∅ です",
						i, j, j, k, i, k,
					)
				}
			}
		}
	}
}
