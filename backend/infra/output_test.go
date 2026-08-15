package infra

import (
	"os"
	"testing"
)

func TestFileOutput_ReadFrom(t *testing.T) {
	j := NewFileJournal(t.TempDir())
	o := NewFileOutput(j)

	// a not-yet-created .out returns no bytes + the same offset, no error (keep tailing).
	data, off, err := o.ReadFrom("r1", 0)
	if err != nil || len(data) != 0 || off != 0 {
		t.Fatalf("missing .out: data=%q off=%d err=%v", data, off, err)
	}

	if err := os.WriteFile(j.OutPath("r1"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, off, err = o.ReadFrom("r1", 0)
	if err != nil || string(data) != "hello" || off != 5 {
		t.Fatalf("read all: data=%q off=%d err=%v", data, off, err)
	}

	// append + read ONLY the new bytes from the saved offset.
	if err := os.WriteFile(j.OutPath("r1"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, off, err = o.ReadFrom("r1", 5)
	if err != nil || string(data) != " world" || off != 11 {
		t.Fatalf("incremental: data=%q off=%d err=%v", data, off, err)
	}
}
