package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

var serverTestDBCounter atomic.Int64

func newServerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:server_api_test_%d?mode=memory&cache=shared", serverTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Inbound{}, &model.Client{}, &model.ConfigVersion{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func callJSON(t *testing.T, handler gin.HandlerFunc, method, path string, params gin.Params, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Params = params
	handler(c)
	return w
}

func TestCreateInboundRejectsUnknownSecurity(t *testing.T) {
	oldDB := store.DB
	store.DB = newServerTestDB(t)
	defer func() { store.DB = oldDB }()
	if err := store.DB.Create(&model.Node{ID: 1, Name: "test", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	w := callJSON(t, CreateInbound, http.MethodPost, "/api/v1/nodes/1/inbounds", gin.Params{{Key: "id", Value: "1"}}, map[string]interface{}{
		"protocol": "vmess", "transport": "tcp", "port": 10086, "security": "invalid",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestCreateInboundPersistsLegacySecurityDefaults(t *testing.T) {
	oldDB := store.DB
	store.DB = newServerTestDB(t)
	defer func() { store.DB = oldDB }()
	if err := store.DB.Create(&model.Node{ID: 1, Name: "test", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	w := callJSON(t, CreateInbound, http.MethodPost, "/api/v1/nodes/1/inbounds", gin.Params{{Key: "id", Value: "1"}}, map[string]interface{}{
		"protocol": "vmess", "transport": "tcp", "port": 10086,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var in model.Inbound
	if err := store.DB.First(&in).Error; err != nil {
		t.Fatal(err)
	}
	if in.Security != model.SecurityNone {
		t.Fatalf("security = %q, want %q", in.Security, model.SecurityNone)
	}
}
