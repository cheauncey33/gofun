package service

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeUploadName(t *testing.T) {
	t.Parallel()
	ok, err := sanitizeUploadName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png")
	if err != nil || ok != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png" {
		t.Fatalf("valid name: %q %v", ok, err)
	}
	if _, err := sanitizeUploadName("../secret.png"); err == nil {
		t.Fatal("expected traversal reject")
	}
	if _, err := sanitizeUploadName("not-hex.png"); err == nil {
		t.Fatal("expected non-hex reject")
	}
}

func TestSaveImageAcceptsPNG(t *testing.T) {
	store, err := NewUploadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	got, err := store.SaveImage(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("SaveImage: %v", err)
	}
	if !strings.HasPrefix(got.URL, uploadURLPrefix) || !strings.HasSuffix(got.Filename, ".png") {
		t.Fatalf("unexpected upload: %#v", got)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, got.Filename)); err != nil {
		t.Fatalf("file missing: %v", err)
	}
}

func TestSaveImageRejectsOversize(t *testing.T) {
	store, err := NewUploadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveImage(bytes.NewReader([]byte("x")), maxUploadBytes+1); err == nil {
		t.Fatal("expected oversize reject")
	}
}
