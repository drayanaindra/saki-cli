package usecase

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

// fakeProtoFS: a set of "existing" paths → bytes; Open errors for a path in openErr.
type fakeProtoFS struct {
	files   map[string][]byte
	openErr map[string]bool
	mtime   time.Time
}

func (f fakeProtoFS) Exists(path string) bool {
	if _, ok := f.files[path]; ok {
		return true
	}
	return f.openErr[path] // an Exists-true-then-Open-fails path
}

func (f fakeProtoFS) Open(path string) (io.ReadSeekCloser, time.Time, error) {
	if f.openErr[path] {
		return nil, time.Time{}, errors.New("boom")
	}
	b, ok := f.files[path]
	if !ok {
		return nil, time.Time{}, errors.New("not found")
	}
	return nopSeekCloser{bytes.NewReader(b)}, f.mtime, nil
}

func TestProtoAssetService_Resolve(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01}
	fs := fakeProtoFS{
		files:   map[string][]byte{"/repo/tasks/proto-foo/preview.html": []byte("<html>hi</html>"), "/repo/tasks/proto-foo/shot.png": png},
		openErr: map[string]bool{"/repo/tasks/proto-foo/vanish.png": true},
		mtime:   time.Unix(1700000000, 0),
	}
	svc := NewProtoAssetService(fs)

	t.Run("happy html", func(t *testing.T) {
		r := svc.Resolve("/repo", "tasks/proto-foo/preview.html")
		if r.Status != 200 {
			t.Fatalf("status = %d, want 200", r.Status)
		}
		defer r.Content.Close()
		got, _ := io.ReadAll(r.Content)
		if string(got) != "<html>hi</html>" {
			t.Fatalf("bytes = %q", got)
		}
		if r.Path != "/repo/tasks/proto-foo/preview.html" {
			t.Fatalf("path = %q", r.Path)
		}
	})

	t.Run("happy png byte-identical", func(t *testing.T) {
		r := svc.Resolve("/repo", "tasks/proto-foo/shot.png")
		if r.Status != 200 {
			t.Fatalf("status = %d, want 200", r.Status)
		}
		defer r.Content.Close()
		got, _ := io.ReadAll(r.Content)
		if !bytes.Equal(got, png) {
			t.Fatalf("png bytes not identical: %v", got)
		}
	})

	t.Run("regex-fail rel → 422, no content", func(t *testing.T) {
		r := svc.Resolve("/repo", "tasks/other/file.html")
		if r.Status != 422 || r.Content != nil {
			t.Fatalf("status = %d content=%v, want 422/nil", r.Status, r.Content)
		}
	})

	t.Run("well-formed missing → 404", func(t *testing.T) {
		r := svc.Resolve("/repo", "tasks/proto-foo/missing.html")
		if r.Status != 404 || r.Content != nil {
			t.Fatalf("status = %d, want 404", r.Status)
		}
	})

	t.Run("exists-then-open-fails → 404 not 500", func(t *testing.T) {
		r := svc.Resolve("/repo", "tasks/proto-foo/vanish.png")
		if r.Status != 404 {
			t.Fatalf("status = %d, want 404", r.Status)
		}
	})
}
