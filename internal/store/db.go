package store

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"time"

	"nodepilot/internal/auth"
	"nodepilot/internal/model"
	"nodepilot/internal/secret"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// DB 全局数据库连接（MVP 单管理端 SQLite）
var DB *gorm.DB

// Init 打开 SQLite 并自动建表
func Init(dbPath string) error {
	// WAL 模式提升并发读写能力；busy_timeout 避免写锁等待立即报错；
	// foreign_keys=on 使外键约束（如级联删除）生效。
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return err
	}
	DB = db
	if err := db.AutoMigrate(
		&model.Admin{},
		&model.Node{},
		&model.Inbound{},
		&model.Client{},
		&model.ConfigVersion{},
		&model.Certificate{},
		&model.SubscriptionGroup{},
		&model.Setting{},
		&model.TrafficStat{},
		&model.NotificationChannel{},
	); err != nil {
		return err
	}
	// 默认端口范围
	if _, err := GetSetting("default_port_range"); err != nil {
		_ = SetSetting("default_port_range", "10000-65535")
	}
	// 旧版明文 token 迁移到 hash+enc（幂等，可重复执行）
	migrateNodeTokens()
	// 旧版明文订阅 token 迁移为 AES 密文（幂等）
	migrateSubscriptionTokens()
	return nil
}

// GetSetting 读取配置项，未找到返回错误
func GetSetting(key string) (string, error) {
	var s model.Setting
	if err := DB.First(&s, "key = ?", key).Error; err != nil {
		return "", err
	}
	return s.Value, nil
}

// SetSetting 写入/更新配置项
func SetSetting(key, value string) error {
	var s model.Setting
	err := DB.First(&s, "key = ?", key).Error
	if err != nil {
		s.Key = key
		s.Value = value
		return DB.Create(&s).Error
	}
	s.Value = value
	return DB.Model(&s).Update("value", value).Error
}

// InitAdmin 首次启动初始化管理员（表为空时写入随机密码账号，强制首次登录修改）。
// 返回生成的明文密码（仅此一次），调用方负责打印给运维；数据库仅存哈希与 MustChangePwd 标志。
func InitAdmin(username string) (string, error) {
	var c int64
	if err := DB.Model(&model.Admin{}).Count(&c).Error; err != nil {
		return "", err
	}
	if c > 0 {
		return "", nil
	}
	// 生成 18 字节随机密码（hex 36 字符），足够熵且便于抄录
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	password := hex.EncodeToString(buf)
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	if err := DB.Create(&model.Admin{
		Username:      username,
		PasswordHash:  hash,
		MustChangePwd: true,
	}).Error; err != nil {
		return "", err
	}
	log.Printf("[store] 初始化管理员 '%s'，已生成随机密码（请尽快登录修改）", username)
	return password, nil
}

// migrateNodeTokens 将历史明文 token 列迁移为 TokenHash + TokenEnc。
// 兼容 v0.1.0 之前直接存明文的库；新库无 token 列数据，本函数为空操作。
func migrateNodeTokens() {
	type legacy struct {
		ID    uint
		Token string
	}
	var rows []legacy
	// token 列可能不存在（全新库），忽略报错。
	if err := DB.Raw("SELECT id, token FROM nodes WHERE token IS NOT NULL AND token <> '' AND (token_hash IS NULL OR token_hash = '')").Scan(&rows).Error; err != nil {
		return
	}
	for _, r := range rows {
		enc, err := secret.Encrypt(r.Token)
		if err != nil {
			log.Printf("[store] 节点 #%d token 加密失败，跳过迁移: %v", r.ID, err)
			continue
		}
		if err := DB.Model(&model.Node{}).Where("id = ?", r.ID).Updates(map[string]interface{}{
			"token_hash": auth.TokenHash(r.Token),
			"token_enc":  enc,
			"token":      "",
		}).Error; err != nil {
			log.Printf("[store] 节点 #%d token 迁移失败: %v", r.ID, err)
		}
	}
}

// migrateSubscriptionTokens 将历史明文订阅 token 迁移为 AES 密文。
// 兼容 v0.1.0 之前直接存明文的库；能正常解密的视为已加密，跳过。
func migrateSubscriptionTokens() {
	var groups []model.SubscriptionGroup
	if err := DB.Find(&groups).Error; err != nil {
		return
	}
	for _, g := range groups {
		if g.Token == "" {
			continue
		}
		// 已加密（可解密）则跳过；解密失败说明是历史明文
		if _, err := secret.Decrypt(g.Token); err == nil {
			continue
		}
		enc, err := secret.Encrypt(g.Token)
		if err != nil {
			log.Printf("[store] 订阅 #%d token 加密失败，跳过迁移: %v", g.ID, err)
			continue
		}
		if err := DB.Model(&model.SubscriptionGroup{}).Where("id = ?", g.ID).Update("token", enc).Error; err != nil {
			log.Printf("[store] 订阅 #%d token 迁移失败: %v", g.ID, err)
		}
	}
}

// Now 便于统一时间来源
func Now() time.Time { return time.Now() }
