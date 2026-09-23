package check_test

import (
	"testing"

	"github.com/fuchigta/spotter/internal/check"
)

func TestDeletedLabel(t *testing.T) {
	got := check.DeletedLabel("internal/foo.go")
	want := "internal/foo.go（削除）"
	if got != want {
		t.Errorf("DeletedLabel() = %q, want %q", got, want)
	}
}
