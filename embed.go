// Package spotter はモジュールルートに置く唯一のパッケージで、バイナリに
// 埋め込むアセット（docs/, skills/）だけを公開する。
//
// //go:embed は ".." を辿れないため、internal/skills から docs/ を埋め込むには
// モジュールルートに embed 宣言を置く必要がある。このパッケージはそれ以外の
// ロジックを持たない（ロジックは internal/skills 側に置く）。
package spotter

import "embed"

// DocsFS は docs/ ディレクトリ全体（spotter-docs スキルの references/ に
// 合成される）。
//
//go:embed docs
var DocsFS embed.FS

// SkillsFS は skills/ ディレクトリ全体（同梱スキルのソース）。
//
//go:embed skills
var SkillsFS embed.FS
