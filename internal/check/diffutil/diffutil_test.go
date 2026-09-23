package diffutil_test

import (
	"strings"
	"testing"

	"github.com/fuchigta/spotter/internal/check/diffutil"
)

func TestParseLinesAddedAndRemoved(t *testing.T) {
	diff := "diff --git a/foo.go b/foo.go\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -10 +9,0 @@\n" +
		"-func TestFoo(t *testing.T) {\n" +
		"@@ -41,0 +42 @@\n" +
		"+// @ts-ignore\n"

	added, removed := diffutil.ParseLines(diff)

	if len(removed) != 1 || removed[0].Num != 10 || removed[0].Text != "func TestFoo(t *testing.T) {" {
		t.Errorf("removed = %+v", removed)
	}
	if len(added) != 1 || added[0].Num != 42 || added[0].Text != "// @ts-ignore" {
		t.Errorf("added = %+v", added)
	}
}

func TestParseLinesFileHeaderIgnored(t *testing.T) {
	// "+++"/"--- " はファイルヘッダ行であり、追加/削除行にも行番号のカウントにも
	// 含まれないこと。
	diff := "diff --git a/foo.go b/foo.go\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,0 +1 @@\n" +
		"+x\n"

	added, _ := diffutil.ParseLines(diff)
	if len(added) != 1 || added[0].Num != 1 || added[0].Text != "x" {
		t.Errorf("added = %+v", added)
	}
}

func TestParseLinesMultipleHunks(t *testing.T) {
	diff := "diff --git a/foo.go b/foo.go\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,0 +1 @@\n" +
		"+a\n" +
		"@@ -5,0 +6 @@\n" +
		"+b\n"

	added, _ := diffutil.ParseLines(diff)
	if len(added) != 2 || added[0].Num != 1 || added[1].Num != 6 {
		t.Errorf("added = %+v", added)
	}
}

func TestParseLinesMalformedHunkHeaderResetsToZero(t *testing.T) {
	// ハンクヘッダの形式が崩れている（数値を読み取れない）場合、直前のハンクの
	// 行番号を引きずらず、そのハンクの行番号は 0 として扱われる。
	diff := "diff --git a/foo.go b/foo.go\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -10,0 +11 @@\n" +
		"+a\n" +
		"@@ garbled hunk header @@\n" +
		"+b\n" +
		"-c\n"

	added, removed := diffutil.ParseLines(diff)
	if len(added) != 2 || added[0].Num != 11 {
		t.Fatalf("added = %+v", added)
	}
	if added[1].Num != 0 || added[1].Text != "b" {
		t.Errorf("崩れたハンクヘッダの後の行は Num=0 のはず, got %+v", added[1])
	}
	if len(removed) != 1 || removed[0].Num != 0 || removed[0].Text != "c" {
		t.Errorf("崩れたハンクヘッダの後の行は Num=0 のはず, got %+v", removed)
	}
}

func TestParseLinesBinaryDiffHasNoAddedOrRemoved(t *testing.T) {
	diff := "diff --git a/img.png b/img.png\n" +
		"Binary files a/img.png and b/img.png differ\n"

	added, removed := diffutil.ParseLines(diff)
	if added != nil || removed != nil {
		t.Errorf("バイナリ差分は追加/削除行を持たないはず, added=%v removed=%v", added, removed)
	}
}

func TestTruncateShorterThanMax(t *testing.T) {
	got := diffutil.Truncate("hello", 10)
	if got != "hello" {
		t.Errorf("Truncate() = %q, want 変化なし", got)
	}
}

func TestTruncateExactlyMax(t *testing.T) {
	s := strings.Repeat("a", 10)
	got := diffutil.Truncate(s, 10)
	if got != s {
		t.Errorf("ちょうど max のときは切り詰めないはず, got %q", got)
	}
}

func TestTruncateASCIIOverMax(t *testing.T) {
	s := strings.Repeat("a", 125)
	got := diffutil.Truncate(s, 120)
	want := strings.Repeat("a", 120) + "..."
	if got != want {
		t.Errorf("Truncate() = %q, want %q", got, want)
	}
}

func TestTruncateMultiByteDoesNotCorruptRunes(t *testing.T) {
	// 130 文字の日本語（1 文字 3 バイト）を 120 文字に切り詰める。バイト単位で
	// 切ると text[:120] がマルチバイト文字の途中で切れて不正な UTF-8 列になる
	// バグがあったため、rune 単位で切ることを確認する。
	s := strings.Repeat("日", 130)
	got := diffutil.Truncate(s, 120)
	want := strings.Repeat("日", 120) + "..."
	if got != want {
		t.Errorf("Truncate() は rune 単位で切り詰められていないはず, got len(rune)=%d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("切り詰めたら \"...\" が付くはず, got %q", got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("不正な UTF-8 列（置換文字）が含まれている: %q", got)
		}
	}
}

func TestFormatHit(t *testing.T) {
	got := diffutil.FormatHit("src/a.go", 42, "// @ts-ignore")
	want := "src/a.go:42: // @ts-ignore"
	if got != want {
		t.Errorf("FormatHit() = %q, want %q", got, want)
	}
}

func TestFormatHitTruncatesLongLine(t *testing.T) {
	text := strings.Repeat("a", 200)
	got := diffutil.FormatHit("src/a.go", 1, text)
	want := "src/a.go:1: " + strings.Repeat("a", 120) + "..."
	if got != want {
		t.Errorf("FormatHit() は 120 rune で切り詰めるはず, got len=%d", len(got))
	}
}
