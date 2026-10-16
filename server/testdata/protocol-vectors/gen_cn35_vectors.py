#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Independent CN 3.5.0 protocol vector oracle for `server/internal/cn35protocol`.

This script reimplements the libEncryptMiracle.so wire format from the
statically recovered spec (see docs/国服3.5.0实施任务/handoffs/03/crypto-spec.md)
using ONLY Python stdlib plus a compact built-in AES-128 — it never imports or
calls the Go implementation, so Go tests that decode these vectors are checked
against an independent oracle, not against themselves.

Wire summary (native dispatch miracle_processData @0xc2d4):

  type 1   base64(AES-CBC-enc(input))                      APP-T 头
  type 2   base64(AES-CBC-enc(input + "&stp=" + time()))   ?e= 查询参数
  type 3   frame(xor195(deflate(AES-static(envelope))))    /account 请求
           仅当明文含 "chooseServer" 时 envelope =
           {"r":S1,"m":..,"data":<input>}，否则原样加密
  type 4   AES-CBC-enc(input + "&stp=" + time())           POST 表单体（裸）
  type 300 xor113(frame(deflate(AES-static(marker[+S0]+json))))  /account 响应
           marker '1' -> 后 32B 为 S0 播种；'0' -> 直接 json
  type 301 inflate(AES-CBC-dec(body))                      游戏通道响应
           -> 服务端编码为 AES-CBC-enc(deflate(json))

  frame   = [00 64] + MD5(ChecksumKey + payload) + payload   (18B 头)
  request 方向 xor key = 0xC3 (195)，response 方向 = 0x71 (113)

  账号通道 AES（静态）：key=MD5("momocahehe") iv=MD5("c368c368")
  游戏通道 AES（会话）：key=MD5("c30ad3b93b50557b698147c34d128532"+S0+S1)
                      iv =MD5("3"+"8fba63c4aedb15a80b3663fb3b7ebcf"+S0+S1)
"""

import base64
import hashlib
import json
import os
import struct
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))

# ---------------- pure-python AES-128 ----------------
# Minimal AES (encrypt/decrypt, CBC, PKCS7) — stdlib only.

_SBOX = []
_INV_SBOX = []


def _init_aes():
    p = 1
    q = 1
    sbox = [0] * 256
    # generate sbox via GF(2^8) log/alog
    alog = [0] * 256
    log = [0] * 256
    x = 1
    for i in range(255):
        alog[i] = x
        log[x] = i
        x ^= (x << 1) ^ (0x1B if x & 0x80 else 0)
        x &= 0xFF
    alog[255] = alog[0]
    for i in range(256):
        # sbox[i] = affine(alog[(255 - log[i]) % 255]) — build via inverse then affine
        pass
    # build inverse table
    inv = [0] * 256
    for i in range(256):
        if i == 0:
            inv[i] = 0
        else:
            inv[i] = alog[(255 - log[i]) % 255]
    def affine(b):
        x = b
        for _ in range(4):
            b = ((b << 1) | (b >> 7)) & 0xFF
            x ^= b
        return x ^ 0x63
    for i in range(256):
        _SBOX.append(affine(inv[i]))
    for i in range(256):
        _INV_SBOX.append(0)
    for i, v in enumerate(_SBOX):
        _INV_SBOX[v] = i


def _xtime(a):
    return ((a << 1) ^ (0x1B if a & 0x80 else 0)) & 0xFF


def _mul(a, b):
    r = 0
    while b:
        if b & 1:
            r ^= a
        a = _xtime(a)
        b >>= 1
    return r


def _key_expansion(key):
    # AES-128: 16B key -> 176B round keys
    rcon = [0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36]
    w = list(key)
    for i in range(16, 176, 4):
        t = w[i - 4:i]
        if (i // 4) % 4 == 0:
            t = [_SBOX[t[1]] ^ rcon[i // 16 - 0],  # rotword+subword+rcon
                 _SBOX[t[2]], _SBOX[t[3]], _SBOX[t[0]]]
            # careful ordering below; rewritten properly after
        for j in range(4):
            w.append(w[i - 16 + j] ^ t[j])
    return w


def _key_expansion_fixed(key):
    rcon = [0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36]
    w = [list(key[i:i + 4]) for i in range(0, 16, 4)]
    for i in range(4, 44):
        t = w[i - 1][:]
        if i % 4 == 0:
            t = [_SBOX[t[1]], _SBOX[t[2]], _SBOX[t[3]], _SBOX[t[0]]]
            t[0] ^= rcon[i // 4 - 1]
        w.append([w[i - 4][j] ^ t[j] for j in range(4)])
    return w


def _aes_encrypt_block(blk, w):
    s = [blk[4 * c + r] for c in range(4) for r in range(4)]  # column-major state

    def add_rk(rd):
        for c in range(4):
            for r in range(4):
                s[4 * c + r] ^= w[4 * rd + c][r]

    def sub():
        for i in range(16):
            s[i] = _SBOX[s[i]]

    def shift():
        t = s[:]
        for r in range(4):
            for c in range(4):
                s[4 * c + r] = t[4 * ((c + r) % 4) + r]

    def mix():
        for c in range(4):
            a = s[4 * c:4 * c + 4]
            s[4 * c + 0] = _mul(a[0], 2) ^ _mul(a[1], 3) ^ a[2] ^ a[3]
            s[4 * c + 1] = a[0] ^ _mul(a[1], 2) ^ _mul(a[2], 3) ^ a[3]
            s[4 * c + 2] = a[0] ^ a[1] ^ _mul(a[2], 2) ^ _mul(a[3], 3)
            s[4 * c + 3] = _mul(a[0], 3) ^ a[1] ^ a[2] ^ _mul(a[3], 2)

    add_rk(0)
    for rd in range(1, 10):
        sub(); shift(); mix(); add_rk(rd)
    sub(); shift(); add_rk(10)
    return bytes(s)


def _aes_decrypt_block(blk, w):
    s = [blk[4 * c + r] for c in range(4) for r in range(4)]

    def add_rk(rd):
        for c in range(4):
            for r in range(4):
                s[4 * c + r] ^= w[4 * rd + c][r]

    def isub():
        for i in range(16):
            s[i] = _INV_SBOX[s[i]]

    def ishift():
        t = s[:]
        for r in range(4):
            for c in range(4):
                s[4 * c + r] = t[4 * ((c - r) % 4) + r]

    def imix():
        for c in range(4):
            a = s[4 * c:4 * c + 4]
            s[4 * c + 0] = _mul(a[0], 14) ^ _mul(a[1], 11) ^ _mul(a[2], 13) ^ _mul(a[3], 9)
            s[4 * c + 1] = _mul(a[0], 9) ^ _mul(a[1], 14) ^ _mul(a[2], 11) ^ _mul(a[3], 13)
            s[4 * c + 2] = _mul(a[0], 13) ^ _mul(a[1], 9) ^ _mul(a[2], 14) ^ _mul(a[3], 11)
            s[4 * c + 3] = _mul(a[0], 11) ^ _mul(a[1], 13) ^ _mul(a[2], 9) ^ _mul(a[3], 14)

    add_rk(10)
    for rd in range(9, 0, -1):
        ishift(); isub(); add_rk(rd); imix()
    ishift(); isub(); add_rk(0)
    return bytes(s)


def _pkcs7_pad(b, n=16):
    p = n - len(b) % n
    return b + bytes([p]) * p


def _pkcs7_unpad(b):
    return b[:-b[-1]]


def aes_enc(key, iv, plain):
    w = _key_expansion_fixed(key)
    plain = _pkcs7_pad(plain)
    out = b""
    prev = iv
    for i in range(0, len(plain), 16):
        blk = bytes(x ^ y for x, y in zip(plain[i:i + 16], prev))
        prev = _aes_encrypt_block(blk, w)
        out += prev
    return out


def aes_dec(key, iv, ct):
    w = _key_expansion_fixed(key)
    out = b""
    prev = iv
    for i in range(0, len(ct), 16):
        blk = ct[i:i + 16]
        out += bytes(x ^ y for x, y in zip(_aes_decrypt_block(blk, w), prev))
        prev = blk
    return _pkcs7_unpad(out)


# ---------------- cn35 wire format ----------------

CHECKSUM_KEY = b"%^&*@Q0je35i7qp9"
VERSION = 100
XOR_REQ = 0xC3
XOR_RESP = 0x71
ACCT_KEY = hashlib.md5(b"momocahehe").digest()
ACCT_IV = hashlib.md5(b"c368c368").digest()
KA_PREFIX = "c30ad3b93b50557b698147c34d128532"
KB_PREFIX = "8fba63c4aedb15a80b3663fb3b7ebcf"
APP_T_SALT = "merctotostoria"


def game_cipher(s0, s1):
    key = hashlib.md5((KA_PREFIX + s0 + s1).encode()).digest()
    iv = hashlib.md5(("3" + KB_PREFIX + s0 + s1).encode()).digest()
    return key, iv


def xor(b, k):
    return bytes(x ^ k for x in b)


def frame(payload):
    return struct.pack(">H", VERSION) + hashlib.md5(CHECKSUM_KEY + payload).digest() + payload


# type 3 request (client->server)
def t3_encode(plain):
    return frame(xor(zlib.compress(aes_enc(ACCT_KEY, ACCT_IV, plain)), XOR_REQ))


def t3_decode(wire):
    assert struct.unpack(">H", wire[:2])[0] == VERSION
    payload = wire[18:]
    assert hashlib.md5(CHECKSUM_KEY + payload).digest() == wire[2:18]
    return aes_dec(ACCT_KEY, ACCT_IV, zlib.decompress(xor(payload, XOR_REQ)))


# type 300 response (server->client)
def t300_encode(json_payload, seed):
    if seed:
        assert len(seed) == 32
        plain = b"1" + seed.encode() + json_payload
    else:
        plain = b"0" + json_payload
    return xor(frame(zlib.compress(aes_enc(ACCT_KEY, ACCT_IV, plain))), XOR_RESP)


def t300_decode(wire):
    framed = xor(wire, XOR_RESP)
    return aes_dec(ACCT_KEY, ACCT_IV, zlib.decompress(framed[18:]))


# type 301 game response (server->client): aes(deflate(json))
def t301_encode(json_payload, s0, s1):
    k, iv = game_cipher(s0, s1)
    return aes_enc(k, iv, zlib.compress(json_payload))


def t301_decode(body, s0, s1):
    k, iv = game_cipher(s0, s1)
    return zlib.decompress(aes_dec(k, iv, body))


# type 1: base64(aes(input))
def t1_encode(plain, s0, s1, unix_time=None):
    k, iv = game_cipher(s0, s1)
    return base64.b64encode(aes_enc(k, iv, plain))


# type 2/4: input + "&stp=" + time
def t24_plain(plain, unix_time):
    return plain + b"&stp=" + str(unix_time).encode()


def _selftest():
    # FIPS-197 AES-128 known answer
    k = bytes.fromhex("000102030405060708090a0b0c0d0e0f")
    p = bytes.fromhex("00112233445566778899aabbccddeeff")
    w = _key_expansion_fixed(k)
    ct = _aes_encrypt_block(p, w)
    assert ct.hex() == "69c4e0d86a7b0430d8cdb78070b4c55a", ct.hex()
    assert _aes_decrypt_block(ct, w) == p


def main():
    _init_aes()
    _selftest()

    s0 = "k7f2m9x1q4w8e3r6t0y5u2i8o1p4a7s3"
    s1 = "qhhNsWnkcE7ddT5xjDb4nuQBNSbJyoDm"  # 实机捕获的 S1 样本
    ts = "1789920261"

    acct_meta = b'[{"v":"3.5.0","mv":"1","ct":2,"sk":"deadbeef","aid":"com.happyelements.miraclecn.uc","lang":"cn","pf":"android","notify":true,"st":0}],[{"method":"metaInfo"}]]'
    acct_choose = b'[{"v":"3.5.0","mv":"1","ct":2,"sk":"deadbeef","aid":"com.happyelements.miraclecn.uc","lang":"cn","pf":"android","notify":true,"st":1}],[{"method":"chooseServer","server_id":1,"choose":"chooseServer"}]]'
    acct_resp = b'{"header":{"errCode":0},"results":[{"retCode":200,"method":"metaInfo","time":1700000000}],"events":[]}'
    game_resp = b'{"header":{"errCode":0},"results":[{"retCode":200,"data":{"preset":1}}]}'
    appt_in = b"1700000000" + APP_T_SALT.encode()
    query_in = b"method=preset_data&uid=666666"
    form_in = b"_method=update&nickname=tester"

    # set2：不同 uid / 中文昵称 / 不同长度
    acct_meta2 = b'[{"v":"3.5.0","mv":"1","ct":2,"sk":"cafef00d","aid":"com.happyelements.miraclecn.uc","lang":"zh","pf":"android","notify":false,"st":0}],[{"method":"getServerList","uid":777777}]]'
    acct_resp2 = '{"header":{"errCode":0},"results":[{"retCode":200,"method":"getServerList","servers":[{"id":1,"name":"国服"}]}],"events":[]}'.encode()
    game_resp2 = '{"header":{"errCode":0},"results":[{"retCode":200,"data":{"nickname":"梅露可","items":[1,2,3]}}]}'.encode()
    appt_in2 = b"1799999999" + APP_T_SALT.encode()
    query_in2 = b"method=preset_data&uid=777777&tutorial_session=true"
    form_in2 = "_method=update&nickname=梅露可·测试者".encode()

    vectors = []

    def add(name, typ, input_hex, expected, **kw):
        v = {"name": name, "type": typ, "input_hex": input_hex, **kw}
        v.update(expected)
        vectors.append(v)

    # --- type300 seed / noreseed ---
    add("cn35-t300-seed", 300, t300_encode(acct_resp, s0).hex(),
        {"expected_plain": acct_resp.decode(), "seeded_s0": s0})
    add("cn35-t300-seed2", 300, t300_encode(acct_resp2, s0).hex(),
        {"expected_plain": acct_resp2.decode(), "seeded_s0": s0})
    add("cn35-t300-noreseed", 300, t300_encode(acct_resp, "").hex(),
        {"expected_plain": acct_resp.decode(), "seeded_s0": ""})
    add("cn35-t300-noreseed2", 300, t300_encode(acct_resp2, "").hex(),
        {"expected_plain": acct_resp2.decode(), "seeded_s0": ""})

    # --- type301 ---
    add("cn35-t301-s0", 301, t301_encode(game_resp, s0, "").hex(),
        {"expected_plain": game_resp.decode(), "s0": s0, "s1": ""})
    add("cn35-t301-s0-2", 301, t301_encode(game_resp2, s0, "").hex(),
        {"expected_plain": game_resp2.decode(), "s0": s0, "s1": ""})
    add("cn35-t301-s0s1", 301, t301_encode(game_resp, s0, s1).hex(),
        {"expected_plain": game_resp.decode(), "s0": s0, "s1": s1})

    # --- type1 ---
    add("cn35-t1-appt", 1, t1_encode(appt_in, s0, "").decode(),
        {"expected_plain": appt_in.decode(), "s0": s0, "s1": "", "encoding": "base64"})
    add("cn35-t1-appt2", 1, t1_encode(appt_in2, s0, "").decode(),
        {"expected_plain": appt_in2.decode(), "s0": s0, "s1": "", "encoding": "base64"})
    add("cn35-t1-appt-s1", 1, t1_encode(appt_in, s0, s1).decode(),
        {"expected_plain": appt_in.decode(), "s0": s0, "s1": s1, "encoding": "base64"})

    # --- type2/4 ---
    add("cn35-t2-query", 2, t1_encode(t24_plain(query_in, ts), s0, "").decode(),
        {"expected_plain": (query_in + b"&stp=" + ts.encode()).decode(),
         "s0": s0, "s1": "", "encoding": "base64"})
    add("cn35-t2-query2", 2, t1_encode(t24_plain(query_in2, ts), s0, "").decode(),
        {"expected_plain": (query_in2 + b"&stp=" + ts.encode()).decode(),
         "s0": s0, "s1": "", "encoding": "base64"})
    add("cn35-t4-form", 4, aes_enc(*game_cipher(s0, ""), t24_plain(form_in, ts)).hex(),
        {"expected_plain": (form_in + b"&stp=" + ts.encode()).decode(),
         "s0": s0, "s1": "", "encoding": "hex"})
    add("cn35-t4-form2", 4, aes_enc(*game_cipher(s0, ""), t24_plain(form_in2, ts)).hex(),
        {"expected_plain": (form_in2 + b"&stp=" + ts.encode()).decode(),
         "s0": s0, "s1": "", "encoding": "hex"})

    # --- type3 ---
    add("cn35-t3-meta", 3, t3_encode(acct_meta).hex(),
        {"expected_plain": acct_meta.decode(), "wrapped": False})
    add("cn35-t3-meta2", 3, t3_encode(acct_meta2).hex(),
        {"expected_plain": acct_meta2.decode(), "wrapped": False})
    env = ('{"r":"%s","m":"%s","data":' % (s1, "deadbeef_integrity") ).encode() + acct_choose + b"}"
    add("cn35-t3-choose", 3, t3_encode(env).hex(),
        {"expected_plain": env.decode(), "wrapped": True,
         "envelope_r": s1, "envelope_data": acct_choose.decode()})

    doc = {
        "spec": "docs/国服3.5.0实施任务/handoffs/03/crypto-spec.md",
        "generated_by": "gen_cn35_vectors.py (independent oracle; stdlib AES)",
        "checksum_key": "%^&*@Q0je35i7qp9",
        "version": 100,
        "xor_request": 195, "xor_response": 113,
        "account_key_md5": "momocahehe", "account_iv_md5": "c368c368",
        "game_key_md5": KA_PREFIX + "+S0+S1",
        "game_iv_md5": "3+" + KB_PREFIX + "+S0+S1",
        "app_t_salt": APP_T_SALT,
        "s1_charset": "1234567890qwertyuiopasdfghjklzxcvbnmQAZWSXEDCRFVTGBYHNUJMIKOLP",
        "vectors": vectors,
    }
    out = os.path.join(HERE, "cn35-vectors.json")
    with open(out, "w", encoding="utf-8") as f:
        json.dump(doc, f, ensure_ascii=False, indent=2)
    print("wrote", out, "(%d vectors)" % len(vectors))


if __name__ == "__main__":
    main()
