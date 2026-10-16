package cn35protocol_test

// 消费 testdata/protocol-vectors/cn35-vectors.json —— 由独立的纯 Python
// 参考实现（gen_cn35_vectors.py，内置 AES，不调用本包）生成，逐向量验证
// 本包解码结果与独立预言机一致。

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
)

type cn35Vector struct {
	Name          string `json:"name"`
	Type          int    `json:"type"`
	InputHex      string `json:"input_hex"`
	ExpectedPlain string `json:"expected_plain"`
	SeededS0      string `json:"seeded_s0"`
	S0            string `json:"s0"`
	S1            string `json:"s1"`
	Encoding      string `json:"encoding"` // "hex" | "base64"
	Wrapped       bool   `json:"wrapped"`
	EnvelopeR     string `json:"envelope_r"`
	EnvelopeData  string `json:"envelope_data"`
}

type cn35VectorDoc struct {
	Vectors []cn35Vector `json:"vectors"`
}

func TestCN35OracleVectors(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "protocol-vectors", "cn35-vectors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var doc cn35VectorDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	if len(doc.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, v := range doc.Vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			var wire []byte
			var err error
			if v.Encoding == "base64" {
				wire, err = base64.StdEncoding.DecodeString(v.InputHex)
			} else {
				wire, err = hex.DecodeString(v.InputHex)
			}
			if err != nil {
				t.Fatalf("input decode: %v", err)
			}
			switch v.Type {
			case 300:
				back, s0, err := cn35protocol.DecodeAccountResponse(wire)
				if err != nil {
					t.Fatalf("DecodeAccountResponse: %v", err)
				}
				if s0 != v.SeededS0 {
					t.Fatalf("seeded S0 = %q want %q", s0, v.SeededS0)
				}
				if !bytes.Equal(back, []byte(v.ExpectedPlain)) {
					t.Fatalf("plain = %s", back)
				}
			case 301:
				back, err := cn35protocol.DecodeGameResponse(wire, v.S0, v.S1)
				if err != nil {
					t.Fatalf("DecodeGameResponse: %v", err)
				}
				if !bytes.Equal(back, []byte(v.ExpectedPlain)) {
					t.Fatalf("plain = %s", back)
				}
			case 1:
				// APP-T：base64(AES(input)) —— 逆运算为 AES 解密
				plain, err := cn35protocol.DecryptFormBody(wire, v.S0, v.S1)
				if err != nil {
					t.Fatalf("decrypt t1: %v", err)
				}
				if string(plain) != v.ExpectedPlain {
					t.Fatalf("plain = %q want %q", plain, v.ExpectedPlain)
				}
			case 2, 4:
				plain, err := cn35protocol.DecryptFormBody(wire, v.S0, v.S1)
				if err != nil {
					t.Fatalf("decrypt t%d: %v", v.Type, err)
				}
				// 服务端视角剥离 &stp= 后缀
				want := v.ExpectedPlain
				if i := bytes.LastIndex([]byte(want), []byte("&stp=")); i >= 0 {
					want = want[:i]
				}
				if string(plain) != want {
					t.Fatalf("plain = %q want %q", plain, want)
				}
			case 3:
				plain, err := cn35protocol.DecodeAccountRequest(wire)
				if err != nil {
					t.Fatalf("DecodeAccountRequest: %v", err)
				}
				biz, s1, wrapped, err := cn35protocol.UnwrapAccountRequest(plain)
				if err != nil {
					t.Fatalf("unwrap: %v", err)
				}
				if wrapped != v.Wrapped {
					t.Fatalf("wrapped = %v want %v", wrapped, v.Wrapped)
				}
				if v.Wrapped {
					if s1 != v.EnvelopeR {
						t.Fatalf("envelope r = %q want %q", s1, v.EnvelopeR)
					}
					if !bytes.Equal(bytes.TrimSpace(biz), []byte(v.EnvelopeData)) {
						t.Fatalf("envelope data = %s", biz)
					}
				} else if !bytes.Equal(plain, []byte(v.ExpectedPlain)) {
					t.Fatalf("plain = %s", plain)
				}
			default:
				t.Fatalf("unknown type %d", v.Type)
			}
		})
	}
}
