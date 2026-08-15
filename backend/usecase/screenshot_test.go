package usecase

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// fakeShotFS: a set of "existing" paths → bytes (reuses the serve surface) plus a scripted WalkShots.
type fakeShotFS struct {
	files map[string][]byte
	shots []domain.ShotFile
	mtime time.Time
}

func (f fakeShotFS) Exists(path string) bool { _, ok := f.files[path]; return ok }

func (f fakeShotFS) Open(path string) (io.ReadSeekCloser, time.Time, error) {
	b, ok := f.files[path]
	if !ok {
		return nil, time.Time{}, io.EOF
	}
	return nopSeekCloser{bytes.NewReader(b)}, f.mtime, nil
}

func (f fakeShotFS) WalkShots(string) []domain.ShotFile { return f.shots }

func TestScreenshotService_List(t *testing.T) {
	t.Run("newest-first, stable ties, drops escapes", func(t *testing.T) {
		fs := fakeShotFS{shots: []domain.ShotFile{
			{Path: "/repo/test-results/old.png", MTimeMs: 100},
			{Path: "/repo/screenshots/new.png", MTimeMs: 300},
			{Path: "/repo/playwright-report/mid-a.png", MTimeMs: 200}, // tie with mid-b
			{Path: "/repo/playwright-report/mid-b.png", MTimeMs: 200},
			{Path: "/outside/escape.png", MTimeMs: 999}, // relativizes to ../outside → dropped
		}}
		got := NewScreenshotService(fs).List("/repo")
		want := []string{"screenshots/new.png", "playwright-report/mid-a.png", "playwright-report/mid-b.png", "test-results/old.png"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("empty → non-nil empty slice (JSON {\"shots\":[]})", func(t *testing.T) {
		got := NewScreenshotService(fakeShotFS{}).List("/repo")
		if got == nil || len(got) != 0 {
			t.Fatalf("got %#v, want non-nil empty", got)
		}
	})
}

func TestScreenshotService_Resolve(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01}
	fs := fakeShotFS{
		files: map[string][]byte{"/repo/test-results/shot.png": png},
		mtime: time.Unix(1700000000, 0),
	}
	svc := NewScreenshotService(fs)

	t.Run("happy png byte-identical", func(t *testing.T) {
		r := svc.Resolve("/repo", "test-results/shot.png")
		if r.Status != 200 {
			t.Fatalf("status = %d, want 200", r.Status)
		}
		defer r.Content.Close()
		got, _ := io.ReadAll(r.Content)
		if !bytes.Equal(got, png) {
			t.Fatalf("png bytes not identical: %v", got)
		}
	})

	t.Run("non-png rel → 422, no content", func(t *testing.T) {
		r := svc.Resolve("/repo", "test-results/shot.jpg")
		if r.Status != 422 || r.Content != nil {
			t.Fatalf("status = %d content=%v, want 422/nil", r.Status, r.Content)
		}
	})

	t.Run("well-formed missing → 404", func(t *testing.T) {
		r := svc.Resolve("/repo", "test-results/missing.png")
		if r.Status != 404 || r.Content != nil {
			t.Fatalf("status = %d, want 404", r.Status)
		}
	})
}
