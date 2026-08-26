package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

func maskIDNumber(idNumber string) string {
	idNumber = strings.ToUpper(strings.TrimSpace(idNumber))
	if len(idNumber) < 8 {
		return "**************"
	}
	return idNumber[:3] + "***********" + idNumber[len(idNumber)-4:]
}

func stableIdentityKey(secret []byte, idNumber string) string {
	idNumber = strings.ToUpper(strings.TrimSpace(idNumber))
	mac := hmac.New(sha256.New, secret)
	_, _ = io.WriteString(mac, "attendee-stable-v1:")
	_, _ = io.WriteString(mac, idNumber)
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func orderAttendeeHash(secret []byte, orderID int64, idNumber string) string {
	idNumber = strings.ToUpper(strings.TrimSpace(idNumber))
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "attendee-id-v1:%d:%s", orderID, idNumber)
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func identityCipherKey(secret []byte) []byte {
	sum := sha256.Sum256(secret)
	return sum[:]
}

func encryptIdentity(secret []byte, plain string) (string, error) {
	block, err := aes.NewCipher(identityCipherKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decryptIdentity(secret []byte, blob string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return "", fmt.Errorf("证件档案损坏")
	}
	block, err := aes.NewCipher(identityCipherKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("证件档案损坏")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("证件档案损坏")
	}
	return string(plain), nil
}
