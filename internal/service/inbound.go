package service

import "fmt"

// ValidateInbound checks protocol-level constraints that are independent of
// HTTP or persistence. Security-specific normalization remains in configgen
// because it also generates REALITY defaults.
func ValidateInbound(protocol, transport string, tlsEnabled bool, certID uint) error {
	validProtocols := map[string]bool{
		"vmess": true, "vless": true, "trojan": true,
		"ss": true, "socks": true, "http": true,
	}
	if !validProtocols[protocol] {
		return fmt.Errorf("不支持的入站协议: %s", protocol)
	}
	validTransports := map[string]bool{"": true, "tcp": true, "ws": true, "grpc": true}
	if !validTransports[transport] {
		return fmt.Errorf("不支持的传输类型: %s", transport)
	}
	if protocol == "trojan" {
		if !tlsEnabled {
			return fmt.Errorf("trojan 入站必须启用 TLS（tls_enabled=true）")
		}
		if certID == 0 {
			return fmt.Errorf("trojan 入站启用 TLS 时必须指定证书（cert_id>0）")
		}
	}
	return nil
}
