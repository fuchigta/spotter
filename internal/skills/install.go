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

type InstallResult struct {
	Name    string
	Outcome Outcome
	Dir     string
}

type UninstallResult struct {
	Name string
	Dir  string
	// Removed は実際に削除したかどうか（dryRun=true のときは常に false）。
	// false は必ずしも失敗ではない（「そもそも設置されていなかった」場合も
	// false になる）。区別したい場合は WouldRemove を見る。
	Removed bool
	// WouldRemove は「検証を通過し、削除の対象になった（dryRun=false なら
	// 実際に削除された）」ことを表す。dryRun=true で計画だけを知りたいときに使う。
	WouldRemove bool
}

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

func NewInstaller(catalog Catalog, version string) Installer {
	return Installer{Catalog: catalog, Version: version}
}

// resolveNames は names が空なら Catalog.List() の全スキル名（ソート済み）を返す。
// 空でなければソートと重複除去だけを行う（"--only a,a" のような入力で同じ
// スキルを2回処理しないため）。
func (in Installer) resolveNames(names []string) ([]string, error) {
	if len(names) > 0 {
		return dedupSorted(names), nil
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
// 既存の dir/<name> が spotter 管理下でなければ force=false はエラーにする。
// 管理下なら内容ハッシュとバージョンの両方が一致するときだけ何もしない
// （ハッシュだけでは、無関係な変更でビルドバージョンだけ上がったケースを
// 区別できないため）。それ以外は常に丸ごと置き換える（ローカル改変は保持しない）。
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
		// 既に最新なら force の有無に関わらず何もしない。force は「spotter 管理外を
		// 上書きしてよい」の意味であって「最新でも強制的に書き直す」の意味ではない。
		if existing.managed && existing.contentHash == contentHash && existing.version == in.Version {
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
//
// 削除前に全対象を検証してから削除する 2 パス方式を取る（1 パス目で途中の
// 名前が force 無しで拒否されても、それより前の対象は削除されていない状態を
// 保証するため。検証自体は SKILL.md を読むだけで安価なので実現できる）。
func (in Installer) Uninstall(dir string, names []string, force, dryRun bool) ([]UninstallResult, error) {
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
	} else {
		names = dedupSorted(names)
	}

	type planned struct {
		name     string
		skillDir string
		exists   bool
	}
	plan := make([]planned, 0, len(names))

	for _, name := range names {
		// name は --only 由来でユーザー入力そのままになりうる。nameRe を通さずに
		// filepath.Join(dir, name) すると、".." を含む name で dir の外の
		// ディレクトリを RemoveAll してしまう（パストラバーサル）。
		if !nameRe.MatchString(name) {
			return nil, fmt.Errorf("skills: %q は不正なスキル名です（[a-z0-9-]、64文字以内）", name)
		}

		skillDir := filepath.Join(dir, name)
		meta, err := readManagedMeta(skillDir)
		if err != nil {
			plan = append(plan, planned{name: name, skillDir: skillDir, exists: false})
			continue
		}
		if !meta.managed && !force {
			return nil, fmt.Errorf("skills: %s: %s は spotter が設置したものではありません（--force で削除できます）", name, skillDir)
		}
		plan = append(plan, planned{name: name, skillDir: skillDir, exists: true})
	}

	results := make([]UninstallResult, 0, len(plan))
	for _, p := range plan {
		if !p.exists {
			results = append(results, UninstallResult{Name: p.name, Dir: p.skillDir})
			continue
		}
		if dryRun {
			results = append(results, UninstallResult{Name: p.name, Dir: p.skillDir, WouldRemove: true})
			continue
		}
		if err := os.RemoveAll(p.skillDir); err != nil {
			return results, fmt.Errorf("skills: %s: %s の削除に失敗しました: %w", p.name, p.skillDir, err)
		}
		results = append(results, UninstallResult{Name: p.name, Dir: p.skillDir, Removed: true, WouldRemove: true})
	}
	return results, nil
}

// dedupSorted はソート済みの重複除去済みスライスを返す。
func dedupSorted(names []string) []string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	out := sorted[:0]
	for i, n := range sorted {
		if i == 0 || sorted[i-1] != n {
			out = append(out, n)
		}
	}
	return out
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

// readManagedMeta は dir/SKILL.md を読んで managedMeta を返す。
//
//   - dir/SKILL.md が存在しない（＝未設置）場合はエラーを返す。呼び出し側は
//     これを「未設置」として扱う
//   - dir/SKILL.md は存在するが frontmatter が壊れている、または
//     metadata.managed-by が "spotter" でない場合はエラーにせず
//     managed=false を返す。呼び出し側はこれを「spotter 管理外」として扱い、
//     上書き・削除には force を要求する
func readManagedMeta(dir string) (managedMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return managedMeta{}, fmt.Errorf("skills: %s の読み込みに失敗しました: %w", dir, err)
	}
	fm, _, err := ParseFrontmatter(data)
	if err != nil {
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
// 旧バージョンにあって新バージョンに無いファイルが残らないよう、最終的には
// dir を新しい内容だけのディレクトリに置き換える（spotter 管理下のディレクトリは
// spotter が完全に所有する前提）。
//
// 書き込みは dir の親の下に作った一時ディレクトリに対して行い、全ファイルの
// 書き込みが成功してから os.Rename で dir に差し替える。書き込み先を最初から
// dir 自体にして RemoveAll 直後に書くと、途中でエラーになった場合（ディスクフル・
// 権限・Windows でのファイルロック等）に「消えたが書き直せていない」状態が
// dir に残ってしまうため、これを避ける。
func writeTree(dir string, files map[string][]byte) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("%s のディレクトリ作成に失敗しました: %w", parent, err)
	}

	tmp, err := os.MkdirTemp(parent, ".spotter-skill-*")
	if err != nil {
		return fmt.Errorf("一時ディレクトリの作成に失敗しました: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(tmp)
		}
	}()

	for rel, data := range files {
		full := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("%s のディレクトリ作成に失敗しました: %w", full, err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return fmt.Errorf("%s の書き込みに失敗しました: %w", full, err)
		}
	}

	// os.Rename は宛先が既に存在すると（特に Windows で）失敗するため、既存の
	// dir があれば一旦退避してから差し替える。差し替えに成功すれば退避先を
	// 削除し、失敗すれば退避したものを dir に戻す（可能な範囲でのロールバック）。
	backup := ""
	if _, err := os.Stat(dir); err == nil {
		backup = dir + ".spotter-old"
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("%s の削除に失敗しました: %w", backup, err)
		}
		if err := os.Rename(dir, backup); err != nil {
			return fmt.Errorf("%s の退避に失敗しました: %w", dir, err)
		}
	}

	if err := os.Rename(tmp, dir); err != nil {
		if backup != "" {
			_ = os.Rename(backup, dir)
		}
		return fmt.Errorf("%s への置き換えに失敗しました: %w", dir, err)
	}

	succeeded = true
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}
