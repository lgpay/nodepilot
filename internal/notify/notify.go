// Package notify 预警通知：邮件(SMTP) / 企业微信自建应用 / Telegram Bot 三种渠道，
// 统一通过 Dispatch(title, body) 扇出到所有已启用渠道。无外部依赖（stdlib 实现）。
package notify

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// Sender 单渠道发送器
type Sender interface {
	Send(title, body string) error
}

// BuildSender 按渠道类型解析 Config JSON，构造对应 Sender
func BuildSender(ch model.NotificationChannel) (Sender, error) {
	switch ch.Type {
	case "email":
		var c emailConfig
		if err := json.Unmarshal([]byte(ch.Config), &c); err != nil {
			return nil, fmt.Errorf("email config 解析失败: %w", err)
		}
		if c.SMTPHost == "" || c.From == "" || len(c.To) == 0 {
			return nil, fmt.Errorf("email 配置缺字段(host/from/to)")
		}
		if c.SMTPPort == 0 {
			c.SMTPPort = 465
		}
		return &EmailSender{cfg: c}, nil
	case "wecom":
		var c wecomConfig
		if err := json.Unmarshal([]byte(ch.Config), &c); err != nil {
			return nil, fmt.Errorf("wecom config 解析失败: %w", err)
		}
		if c.CorpID == "" || c.CorpSecret == "" || c.AgentID == 0 {
			return nil, fmt.Errorf("wecom 配置缺字段(corpid/corpsecret/agentid)")
		}
		return &WeComSender{cfg: c}, nil
	case "tg":
		var c tgConfig
		if err := json.Unmarshal([]byte(ch.Config), &c); err != nil {
			return nil, fmt.Errorf("tg config 解析失败: %w", err)
		}
		if c.BotToken == "" || c.ChatID == "" {
			return nil, fmt.Errorf("tg 配置缺字段(bot_token/chat_id)")
		}
		return &TGSender{cfg: c}, nil
	default:
		return nil, fmt.Errorf("未知渠道类型: %s", ch.Type)
	}
}

// Dispatch 向所有已启用渠道异步扇出；单渠道失败仅记录日志，不阻塞调用方。
func Dispatch(title, body string) {
	var channels []model.NotificationChannel
	if err := store.DB.Where("enabled = ?", true).Find(&channels).Error; err != nil {
		log.Printf("[notify] load channels failed: %v", err)
		return
	}
	if len(channels) == 0 {
		log.Printf("[notify] no enabled channel for: %s", title)
		return
	}
	for _, ch := range channels {
		go func(ch model.NotificationChannel) {
			s, err := BuildSender(ch)
			if err != nil {
				log.Printf("[notify] channel #%d (%s) config invalid: %v", ch.ID, ch.Type, err)
				return
			}
			if err := s.Send(title, body); err != nil {
				log.Printf("[notify] channel #%d (%s) send failed: %v", ch.ID, ch.Type, err)
				return
			}
			log.Printf("[notify] channel #%d (%s) sent: %s", ch.ID, ch.Type, title)
		}(ch)
	}
}

// ---- 邮件 (SMTP) ----

type emailConfig struct {
	SMTPHost string   `json:"smtp_host"`
	SMTPPort int      `json:"smtp_port"`
	SMTPUser string   `json:"smtp_user"`
	SMTPPass string   `json:"smtp_pass"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

// EmailSender 支持 465 隐式 TLS 与 587/25 STARTTLS
type EmailSender struct{ cfg emailConfig }

func (s *EmailSender) Send(title, body string) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.SMTPHost, s.cfg.SMTPPort)
	from := s.cfg.From
	to := s.cfg.To

	var auth smtp.Auth
	if s.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPass, s.cfg.SMTPHost)
	}

	msg := buildMail(from, to, title, body)

	var client *smtp.Client
	var err error
	if s.cfg.SMTPPort == 465 {
		conn, dErr := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{ServerName: s.cfg.SMTPHost})
		if dErr != nil {
			return fmt.Errorf("tls dial %s: %w", addr, dErr)
		}
		client, err = smtp.NewClient(conn, s.cfg.SMTPHost)
	} else {
		conn, dErr := net.DialTimeout("tcp", addr, 10*time.Second)
		if dErr != nil {
			return fmt.Errorf("dial %s: %w", addr, dErr)
		}
		client, err = smtp.NewClient(conn, s.cfg.SMTPHost)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok && s.cfg.SMTPPort != 465 {
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.SMTPHost}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("rcpt %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}
	return client.Quit()
}

func buildMail(from string, to []string, title, body string) []byte {
	var b bytes.Buffer
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + title + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(title + "\n\n" + body + "\n")
	return b.Bytes()
}

// ---- 企业微信自建应用 ----

type wecomConfig struct {
	CorpID     string `json:"corpid"`
	CorpSecret string `json:"corpsecret"`
	AgentID    int    `json:"agentid"`
	ToUser     string `json:"touser"` // @all 或 UserID 列表
	ToParty    string `json:"toparty"`
	ToTag      string `json:"totag"`
}

// WeComSender 企业微信自建应用消息：先 gettoken 取 access_token，再 message/send。
type WeComSender struct{ cfg wecomConfig }

var (
	wecomTokenCache   string
	wecomTokenExpire  time.Time
	wecomTokenMu      sync.Mutex
)

func (s *WeComSender) getToken() (string, error) {
	wecomTokenMu.Lock()
	defer wecomTokenMu.Unlock()
	if wecomTokenCache != "" && time.Now().Before(wecomTokenExpire) {
		return wecomTokenCache, nil
	}
	u := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		s.cfg.CorpID, s.cfg.CorpSecret)
	var resp struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := httpJSON(httpClient, "GET", u, nil, &resp); err != nil {
		return "", err
	}
	if resp.ErrCode != 0 {
		return "", fmt.Errorf("wecom gettoken errcode=%d %s", resp.ErrCode, resp.ErrMsg)
	}
	wecomTokenCache = resp.AccessToken
	exp := resp.ExpiresIn
	if exp <= 0 {
		exp = 7200
	}
	wecomTokenExpire = time.Now().Add(time.Duration(exp-200) * time.Second)
	return wecomTokenCache, nil
}

func (s *WeComSender) Send(title, body string) error {
	token, err := s.getToken()
	if err != nil {
		return err
	}
	payload := map[string]interface{}{
		"touser":  s.cfg.ToUser,
		"toparty": s.cfg.ToParty,
		"totag":   s.cfg.ToTag,
		"msgtype": "text",
		"agentid": s.cfg.AgentID,
		"text":    map[string]string{"content": title + "\n" + body},
	}
	u := "https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token=" + token
	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := httpJSON(httpClient, "POST", u, payload, &resp); err != nil {
		return err
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("wecom send errcode=%d %s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}

// ---- Telegram Bot ----

type tgConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// TGSender Telegram Bot API sendMessage
type TGSender struct{ cfg tgConfig }

func (s *TGSender) Send(title, body string) error {
	text := "🔔 *" + title + "*\n" + body
	payload := map[string]string{
		"chat_id":    s.cfg.ChatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	u := "https://api.telegram.org/bot" + s.cfg.BotToken + "/sendMessage"
	var resp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := httpJSON(httpClient, "POST", u, payload, &resp); err != nil {
		return err
	}
	if !resp.OK {
		// 兼容：部分客户端不支持 Markdown 时去掉 parse_mode 重试一次
		payload["parse_mode"] = ""
		payload["text"] = title + "\n" + body
		if err := httpJSON(httpClient, "POST", u, payload, &resp); err != nil {
			return err
		}
		if !resp.OK {
			return fmt.Errorf("telegram send failed: %s", resp.Description)
		}
	}
	return nil
}

// ---- HTTP 工具 ----

var httpClient = &http.Client{Timeout: 10 * time.Second}

// httpJSON 执行 JSON 请求并将响应解析到 out（out 可带 json tag 的结构体）
func httpJSON(c *http.Client, method, url string, body interface{}, out interface{}) error {
	var reqBody []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = b
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d for %s", resp.StatusCode, url)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
