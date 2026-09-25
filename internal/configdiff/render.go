package configdiff

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// kindDescriptions は Loosening.String() が括弧内に添える日本語の説明。
var kindDescriptions = map[Kind]string{
	KindCheckRemoved:   "検査が削除されました",
	KindTypeChanged:    "type を変更しました",
	KindExemptEnabled:  "免除を有効にしました",
	KindLimitRaised:    "上限を上げました",
	KindRuleRemoved:    "ルールが削除されました",
	KindAllowAdded:     "許可を追加しました",
	KindTargetRemoved:  "対象を削除しました",
	KindTurnedOff:      "無効にしました",
	KindVersionLowered: "required_version を下げました",
	KindChanged:        "変更しました",
	KindUnparsable:     "YAML として解析できません",
}

// String は Loosening を利用者向けの 1 行にする
// （例: "checks.diff-size.max_lines: 1500 → 5000（上限を上げました）"）。
func (l Loosening) String() string {
	desc := kindDescriptions[l.Kind]

	if l.Kind == KindUnparsable {
		return fmt.Sprintf("%s: %s（%s）", l.Path, desc, l.After)
	}
	switch {
	case l.Before == "" && l.After == "":
		return fmt.Sprintf("%s（%s）", l.Path, desc)
	case l.Before == "":
		return fmt.Sprintf("%s: → %s（%s）", l.Path, l.After, desc)
	case l.After == "":
		return fmt.Sprintf("%s: %s →（%s）", l.Path, l.Before, desc)
	default:
		return fmt.Sprintf("%s: %s → %s（%s）", l.Path, l.Before, l.After, desc)
	}
}

// render は値を利用者向けに 1 行で表示する。nil は空文字列（省略を表す）。HTML
// エスケープをせず encoding/json で表示するのは、`&&` のようなシェル的な値をそのまま
// 読めるようにするため（このパッケージの出力は HTML には埋め込まない）。
func render(v any) string {
	if v == nil {
		return ""
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}
