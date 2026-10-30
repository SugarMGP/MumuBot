package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
)

const currentKeyVersion = 1

var (
	mu     sync.RWMutex
	aead   cipher.AEAD
	loaded bool
)

// Configure 从 base64 编码的 32 字节密钥初始化 AES-256-GCM
func Configure(encoded string) error {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		mu.Lock()
		aead = nil
		loaded = false
		mu.Unlock()
		return fmt.Errorf("MUMU_SECRET_KEY 未配置")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(encoded)
	}
	if err != nil {
		return fmt.Errorf("MUMU_SECRET_KEY 不是合法 base64")
	}
	if len(key) != 32 {
		return fmt.Errorf("MUMU_SECRET_KEY 必须是 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("初始化加密算法失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("初始化 GCM 失败: %w", err)
	}
	mu.Lock()
	aead = gcm
	loaded = true
	mu.Unlock()
	return nil
}

// Configured 返回是否已经配置可用密钥
func Configured() bool {
	mu.RLock()
	defer mu.RUnlock()
	return loaded && aead != nil
}

// Encrypt 加密明文并返回密文、nonce 和密钥版本
func Encrypt(plain []byte) ([]byte, []byte, int, error) {
	mu.RLock()
	gcm := aead
	mu.RUnlock()
	if gcm == nil {
		return nil, nil, 0, fmt.Errorf("MUMU_SECRET_KEY 未配置")
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, 0, fmt.Errorf("生成加密随机数失败: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plain, nil)
	return ciphertext, nonce, currentKeyVersion, nil
}

// Decrypt 解密指定版本的密文
func Decrypt(ciphertext, nonce []byte, version int) ([]byte, error) {
	if version != currentKeyVersion {
		return nil, fmt.Errorf("不支持的加密密钥版本 %d", version)
	}
	mu.RLock()
	gcm := aead
	mu.RUnlock()
	if gcm == nil {
		return nil, fmt.Errorf("MUMU_SECRET_KEY 未配置")
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("加密 nonce 长度无效")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("解密失败: %w", err)
	}
	return plain, nil
}

// EncryptString 加密字符串
func EncryptString(plain string) ([]byte, []byte, int, error) {
	return Encrypt([]byte(plain))
}

// DecryptString 解密字符串
func DecryptString(ciphertext, nonce []byte, version int) (string, error) {
	plain, err := Decrypt(ciphertext, nonce, version)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
