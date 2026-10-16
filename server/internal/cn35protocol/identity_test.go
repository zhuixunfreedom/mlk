// identity_test.go 身份头编解码测试。DES-ECB/SHA1 期望值由独立工具生成：
//
//	printf '666666' | openssl enc -des-ecb -K 35743231364F6254 -nosalt | xxd -p
//	printf 'TYhGo022...666666' | shasum
//
// （openssl 默认 PKCS7 填充与客户端 AddPaddingPKCS5 的 b=8-len%8 等价。）
package cn35protocol_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
)

func TestIdentityDES_OracleVectors(t *testing.T) {
	cases := []struct {
		name, key, plain, wantHex string
	}{
		{"userId-666666", cn35protocol.IdentityDesKey, "666666", "71777d49834d08dd"},
		{"sk-ascii", cn35protocol.IdentityDesKey, "sk-test-abc123", "622cb6121204a3e8520b1148f5074315"},
		{"empty-prelogin", cn35protocol.IdentityDesKey, "", "2280cd62d4c9a6bc"},
		{"device-token", cn35protocol.DeviceTokenDesKey, "tokXYZ", "b8d40de04062ceda"},
	}
	for _, c := range cases {
		got, err := cn35protocol.EncryptIdentity(c.key, c.plain)
		if err != nil {
			t.Fatalf("%s encrypt: %v", c.name, err)
		}
		if got != c.wantHex {
			t.Fatalf("%s encrypt = %s, want %s", c.name, got, c.wantHex)
		}
		back, err := cn35protocol.DecryptIdentity(c.key, got)
		if err != nil {
			t.Fatalf("%s decrypt: %v", c.name, err)
		}
		if back != c.plain {
			t.Fatalf("%s decrypt = %q, want %q", c.name, back, c.plain)
		}
	}
}

func TestIdentityDES_UpperHexAccepted(t *testing.T) {
	// 客户端输出小写 hex，但服务端不应对大小写敏感。
	got, err := cn35protocol.DecryptIdentity(cn35protocol.IdentityDesKey, strings.ToUpper("71777d49834d08dd"))
	if err != nil || got != "666666" {
		t.Fatalf("upper hex: %q %v", got, err)
	}
}

func TestIdentityDES_Errors(t *testing.T) {
	if _, err := cn35protocol.DecryptIdentity(cn35protocol.IdentityDesKey, "zz"); err == nil {
		t.Fatal("非 hex 输入应报错")
	}
	if _, err := cn35protocol.DecryptIdentity(cn35protocol.IdentityDesKey, "71777d49"); err == nil {
		t.Fatal("非块倍数应报错")
	}
	// 篡改末字节破坏填充 → 分类错误，不得静默通过。
	bad := "71777d49834d0800"
	if _, err := cn35protocol.DecryptIdentity(cn35protocol.IdentityDesKey, bad); !errors.Is(err, cn35protocol.ErrAES) {
		t.Fatalf("篡改填充应报 ErrAES, got %v", err)
	}
}

func TestIdentitySHA1_Oracle(t *testing.T) {
	if got := cn35protocol.HashIdentity("666666"); got != "5506761ac9a27f4f800dbc90453e3ad9c4bc5fa6" {
		t.Fatalf("HashIdentity = %s", got)
	}
}

func TestAPP_T_RoundTrip(t *testing.T) {
	s0, s1 := strings.Repeat("a", 32), strings.Repeat("b", 32)
	v, err := cn35protocol.EncryptAPP_T("1700000000", s0, s1)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := cn35protocol.DecryptAPP_T(v, s0, s1)
	if err != nil || ts != "1700000000" {
		t.Fatalf("APP_T = %q %v", ts, err)
	}
	// 错误会话密钥 → 解密失败或盐不匹配，均为错误。
	if _, err := cn35protocol.DecryptAPP_T(v, s1, s0); err == nil {
		t.Fatal("错误会话密钥解 APP_T 应报错")
	}
}
