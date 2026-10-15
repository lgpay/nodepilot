package service

import (
	"testing"

	"nodepilot/internal/model"
)

func TestValidateInbound(t *testing.T) {
	tests := []struct {
		name      string
		protocol  string
		transport string
		tls       bool
		certID    uint
		wantErr   bool
	}{
		{name: "valid vmess ws", protocol: "vmess", transport: "ws"},
		{name: "invalid protocol", protocol: "unknown", transport: "tcp", wantErr: true},
		{name: "invalid transport", protocol: "vmess", transport: "quic", wantErr: true},
		{name: "trojan requires tls", protocol: "trojan", transport: "tcp", wantErr: true},
		{name: "trojan requires certificate", protocol: "trojan", transport: "tcp", tls: true, wantErr: true},
		{name: "valid trojan", protocol: "trojan", transport: "tcp", tls: true, certID: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateInbound(tt.protocol, tt.transport, tt.tls, tt.certID); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateInbound() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPrepareInboundNormalizesReality(t *testing.T) {
	in := &model.Inbound{Protocol: "vless", Transport: "tcp", Security: model.SecurityReality, RealityDest: "example.com:443"}
	if err := PrepareInbound(in); err != nil {
		t.Fatalf("PrepareInbound() error = %v", err)
	}
	if in.RealityPrivateKey == "" || in.RealityShortIds == "" {
		t.Fatal("reality defaults were not generated")
	}
}
