package wechat

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
)

func decryptWechatpayResource(apiV3Key string, associatedData string, nonce string, ciphertext string) ([]byte, error) {
	key := []byte(apiV3Key)
	if len(key) != 32 {
		return nil, errors.New("APIv3Key 长度必须为 32 字节")
	}
	ct, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, []byte(nonce), ct, []byte(associatedData))
	if err != nil {
		return nil, err
	}
	return plain, nil
}
