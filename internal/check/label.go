package check

// DeletedLabel は Violation.Files に出す削除ファイルの表示名。変更されたファイルと
// 見分けが付くよう「（削除）」を付ける。commitintent / docsync など、削除ファイルを
// 違反候補に含める検査が共通で使う。
func DeletedLabel(path string) string {
	return path + "（削除）"
}
