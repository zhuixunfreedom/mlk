// identity.go 国服 3.5.0 身份头（APP_ID_1/2/3/4）编解码。
//
// 客户端来源：CryptManager.cs:73-95（Encrypt(key,str)：DES-ECB、PaddingMode.None、
// 手工 PKCS5 填充 b=8-len%8、输出小写 hex），UserInfo.cs:160-196
// （EncryptUserId/EncryptSessionKey/HashedUserId），Setting.cs yaml 默认值：
//
//	des_key             = "5t216ObT"   （Config/Setting 未覆盖，走默认）
//	device_token_des_key = "00e2fdaa"
//	random_hash         = "TYhGo022TyofIxfs2gRVoUuyWwv0iR2G0FgAC9ml"
//
// 头映射（JSONRequester.cs:19-27,541-577 getHeader）：
//
//	APP_ID_1 = DES-ECB-hex(DesKey, UserId)         —— 服务端解密取 userId 查会话
//	APP_ID_2 = SHA1(RandomHash + UserId)           —— 一致性校验（可选消费）
//	APP_ID_3 = DES-ECB-hex(DeviceTokenDesKey, DeviceToken) —— 推送令牌，可不消费
//	APP_ID_4 = DES-ECB-hex(DesKey, SessionKey)     —— 解密取 sk 做会话鉴权
package cn35protocol

import (
	"crypto/des"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const (
	// IdentityDesKey APP_ID_1/APP_ID_4 的 DES-ECB 密钥（Config/Setting 默认值，
	// 3.5.0 APK 内嵌 yaml 未覆盖此项）。
	IdentityDesKey = "5t216ObT"
	// DeviceTokenDesKey APP_ID_3 的 DES-ECB 密钥（默认值）。
	DeviceTokenDesKey = "00e2fdaa"
	// IdentityHashSalt APP_ID_2 的 SHA1 前缀盐（random_hash 默认值）。
	IdentityHashSalt = "TYhGo022TyofIxfs2gRVoUuyWwv0iR2G0FgAC9ml"
)

// EncryptIdentity 对应 CryptManager.Encrypt(key, str)：DES-ECB(key=ASCII key)，
// 手工 PKCS5 填充（b=8-len%8，恒填充），输出小写 hex。测试/校验工具用。
func EncryptIdentity(key, plain string) (string, error) {
	block, err := des.NewCipher([]byte(key))
	if err != nil {
		return "", fmt.Errorf("cn35protocol: DES key: %w", err)
	}
	src := []byte(plain)
	pad := des.BlockSize - len(src)%des.BlockSize
	padded := make([]byte, len(src)+pad)
	copy(padded, src)
	for i := len(src); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += des.BlockSize {
		block.Encrypt(out[i:i+des.BlockSize], padded[i:i+des.BlockSize])
	}
	return hex.EncodeToString(out), nil
}

// DecryptIdentity 服务端解 APP_ID_1/APP_ID_4：小写 hex → DES-ECB 解密 →
// 去 PKCS5 填充 → 明文字符串。返回错误时调用方按身份缺失处理，不进业务写。
func DecryptIdentity(key, hexValue string) (string, error) {
	cipherText, err := hex.DecodeString(hexValue)
	if err != nil {
		return "", fmt.Errorf("cn35protocol: 身份头 hex 解码失败: %w", err)
	}
	if len(cipherText) == 0 || len(cipherText)%des.BlockSize != 0 {
		return "", fmt.Errorf("%w: 身份头密文长度 %d 非 DES 块倍数", ErrAES, len(cipherText))
	}
	block, err := des.NewCipher([]byte(key))
	if err != nil {
		return "", fmt.Errorf("cn35protocol: DES key: %w", err)
	}
	out := make([]byte, len(cipherText))
	for i := 0; i < len(cipherText); i += des.BlockSize {
		block.Decrypt(out[i:i+des.BlockSize], cipherText[i:i+des.BlockSize])
	}
	pad := int(out[len(out)-1])
	if pad == 0 || pad > des.BlockSize || pad > len(out) {
		return "", fmt.Errorf("%w: 身份头填充字节非法 %d", ErrAES, pad)
	}
	for _, v := range out[len(out)-pad:] {
		if int(v) != pad {
			return "", fmt.Errorf("%w: 身份头 PKCS5 填充不一致", ErrAES)
		}
	}
	return string(out[:len(out)-pad]), nil
}

// HashIdentity 对应 CryptManager.SHA1(RandomHash + str)：APP_ID_2 期望值。
// 返回小写 hex（HexStr 字符表）。
func HashIdentity(plain string) string {
	sum := sha1.Sum([]byte(IdentityHashSalt + plain))
	return hex.EncodeToString(sum[:])
}

// DecryptAPP_T 服务端解 APP_T 头（type1 逆运算）：
// base64 → AES-CBC-session 解密 → 明文应为 "<serverTime>merctotostoria"。
// 返回剥离盐后的时间戳串；盐不匹配时返回错误。
func DecryptAPP_T(value, s0, s1 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("%w: APP_T base64: %v", ErrEnvelope, err)
	}
	key, iv := GameCipher(s0, s1)
	plain, err := aesCBCDecrypt(key, iv, raw)
	if err != nil {
		return "", err
	}
	s := string(plain)
	if len(s) < len(AppTSalt) || s[len(s)-len(AppTSalt):] != AppTSalt {
		return "", fmt.Errorf("%w: APP_T 盐后缀不匹配", ErrEnvelope)
	}
	return s[:len(s)-len(AppTSalt)], nil
}
