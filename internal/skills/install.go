package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// これらの metadata キーは spotter が設置したスキルの所有権とバージョンを
// 追跡するために SKILL.md の frontmatter へ埋め込む。ユーザー自身のスキルや
// 他ツールが設置したものと区別するためのマーカーであり、Agent Skills 標準の
// metadata（任意の string→string マップ）の範囲内なので、他エージェントの
// 実装からは単なる不明キーとして無害に無視される。
const (
	metaManagedBy    = "managed-by"
	metaManagedValue = "spotter"
	metaVersion      = "spotter-version"
	metaContentHash  = "spotter-content-hash"
)

// Outcome は Install が実際に何をしたか。
type Outcome string

const (
	OutcomeCreated Outcome = "created" // ディレクトリを新規作成した
	OutcomeUpdated Outcome = "updated" // 既存の spotter 管理ディレクトリを上書きした
	OutcomeAlready Outcome = "already" // 既に最新の内容が設置されていた
)

// InstallResult は 1 スキルに対する Install の結果。
type InstallResult struct {
	Name    string
	Outcome Outcome
	Dir     string
}

// UninstallResult は 1 スキルに対する Uninstall の結果。
type UninstallResult struct {
	Name    string
	Removed bool // false は「そもそも設置されていなかった」（エラーではない）
	Dir     string
}

// StatusEntry は 1 スキルの設置状況（spotter doctor / spotter skills status 向け）。
type StatusEntry struct {
	Name      string
	Dir       string
	Installed bool
	// Managed は、設置済みのものが spotter 管理下（metadata.managed-by=spotter）
	// かどうか。Installed=false のときは常に false。
	Managed          bool
	InstalledVersion string // Managed のときだけ意味を持つ
	CurrentVersion   string
	UpToDate         bool // Managed かつ InstalledVersion == CurrentVersion
}

// Installer はバイナリに同梱されたスキルを実際のディレクトリへ設置・削除・
// 状態確認する。
type Installer struct {
	Catalog Catalog
	// Version は metadata.spotter-version に埋め込むビルドバージョン
	// （internal/cli の buildVersion を渡す想定）。
	Version string
}

// NewInstaller は catalog と version から Installer を作る。
func NewInstaller(catalog Catalog, version string) Installer {
	return Installer{Catalog: catalog, Version: version}
}

// resolveNames は names が空なら Catalog.List() の全スキル名（ソート済み）を返す。
func (in Installer) resolveNames(names []string) ([]string, error) {
	if len(names) > 0 {
		sorted := append([]string(nil), names...)
		sort.Strings(sorted)
		return sorted, nil
	}
	metas, err := in.Catalog.List()
	if err != nil {
		return nil, err
	}
	all := make([]string, 0, len(metas))
	for _, m := range metas {
		all = append(all, m.Name)
	}
	return all, nil
}

// Install は names（空なら全同梱スキル）を dir 配下（dir/<name>/...）へ設置する。
// 既に dir/<name> が存在する場合:
//   - metadata.managed-by が "spotter" でない（＝ spotter が作ったのではない）
//     ディレクトリは force=false だとエラーにする
//   - metadata.managed-by が "spotter" なら、内容のハッシュとバイナリの
//     バージョンの両方を比較し、既に最新なら OutcomeAlready、そうでなければ
//     丸ごと置き換えて OutcomeUpdated にする（内容のハッシュだけでは、
//     docs/ の中身は同じだが同梱スキル一覧など無関係な変更でビルドバージョンが
//     上がっただけのケースを区別できないため、version も比較する。
//     spotter 管理下のディレクトリはローカル改変の有無を区別せず常に
//     最新化する。ユーザーが手を入れたい場合は本文側ではなく
//     .claude/skills 等の外に自分のスキルとして置くべき、という前提）
//
// 1 つでも失敗すると、そこまでの結果と共にエラーを返す（途中で打ち切る）。
func (in Installer) Install(dir string, names []string, force bool) ([]InstallResult, error) {
	names, err := in.resolveNames(names)
	if err != nil {
		return nil, err
	}

	var results []InstallResult
	for _, name := range names {
		r, err := in.installOne(dir, name, force)
		if err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, nil
}

func (in Installer) installOne(dir, name string, force bool) (InstallResult, error) {
	files, err := in.Catalog.Compose(name)
	if err != nil {
		return InstallResult{}, err
	}
	skillMD, ok := files["SKILL.md"]
	if !ok {
		return InstallResult{}, fmt.Errorf("skills: %s: SKILL.md がありません", name)
	}

	contentHash := hashTree(files)
	skillDir := filepath.Join(dir, name)

	existing, existingErr := readManagedMeta(skillDir)
	dirExists := existingErr == nil

	if dirExists {
		if !existing.managed && !force {
			return InstallResult{}, fmt.Errorf("skills: %s: %s は spotter が設置したものではありません（--force で上書きできます）", name, skillDir)
		}
		if existing.managed && existing.contentHash == contentHash && existing.version == in.Version && !force {
			return InstallResult{Name: name, Outcome: OutcomeAlready, Dir: skillDir}, nil
		}
	}

	injected, err := injectMeta(skillMD, in.Version, contentHash)
	if err != nil {
		return InstallResult{}, fmt.Errorf("skills: %s: %w", name, err)
	}
	files["SKILL.md"] = injected

	if err := writeTree(skillDir, files); err != nil {
		return InstallResult{}, fmt.Errorf("skills: %s: %w", name, err)
	}

	outcome := OutcomeCreated
	if dirExists {
		outcome = OutcomeUpdated
	}
	return InstallResult{Name: name, Outcome: outcome, Dir: skillDir}, nil
}

// Uninstall は names（空なら dir 直下にある全ディレクトリ）を dir から削除する。
// spotter 管理下でないディレクトリは force=false だとエラーにする。dir 自体や
// 個々の対象ディレクトリが存在しない場合はエラーにせず Removed=false を返す。
func (in Installer) Uninstall(dir string, names []string, force bool) ([]UninstallResult, error) {
	if len(names) == 0 {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("skills: %s の一覧取得に失敗しました: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
	}

	var results []UninstallResult
	for _, name := range names {
		skillDir := filepath.Join(dir, name)
		meta, err := readManagedMeta(skillDir)
		if err != nil {
			results = append(results, UninstallResult{Name: name, Removed: false, Dir: skillDir})
			continue
		}
		if !meta.managed && !force {
			return results, fmt.Errorf("skills: %s: %s は spotter が設置したものではありません（--force で削除できます）", name, skillDir)
		}
		if err := os.RemoveAll(skillDir); err != nil {
			return results, fmt.Errorf("skills: %s: %s の削除に失敗しました: %w", name, skillDir, err)
		}
		results = append(results, UninstallResult{Name: name, Removed: true, Dir: skillDir})
	}
	return results, nil
}

// Status は同梱スキル全ての、dir 配下での設置状況を返す。
func (in Installer) Status(dir string) ([]StatusEntry, error) {
	metas, err := in.Catalog.List()
	if err != nil {
		return nil, err
	}

	entries := make([]StatusEntry, 0, len(metas))
	for _, m := range metas {
		skillDir := filepath.Join(dir, m.Name)
		meta, err := readManagedMeta(skillDir)
		if err != nil {
			entries = append(entries, StatusEntry{Name: m.Name, Dir: skillDir, CurrentVersion: in.Version})
			continue
		}
		entries = append(entries, StatusEntry{
			Name:             m.Name,
			Dir:              skillDir,
			Installed:        true,
			Managed:          meta.managed,
			InstalledVersion: meta.version,
			CurrentVersion:   in.Version,
			UpToDate:         meta.managed && meta.version == in.Version,
		})
	}
	return entries, nil
}

// managedMeta は既存ディレクトリの SKILL.md から読み取った所有権情報。
type managedMeta struct {
	managed     bool
	version     string
	contentHash string
}

// readManagedMeta は dir/SKILL.md を読んで managedMeta を返す。ディレクトリや
// SKILL.md が存在しない、または frontmatter が壊れている場合はエラーを返す
// （呼び出し側は「未設置」または「spotter 管理外」として扱う）。
func readManagedMeta(dir string) (managedMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return managedMeta{}, err
	}
	fm, _, err := ParseFrontmatter(data)
	if err != nil {
		// frontmatter が読めない = spotter が書いたものではない、として扱う
		// （エラーにはせず managed=false を返す。呼び出し側の force 判定に委ねる）。
		return managedMeta{managed: false}, nil
	}
	if fm.Metadata[metaManagedBy] != metaManagedValue {
		return managedMeta{managed: false}, nil
	}
	return managedMeta{
		managed:     true,
		version:     fm.Metadata[metaVersion],
		contentHash: fm.Metadata[metaContentHash],
	}, nil
}

// injectMeta は skillMD の frontmatter に所有権メタデータを埋め込み直す。
// 埋め込み FS 由来の元データ（フィールド順・空行等）は yaml.Marshal による
// 再構築で失われるが、SKILL.md はツール生成物として扱う前提なので許容する
// （人間が保持したい情報は本文側、または元のソース skills/<name>/SKILL.md に
// 置くべきもので、設置先のコピーは常に上書きされる）。
func injectMeta(skillMD []byte, version, contentHash string) ([]byte, error) {
	fm, body, err := ParseFrontmatter(skillMD)
	if err != nil {
		return nil, err
	}

	meta := make(map[string]string, len(fm.Metadata)+3)
	for k, v := range fm.Metadata {
		meta[k] = v
	}
	meta[metaManagedBy] = metaManagedValue
	meta[metaVersion] = version
	meta[metaContentHash] = contentHash
	fm.Metadata = meta

	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("skills: frontmatter の再構築に失敗しました: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(frontmatterDelim)
	buf.WriteString("\n")
	buf.Write(yamlBytes)
	buf.WriteString(frontmatterDelim)
	buf.WriteString("\n")
	buf.WriteString(body)
	return buf.Bytes(), nil
}

// hashTree は files（相対パス→中身）の決定的なハッシュを返す。files の
// イテレーション順（Go の map はランダム）に依存しないよう、パスをソートして
// から連結する。SKILL.md はメタデータ注入前の生の中身で計算する必要がある
// （注入後の内容でハッシュを取ると、ハッシュ自身がハッシュ値に依存する
// 循環になる）ため、呼び出し側は Compose() 直後・injectMeta 呼び出し前の
// files に対してこれを呼ぶこと。
func hashTree(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(files[p])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeTree は dir を丸ごと置き換えて files（相対パス→中身）を書き出す。
// 既存の dir を os.RemoveAll してから書くため、旧バージョンにあって新
// バージョンに無いファイルが残らない（spotter 管理下のディレクトリは
// spotter が完全に所有する前提）。
func writeTree(dir string, files map[string][]byte) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("%s の削除に失敗しました: %w", dir, err)
	}
	for rel, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("%s のディレクトリ作成に失敗しました: %w", full, err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return fmt.Errorf("%s の書き込みに失敗しました: %w", full, err)
		}
	}
	return nil
}
