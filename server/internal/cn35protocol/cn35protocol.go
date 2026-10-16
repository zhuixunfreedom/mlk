// Package cn35protocol 实现国服 3.5.0 客户端 (libEncryptMiracle.so) 的线协议编解码。
//
// 本包由对 client/decoded/lib/armeabi-v7a/libEncryptMiracle.so 的静态反汇编
// 还原而来（Thumb 模式，dispatch miracle_processData@0xc2d4）。证据与推导过程
// 见 artifacts/tasks/toto-cn-3.5.0-onboarding 下 c-crypto-03 记录。
//
// 客户端原生导出：
//
//	miracle_processData(value1, len, type, *out) -> int   // 返回输出长度，*out=输出缓冲
//	miracle_SetIV(value1, len)                            // 死写 0x73004，从不被读（诱饵）
//	miracle_freeIntPtr(p)
//
// type 分派（dispatch @0xc2d4）：
//
//	1   -> 0xbf74  base64(AES-CBC-enc)            APP_T 头
//	2   -> 0xbff8  t1(input + "&stp=" + time())    ?e= 查询参数
//	3   -> 0xbc38  frame(xor195(deflate(aes)))     /account 请求体
//	4   -> 0xc138  aes(input + "&stp=" + time())   POST 表单体（裸 AES 输出）
//	300 -> 0xbabc  /account 响应解码（含 S0 播种）
//	301 -> 0xbf14  inflate(aes-dec)                游戏通道响应解码
//
// 会话状态（.bss 全局，均属进程内字符串）：
//
//	S0 @0x73380  32 字节会话密钥字符串，由 type300 收到 '1' 标记响应时写入
//	             （plain[1:0x21] 拷贝到 calloc(0x21)）。为 NULL 时任何
//	             type1/2/4/301 调用都会崩溃 —— 服务端首个 /account 响应必须播种。
//	S1 @0x73384  32 字符客户端随机盐（lrand48，字符集
//	             "1234567890qwertyuiopasdfghjklzxcvbnmQAZWSXEDCRFVTGBYHNUJMIKOLP"），
//	             仅在明文含 "chooseServer" 的 type3 请求中惰性生成，
//	             并作为信封 "r" 字段发送给服务端 —— 服务端从信封读取。
//
// 密钥材料（keyA @0xbdcc / keyB @0xbe84 构造，随后经 MD5 取 16 字节 @0xe78c）：
//
//	keyA_str = "c30ad3b93b50557b698147c34d128532" + S0 + S1 + "1f94a39edbafdeb95a51b879eedc31d0"
//	           但写入的 lenA 在追加后缀之前取（64+len(S1) = 32+len(S0)+len(S1)），
//	           故 MD5 输入实际为 "c30ad3b93b50557b698147c34d128532"+S0+S1。
//	keyB_str = "3" + "8fba63c4aedb15a80b3663fb3b7ebcf" + S0 + S1 + "8hf82hvaFEF2r2f2h88h2hdch9s8hf32"
//	           （0xbe34 构造 "3"+partial 后 0x394d0 为 assign 语义覆盖 s，非 append；
//	           已用 harness2 实机转储证实最终串形为 "3"+prefix+S0+S1+mid）。
//	           lenB 在追加 mid 之前取 = 1+31+len(S0)+len(S1) = 64+len(S1)，
//	           故 MD5 输入恰好为 "3"+prefix+S0+S1（mid 整段为死代码）。
//
// 帧格式（0xc584，与 1.5 一致）：18 字节头 = [00 64](版本100大端) + MD5(校验键+载荷)16B，
// 校验键 "%^&*@Q0je35i7qp9"。注意方向不对称：
//   - 请求(t3)：先 xor195 再加帧头 —— MD5 覆盖 xor 后的字节，帧头明文。
//   - 响应(t300)：整帧（含头）被 xor113 —— 客户端先 xor113 全量再丢弃头 18 字节，
//     因此客户端从不校验响应帧头 MD5。
package cn35protocol

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Err* 给出可分类的解码失败原因。
var (
	ErrTruncated   = errors.New("cn35protocol: 帧截断")
	ErrBadVersion  = errors.New("cn35protocol: 帧版本不匹配")
	ErrBadChecksum = errors.New("cn35protocol: 帧 MD5 校验失败")
	ErrZlib        = errors.New("cn35protocol: zlib 解压失败")
	ErrAES         = errors.New("cn35protocol: AES 解密失败")
	ErrBadMarker   = errors.New("cn35protocol: 响应标记字节非法")
	ErrEnvelope    = errors.New("cn35protocol: chooseServer 信封格式非法")
)

const (
	// Version 帧协议版本（帧头前两字节大端 u16 = 100，即字节 00 64）。
	Version = 100
	// HeaderSize 帧头长度 18 = 2 版本 + 16 MD5。
	HeaderSize = 18
	// RequestXOR 客户端->服务端方向 XOR 键（t3 用 0xC3=195）。
	RequestXOR byte = 0xC3
	// ResponseXOR 服务端->客户端方向 XOR 键（t300 用 0x71=113）。
	ResponseXOR byte = 0x71
	// ChecksumKey 帧头 MD5 盐。
	ChecksumKey = "%^&*@Q0je35i7qp9"

	// accountKeyStr/accountIVStr 账号通道 AES 常量（t3/t300，地址 0x5d020/0x5d02b）。
	accountKeyStr = "momocahehe"
	accountIVStr  = "c368c368"

	// 游戏通道密钥材料片段（keyA@0xbdcc/keyB@0xbe84）。
	keyAPrefix = "c30ad3b93b50557b698147c34d128532"
	keyBPrefix = "8fba63c4aedb15a80b3663fb3b7ebcf"
	keyBTail   = "3"
	// keyBMid 是原生 keyB 串尾部追加段；因 lenB 在追加前取值，永不进入 MD5 输入（死代码）。
	keyBMid = "8hf82hvaFEF2r2f2h88h2hdch9s8hf32"

	// AppTSalt APP_T 拼接盐（JSONRequester.cs:29）。
	AppTSalt = "merctotostoria"
	// StpParam 游戏请求追加的时间戳参数名（t2/t4 构造 "&stp="+unixtime）。
	StpParam = "stp"
	// S1Charset S1 随机盐字符集（t3 内 0xbb80）。
	S1Charset = "1234567890qwertyuiopasdfghjklzxcvbnmQAZWSXEDCRFVTGBYHNUJMIKOLP"
	// SessionKeyLen S0 长度（t300 固定拷贝 0x20）。
	SessionKeyLen = 32
	// MarkerSeed 播种标记字节 '1'。
	MarkerSeed = '1'
	// MarkerPlain 普通响应标记（非 '1' 的任意字节；惯例用 '0'）。
	MarkerPlain = '0'
)

// ---- 密钥推导 ----

// AccountCipher 返回账号通道 AES-128-CBC 的 (key, iv)：
// MD5("momocahehe"), MD5("c368c368")。两通道共用该常量对。
func AccountCipher() (key, iv []byte) {
	k := md5.Sum([]byte(accountKeyStr))
	v := md5.Sum([]byte(accountIVStr))
	return k[:], v[:]
}

// GameCipher 返回游戏通道 AES-128-CBC 的 (key, iv)。
// s0 为服务端下发的 32 字节会话密钥；s1 为客户端 chooseServer 信封 "r" 字段
// （未发送 chooseServer 前 S1 为空串）。与客户端 0xbdcc/0xbe84 的有效行为一致：
// keyA 与 keyB 的 "死后缀"（"1f94a39e..."/重复段）不进入 MD5。
func GameCipher(s0, s1 string) (key, iv []byte) {
	ka := md5.Sum([]byte(keyAPrefix + s0 + s1))
	kb := md5.Sum([]byte(keyBTail + keyBPrefix + s0 + s1))
	return ka[:], kb[:]
}

// ---- 账号通道（/account?uid=666666）----

// DecodeAccountRequest 解码一条 type3 请求体：
//
//	wire = [00 64] + MD5(ChecksumKey + xored) + xored
//	xored = xor195(zlib_deflate(aes_enc(envelope)))
func DecodeAccountRequest(wire []byte) ([]byte, error) {
	if len(wire) < HeaderSize {
		return nil, fmt.Errorf("%w: got %d bytes, need at least %d", ErrTruncated, len(wire), HeaderSize)
	}
	if v := binary.BigEndian.Uint16(wire[:2]); v != Version {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrBadVersion, v, Version)
	}
	payload := wire[HeaderSize:]
	sum := md5.Sum(append([]byte(ChecksumKey), payload...))
	if subtle.ConstantTimeCompare(wire[2:HeaderSize], sum[:]) != 1 {
		return nil, fmt.Errorf("%w: MD5(checksumKey || xored) mismatch", ErrBadChecksum)
	}
	compressed := xorBytes(payload, RequestXOR)
	cipherText, err := zlibInflate(compressed)
	if err != nil {
		return nil, err
	}
	key, iv := AccountCipher()
	return aesCBCDecrypt(key, iv, cipherText)
}

// EncodeAccountRequest 生成一条客户端方向 type3 请求（测试/校验工具用；
// 服务端正常路径只调用 DecodeAccountRequest）。chooseServer 信封由调用方拼装。
func EncodeAccountRequest(envelope []byte) ([]byte, error) {
	key, iv := AccountCipher()
	ct, err := aesCBCEncrypt(key, iv, envelope)
	if err != nil {
		return nil, err
	}
	comp, err := zlibDeflate(ct)
	if err != nil {
		return nil, err
	}
	xored := xorBytes(comp, RequestXOR)
	return encodeFrame(xored), nil
}

// EncodeAccountResponse 生成服务端 type300 响应帧：
//
//	plain  = marker + [S0 32B if marker=='1'] + json
//	cipher = aes_enc(plain); comp = deflate(cipher)
//	frame  = [00 64] + MD5(ChecksumKey + comp) + comp
//	wire   = xor113(frame)                      // 整个帧异或
//
// seed 非空时使用 '1' 标记并播种 S0（必须为 32 字节，否则客户端越界读）；
// seed 为空时使用 '0' 标记。
func EncodeAccountResponse(jsonPayload []byte, seed string) ([]byte, error) {
	var plain []byte
	if seed != "" {
		if len(seed) != SessionKeyLen {
			return nil, fmt.Errorf("cn35protocol: S0 必须 %d 字节, got %d", SessionKeyLen, len(seed))
		}
		plain = make([]byte, 0, 1+SessionKeyLen+len(jsonPayload))
		plain = append(plain, MarkerSeed)
		plain = append(plain, seed...)
	} else {
		plain = make([]byte, 0, 1+len(jsonPayload))
		plain = append(plain, MarkerPlain)
	}
	plain = append(plain, jsonPayload...)
	key, iv := AccountCipher()
	ct, err := aesCBCEncrypt(key, iv, plain)
	if err != nil {
		return nil, err
	}
	comp, err := zlibDeflate(ct)
	if err != nil {
		return nil, err
	}
	return xorBytes(encodeFrame(comp), ResponseXOR), nil
}

// DecodeAccountResponse 执行客户端 type300 解码（测试/向量比对用）。
// 返回 (payload, seededS0)；marker 非 '1' 时 seededS0 为 ""。
func DecodeAccountResponse(wire []byte) (payload []byte, seededS0 string, err error) {
	framed := xorBytes(wire, ResponseXOR)
	if len(framed) < HeaderSize {
		return nil, "", fmt.Errorf("%w: got %d bytes, need at least %d", ErrTruncated, len(framed), HeaderSize)
	}
	cipherText, err := zlibInflate(framed[HeaderSize:])
	if err != nil {
		return nil, "", err
	}
	key, iv := AccountCipher()
	plain, err := aesCBCDecrypt(key, iv, cipherText)
	if err != nil {
		return nil, "", err
	}
	if len(plain) == 0 {
		return nil, "", fmt.Errorf("%w: empty plaintext", ErrTruncated)
	}
	if plain[0] == MarkerSeed {
		if len(plain) < 1+SessionKeyLen {
			return nil, "", fmt.Errorf("%w: '1' 标记响应不足 %d 字节", ErrTruncated, 1+SessionKeyLen)
		}
		return plain[1+SessionKeyLen:], string(plain[1 : 1+SessionKeyLen]), nil
	}
	return plain[1:], "", nil
}

// ---- 游戏通道（{host}{path}?e=..., APP_T, Encrypted:true）----

// EncryptAPP_T 对应 type1：base64(aes_enc(serverTime + "merctotostoria"))。
// stp/时间戳由客户端时间生成，这里由调用方传入等价字符串。
func EncryptAPP_T(serverTime string, s0, s1 string) (string, error) {
	key, iv := GameCipher(s0, s1)
	ct, err := aesCBCEncrypt(key, iv, []byte(serverTime+AppTSalt))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// EncryptQuery 对应 type2：e = base64(aes_enc(query + "&stp=" + unixTime))。
// 输入为 '?' 之后的查询串；unixTime 为十进制 unix 秒（time()）。
func EncryptQuery(query, unixTime string, s0, s1 string) (string, error) {
	key, iv := GameCipher(s0, s1)
	ct, err := aesCBCEncrypt(key, iv, []byte(query+"&"+StpParam+"="+unixTime))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// EncryptFormBody 对应 type4：aes_enc(formdata + "&stp=" + unixTime) 裸输出。
func EncryptFormBody(formdata, unixTime string, s0, s1 string) ([]byte, error) {
	key, iv := GameCipher(s0, s1)
	return aesCBCEncrypt(key, iv, []byte(formdata+"&"+StpParam+"="+unixTime))
}

// DecryptFormBody 服务端解码 type4 请求体：AES 解密后剥离 "&stp=..." 后缀。
func DecryptFormBody(body []byte, s0, s1 string) ([]byte, error) {
	key, iv := GameCipher(s0, s1)
	plain, err := aesCBCDecrypt(key, iv, body)
	if err != nil {
		return nil, err
	}
	return stripStp(plain), nil
}

// DecryptQuery 服务端解码 ?e= 参数（type2 逆运算）。
func DecryptQuery(e string, s0, s1 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(e)
	if err != nil {
		return nil, fmt.Errorf("%w: base64: %v", ErrEnvelope, err)
	}
	return DecryptFormBody(raw, s0, s1)
}

// EncodeGameResponse 对应 type301 逆运算：
// 客户端收到 ENCRYPTED:true 响应时执行 inflate(aes_dec(body))，
// 故服务端下发 aes_enc(deflate(json))。
func EncodeGameResponse(jsonPayload []byte, s0, s1 string) ([]byte, error) {
	comp, err := zlibDeflate(jsonPayload)
	if err != nil {
		return nil, err
	}
	key, iv := GameCipher(s0, s1)
	return aesCBCEncrypt(key, iv, comp)
}

// DecodeGameResponse 执行客户端 type301（测试用）：inflate(aes_dec(body))。
func DecodeGameResponse(body []byte, s0, s1 string) ([]byte, error) {
	key, iv := GameCipher(s0, s1)
	comp, err := aesCBCDecrypt(key, iv, body)
	if err != nil {
		return nil, err
	}
	return zlibInflate(comp)
}

// UnwrapAccountRequest 处理 type3 解码明文的双形态：
//
//   - 明文含 "chooseServer" 的请求被原生包为信封
//     {"r":"<S1>","m":"<integrity>","data":<原请求体>}
//   - 其他请求（metaInfo 等）原样透传，无信封
//
// 注意：信封是字符串模板拼接产物而非合法 JSON —— data 段为原始请求体
// （形如 [{...}],[{...}]]，含顶层 `],[` 边界），故不能走标准 JSON 解析，
// 这里按固定字段位置宽容提取。
//
// 返回 (business, s1, wrapped)；非信封时 s1=""。服务端应以 r 字段作为
// 该会话后续游戏通道密钥的 S1（客户端惰性生成、只在 chooseServer 发送一次）。
// "m" 为客户端完整性签名，当前版本服务端不校验。
func UnwrapAccountRequest(plain []byte) (business []byte, s1 string, wrapped bool, err error) {
	const rPrefix = `{"r":"`
	const mPrefix = `","m":"`
	const dPrefix = `","data":`
	if !bytes.HasPrefix(plain, []byte(rPrefix)) {
		return plain, "", false, nil
	}
	rest := plain[len(rPrefix):]
	i := bytes.Index(rest, []byte(mPrefix))
	if i < 0 {
		return nil, "", false, fmt.Errorf("%w: envelope missing m separator", ErrEnvelope)
	}
	s1 = string(rest[:i])
	if len(s1) != SessionKeyLen {
		return nil, "", false, fmt.Errorf("%w: envelope r len %d, want %d", ErrEnvelope, len(s1), SessionKeyLen)
	}
	rest = rest[i+len(mPrefix):]
	j := bytes.Index(rest, []byte(dPrefix))
	if j < 0 {
		return nil, "", false, fmt.Errorf("%w: envelope missing data separator", ErrEnvelope)
	}
	data := rest[j+len(dPrefix):]
	// 信封末尾必有一个收尾 '}'；剥掉后即为原请求体。
	if len(data) == 0 || data[len(data)-1] != '}' {
		return nil, "", false, fmt.Errorf("%w: envelope missing trailing }", ErrEnvelope)
	}
	data = data[:len(data)-1]
	return data, s1, true, nil
}

// stripStp 剥离 "&stp=<ts>"（或前缀 "stp=<ts>"）后缀；找不到时原样返回。
func stripStp(plain []byte) []byte {
	if i := bytes.LastIndex(plain, []byte("&"+StpParam+"=")); i >= 0 {
		return plain[:i]
	}
	if i := bytes.LastIndex(plain, []byte(StpParam+"=")); i == 0 {
		return nil
	}
	return plain
}

// ---- 帧/原语 ----

// encodeFrame [00 64] + MD5(ChecksumKey+payload) + payload。
func encodeFrame(payload []byte) []byte {
	sum := md5.Sum(append([]byte(ChecksumKey), payload...))
	frame := make([]byte, HeaderSize+len(payload))
	binary.BigEndian.PutUint16(frame[:2], Version)
	copy(frame[2:HeaderSize], sum[:])
	copy(frame[HeaderSize:], payload)
	return frame
}

func xorBytes(in []byte, key byte) []byte {
	out := make([]byte, len(in))
	for i, v := range in {
		out[i] = v ^ key
	}
	return out
}

func zlibDeflate(plain []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(plain); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("%w: compress: %v", ErrZlib, err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("%w: compress close: %v", ErrZlib, err)
	}
	return buf.Bytes(), nil
}

func zlibInflate(compressed []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("%w: open: %v", ErrZlib, err)
	}
	plain, err := io.ReadAll(r)
	cerr := r.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrZlib, err)
	}
	if cerr != nil {
		return nil, fmt.Errorf("%w: close: %v", ErrZlib, cerr)
	}
	return plain, nil
}

func aesCBCEncrypt(key, iv, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAES, err)
	}
	// PKCS7 恒填充（len%16==0 时补一整块 16×0x10）：与独立 Python 预言机
	// gen_cn35_vectors.py:_pkcs7_pad 及客户端 native AES 语义一致。
	plain = pkcs7Pad(plain, aes.BlockSize)
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plain)
	return out, nil
}

func aesCBCDecrypt(key, iv, cipherText []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAES, err)
	}
	if len(cipherText) == 0 || len(cipherText)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: ciphertext length %d not a multiple of block size", ErrAES, len(cipherText))
	}
	out := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, cipherText)
	return pkcs7Unpad(out)
}

func pkcs7Pad(b []byte, n int) []byte {
	pad := n - len(b)%n
	return append(b, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 || len(b)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: padded length %d", ErrAES, len(b))
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(b) {
		return nil, fmt.Errorf("%w: bad PKCS7 padding byte %d", ErrAES, pad)
	}
	for _, v := range b[len(b)-pad:] {
		if int(v) != pad {
			return nil, fmt.Errorf("%w: inconsistent PKCS7 padding", ErrAES)
		}
	}
	return b[:len(b)-pad], nil
}
