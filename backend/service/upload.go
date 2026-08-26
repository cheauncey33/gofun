package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxUploadBytes  = 2 << 20
	uploadURLPrefix = "/api/v1/uploads/"
)

var uploadTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

type UploadStore struct {
	Dir string
}

func NewUploadStore(dir string) (*UploadStore, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "uploads"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建上传目录失败: %w", err)
	}
	return &UploadStore{Dir: dir}, nil
}

type UploadedImage struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
}

func (s *UploadStore) SaveImage(src io.Reader, size int64) (*UploadedImage, error) {
	if size <= 0 || size > maxUploadBytes {
		return nil, fmt.Errorf("%w: 图片需小于 2MB", ErrInvalidTicketCatalog)
	}
	limited := io.LimitReader(src, maxUploadBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxUploadBytes {
		return nil, fmt.Errorf("%w: 图片需小于 2MB", ErrInvalidTicketCatalog)
	}
	ext, ok := uploadTypes[http.DetectContentType(payload)]
	if !ok {
		return nil, fmt.Errorf("%w: 仅支持 JPG / PNG / WebP / GIF", ErrInvalidTicketCatalog)
	}
	name, err := randomUploadName(ext)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.Dir, name)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return nil, err
	}
	return &UploadedImage{URL: uploadURLPrefix + name, Filename: name}, nil
}

func (s *UploadStore) Open(name string) (*os.File, string, error) {
	safe, err := sanitizeUploadName(name)
	if err != nil {
		return nil, "", err
	}
	file, err := os.Open(filepath.Join(s.Dir, safe))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", ErrTicketResourceNotFound
		}
		return nil, "", err
	}
	return file, safe, nil
}

func randomUploadName(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ext, nil
}

func sanitizeUploadName(name string) (string, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) {
		return "", ErrTicketResourceNotFound
	}
	ext := strings.ToLower(filepath.Ext(name))
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if len(stem) != 32 {
		return "", ErrTicketResourceNotFound
	}
	for _, r := range stem {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", ErrTicketResourceNotFound
		}
	}
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return stem + ext, nil
	default:
		return "", ErrTicketResourceNotFound
	}
}
