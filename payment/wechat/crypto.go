package wechat

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
)

// aesGCMDecrypt AES-256-GCM 解密
// key: APIv3密钥(32字节)
// nonce: 随机串(12字节hex编码)
// ciphertext: 密文
// associatedData: 附加数据
func aesGCMDecrypt(key []byte, nonceHex string, ciphertext []byte, associatedData string) ([]byte, error) {
	// 确保密钥长度为32字节(AES-256)
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid key length: %d, expected 32", len(key))
	}

	// nonce是hex编码的12字节
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		// 如果不是hex编码，直接使用原始字符串
		nonce = []byte(nonceHex)
	}
	if len(nonce) != 12 {
		// 如果长度不对，截取或填充
		if len(nonce) > 12 {
			nonce = nonce[:12]
		} else {
			padded := make([]byte, 12)
			copy(padded, nonce)
			nonce = padded
		}
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher failed: %w", err)
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM failed: %w", err)
	}

	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, []byte(associatedData))
	if err != nil {
		return nil, fmt.Errorf("decrypt failed: %w", err)
	}

	return plaintext, nil
}
