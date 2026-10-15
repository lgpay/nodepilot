package configgen

import (
	"encoding/json"

	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

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

func buildStreamSettings(in model.Inbound, network, security string) map[string]interface{} {
	input := parseStreamInput(in.StreamSettings)
	stream := map[string]interface{}{"network": network}

	switch network {
	case "ws":
		path := input.WsSettings.Path
		if path == "" {
			path = input.WsPath
		}
		if path == "" {
			path = "/v2ray"
		}
		ws := map[string]interface{}{"path": path}
		if len(input.WsSettings.Headers) > 0 {
			ws["headers"] = input.WsSettings.Headers
		}
		stream["wsSettings"] = ws
	case "tcp":
		// REALITY carries TLS itself and must use bare TCP.
		if security != model.SecurityReality {
			header := input.TcpSettings.Header
			if header.Type != "" && header.Type != "none" {
				tcp := map[string]interface{}{"type": header.Type}
				if len(header.Request) > 0 {
					tcp["request"] = header.Request
				}
				stream["tcpSettings"] = map[string]interface{}{"header": tcp}
			}
		}
	case "grpc":
		serviceName := input.GrpcSettings.ServiceName
		if serviceName == "" {
			serviceName = "xray"
		}
		grpc := map[string]interface{}{"serviceName": serviceName}
		if input.GrpcSettings.Mode != "" {
			grpc["mode"] = input.GrpcSettings.Mode
		}
		stream["grpcSettings"] = grpc
	}

	switch security {
	case model.SecurityReality:
		stream["security"] = model.SecurityReality
		stream["realitySettings"] = buildRealitySettings(in)
	case model.SecurityTLS:
		stream["security"] = model.SecurityTLS
		certFile, keyFile := agentCertFile, agentKeyFile
		if in.TLSCertID > 0 && store.DB != nil {
			var cert model.Certificate
			if store.DB.First(&cert, in.TLSCertID).Error == nil && cert.CertPath != "" {
				certFile, keyFile = cert.CertPath, cert.KeyPath
			}
		}
		stream["tlsSettings"] = map[string]interface{}{
			"certificates": []map[string]interface{}{{"certificateFile": certFile, "keyFile": keyFile}},
		}
	default:
		stream["security"] = model.SecurityNone
	}
	return stream
}
