package configgen

import (
	"encoding/json"

	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// XrayStreamSettings is the typed representation of xray streamSettings.
// Pointer fields keep protocol-specific sections out of unrelated transports.
type XrayStreamSettings struct {
	Network         string               `json:"network"`
	Security        string               `json:"security"`
	WSSettings      *XrayWSSettings      `json:"wsSettings,omitempty"`
	TCPSettings     *XrayTCPSettings     `json:"tcpSettings,omitempty"`
	GRPCSettings    *XrayGRPCSettings    `json:"grpcSettings,omitempty"`
	TLSSettings     *XrayTLSSettings     `json:"tlsSettings,omitempty"`
	RealitySettings *XrayRealitySettings `json:"realitySettings,omitempty"`
}

type XrayWSSettings struct {
	Path    string                 `json:"path"`
	Headers map[string]interface{} `json:"headers,omitempty"`
}

type XrayTCPSettings struct {
	Header XrayTCPHeader `json:"header"`
}

type XrayTCPHeader struct {
	Type    string                 `json:"type"`
	Request map[string]interface{} `json:"request,omitempty"`
}

type XrayGRPCSettings struct {
	ServiceName string `json:"serviceName"`
	Mode        string `json:"mode,omitempty"`
}

type XrayTLSSettings struct {
	Certificates []XrayCertificate `json:"certificates"`
}

type XrayCertificate struct {
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

type XrayRealitySettings struct {
	Show        bool     `json:"show"`
	Dest        string   `json:"dest"`
	Xver        int      `json:"xver"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIds    []string `json:"shortIds"`
	SpiderX     string   `json:"spiderX"`
}

// streamInput is the persisted, backward-compatible representation of the
// transport options entered by the UI. It accepts both the historical wsPath
// shortcut and xray's native nested settings.
type streamInput struct {
	WsPath     string `json:"wsPath"`
	WsSettings struct {
		Path    string                 `json:"path"`
		Headers map[string]interface{} `json:"headers"`
	} `json:"wsSettings"`
	TcpSettings struct {
		Header struct {
			Type    string                 `json:"type"`
			Request map[string]interface{} `json:"request"`
		} `json:"header"`
	} `json:"tcpSettings"`
	GrpcSettings struct {
		ServiceName string `json:"serviceName"`
		Mode        string `json:"mode"`
	} `json:"grpcSettings"`
}

func parseStreamInput(raw string) streamInput {
	var input streamInput
	_ = json.Unmarshal([]byte(raw), &input)
	return input
}

func buildStreamSettings(in model.Inbound, network, security string) XrayStreamSettings {
	input := parseStreamInput(in.StreamSettings)
	stream := XrayStreamSettings{Network: network}

	switch network {
	case "ws":
		path := input.WsSettings.Path
		if path == "" {
			path = input.WsPath
		}
		if path == "" {
			path = "/v2ray"
		}
		ws := &XrayWSSettings{Path: path}
		if len(input.WsSettings.Headers) > 0 {
			ws.Headers = input.WsSettings.Headers
		}
		stream.WSSettings = ws
	case "tcp":
		// REALITY carries TLS itself and must use bare TCP.
		if security != model.SecurityReality {
			header := input.TcpSettings.Header
			if header.Type != "" && header.Type != "none" {
				tcp := XrayTCPHeader{Type: header.Type, Request: header.Request}
				stream.TCPSettings = &XrayTCPSettings{Header: tcp}
			}
		}
	case "grpc":
		serviceName := input.GrpcSettings.ServiceName
		if serviceName == "" {
			serviceName = "xray"
		}
		stream.GRPCSettings = &XrayGRPCSettings{ServiceName: serviceName, Mode: input.GrpcSettings.Mode}
	}

	switch security {
	case model.SecurityReality:
		stream.Security = model.SecurityReality
		stream.RealitySettings = buildRealitySettings(in)
	case model.SecurityTLS:
		stream.Security = model.SecurityTLS
		certFile, keyFile := agentCertFile, agentKeyFile
		if in.TLSCertID > 0 && store.DB != nil {
			var cert model.Certificate
			if store.DB.First(&cert, in.TLSCertID).Error == nil && cert.CertPath != "" {
				certFile, keyFile = cert.CertPath, cert.KeyPath
			}
		}
		stream.TLSSettings = &XrayTLSSettings{Certificates: []XrayCertificate{{CertificateFile: certFile, KeyFile: keyFile}}}
	default:
		stream.Security = model.SecurityNone
	}
	return stream
}
