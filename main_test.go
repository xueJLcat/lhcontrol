package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingLogFileKeepsCurrentAndSingleBoundedBackup(t *testing.T) {
	const limit = int64(32)
	path := filepath.Join(t.TempDir(), "lhcontrol.log")
	writer, err := openRotatingLogFile(path, limit)
	if err != nil {
		t.Fatalf("openRotatingLogFile() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	for _, value := range [][]byte{
		bytes.Repeat([]byte("A"), 20),
		bytes.Repeat([]byte("B"), 20),
		bytes.Repeat([]byte("C"), 20),
	} {
		if written, err := writer.Write(value); err != nil || written != len(value) {
			t.Fatalf("Write() = %d, %v; want %d, nil", written, err, len(value))
		}
	}
	if err := writer.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read current log: %v", err)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read backup log: %v", err)
	}
	if int64(len(current)) > limit || int64(len(backup)) > limit {
		t.Fatalf("log sizes exceed limit: current=%d backup=%d limit=%d", len(current), len(backup), limit)
	}
	if !bytes.Equal(current, bytes.Repeat([]byte("C"), 20)) ||
		!bytes.Equal(backup, bytes.Repeat([]byte("B"), 20)) {
		t.Fatalf("unexpected rotation contents: current=%q backup=%q", current, backup)
	}
}

func TestRotatingLogFileRejectsInvalidLimit(t *testing.T) {
	if _, err := openRotatingLogFile(filepath.Join(t.TempDir(), "log"), 0); err == nil {
		t.Fatal("openRotatingLogFile() unexpectedly accepted a zero limit")
	}
}

func TestRotatingLogFileBoundsOversizedExistingLog(t *testing.T) {
	const limit = int64(16)
	path := filepath.Join(t.TempDir(), "lhcontrol.log")
	original := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatalf("write existing log: %v", err)
	}
	writer, err := openRotatingLogFile(path, limit)
	if err != nil {
		t.Fatalf("openRotatingLogFile() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read bounded backup: %v", err)
	}
	if int64(len(backup)) != limit || !bytes.Equal(backup, original[len(original)-int(limit):]) {
		t.Fatalf("bounded backup = %q (%d bytes)", backup, len(backup))
	}
}

func TestRotatingLogFileReopensCurrentFileAfterRotationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lhcontrol.log")
	writer, err := openRotatingLogFile(path, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Write([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(writer.backupPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(writer.backupPath, "blocked"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("7890")); err == nil {
		t.Fatal("rotation unexpectedly succeeded")
	}
	writer.maxSize = 32
	if _, err := writer.Write([]byte("ok")); err != nil {
		t.Fatalf("writer remained unusable after rotation failure: %v", err)
	}
}
