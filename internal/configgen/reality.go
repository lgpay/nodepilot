package configgen

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	"nodepilot/internal/model"
)

// REALITY 密钥/参数助手。
// REALITY 使用 X25519（Curve25519）密钥对：服务端存私钥(privateKey)，
// 客户端链接用公钥(pbk)。编码与 `xray x25519` 输出一致：base64url 无填充。

// GenRealityKeys 生成 REALITY 所需的 X25519 密钥对。
// 返回 priv（服务端 privateKey）与 pub（客户端 pbk），均为 base64url 无填充编码。
func GenRealityKeys() (priv, pub string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return encodeKey(key.Bytes()), encodeKey(key.PublicKey().Bytes()), nil
}

// RealityPublicKey 由服务端私钥派生公钥(pbk)，用于生成客户端订阅链接。
func RealityPublicKey(priv string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil {
		return "", fmt.Errorf("reality privateKey 不是合法 base64url: %w", err)
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("reality privateKey 长度/格式不正确: %w", err)
	}
	return encodeKey(key.PublicKey().Bytes()), nil
}

func encodeKey(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// GenShortId 生成随机 shortId（8 位十六进制 = 4 字节），作为 REALITY 客户端标识之一。
// 服务端 shortIds 可含多个，客户端链接用其中一个即可。
func GenShortId() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// buildRealitySettings 生成 REALITY 服务端 realitySettings：
// 借用目标握手(target/dest)、SNI 白名(serverNames)、私钥、shortId 列表、spiderX。
func buildRealitySettings(in model.Inbound) *XrayRealitySettings {
	serverNames := splitCSV(in.RealityServerNames)
	if len(serverNames) == 0 {
		if h := destHost(in.RealityDest); h != "" {
			serverNames = []string{h}
		}
	}
	spiderX := in.RealitySpiderX
	if spiderX == "" {
		spiderX = "/"
	}
	return &XrayRealitySettings{
		Show: false, Dest: in.RealityDest, Xver: 0,
		ServerNames: serverNames, PrivateKey: in.RealityPrivateKey,
		ShortIds: splitCSV(in.RealityShortIds), SpiderX: spiderX,
	}
}

// splitCSV 拆分逗号分隔字段，去空白、去空项。
func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// destHost 从 "host:port" 取 host；无端口时原样返回。
func destHost(dest string) string {
	if h, _, err := net.SplitHostPort(dest); err == nil {
		return h
	}
	return dest
}

// NormalizeReality 归一化入站安全层并补全 REALITY 参数：
//   - security 为空时按 TLSEnabled 派生（tls/none），向后兼容旧库；
//   - security=reality 时校验协议必须 VLESS、传输必须 TCP、dest 非空；
//     私钥/shortId/serverNames/spiderX 缺省则自动生成/推导；并关闭 TLS（reality 承载 TLS）。
func NormalizeReality(in *model.Inbound) error {
	if in.Security == "" {
		if in.TLSEnabled {
			in.Security = model.SecurityTLS
		} else {
			in.Security = model.SecurityNone
		}
	}
	if in.Security != model.SecurityNone && in.Security != model.SecurityTLS && in.Security != model.SecurityReality {
		return fmt.Errorf("不支持的安全层: %s（仅支持 none、tls、reality）", in.Security)
	}
	if in.Security != model.SecurityReality {
		return nil
	}
	if in.Protocol != "vless" {
		return fmt.Errorf("REALITY 仅支持 VLESS 协议")
	}
	if in.Transport == "" {
		in.Transport = "tcp"
	}
	if in.Transport != "tcp" {
		return fmt.Errorf("REALITY 仅支持 TCP 传输（当前 %s）", in.Transport)
	}
	if strings.TrimSpace(in.RealityDest) == "" {
		return fmt.Errorf("REALITY 必须填写目标 dest（如 service.sh.189.cn:443）")
	}
	if strings.TrimSpace(in.RealityPrivateKey) == "" {
		priv, _, err := GenRealityKeys()
		if err != nil {
			return err
		}
		in.RealityPrivateKey = priv
	}
	if strings.TrimSpace(in.RealityShortIds) == "" {
		in.RealityShortIds = GenShortId()
	}
	if strings.TrimSpace(in.RealityServerNames) == "" {
		in.RealityServerNames = destHost(in.RealityDest)
	}
	if strings.TrimSpace(in.RealitySpiderX) == "" {
		in.RealitySpiderX = "/"
	}
	in.TLSEnabled = false // REALITY 自身承载 TLS，不再使用证书
	return nil
}
