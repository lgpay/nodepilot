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
	"nodepilot/internal/auth"
	"nodepilot/internal/config"
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

func TestCreateInboundValidatesNodePortAndUniqueness(t *testing.T) {
	oldDB := store.DB
	store.DB = newServerTestDB(t)
	defer func() { store.DB = oldDB }()
	if err := store.DB.Create(&model.Node{ID: 1, Name: "test", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	missing := callJSON(t, CreateInbound, http.MethodPost, "/api/v1/nodes/99/inbounds", gin.Params{{Key: "id", Value: "99"}}, map[string]interface{}{"protocol": "vmess", "port": 10086})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing node status = %d, want 404", missing.Code)
	}
	badPort := callJSON(t, CreateInbound, http.MethodPost, "/api/v1/nodes/1/inbounds", gin.Params{{Key: "id", Value: "1"}}, map[string]interface{}{"protocol": "vmess", "port": 0})
	if badPort.Code != http.StatusBadRequest {
		t.Fatalf("bad port status = %d, want 400", badPort.Code)
	}
	first := callJSON(t, CreateInbound, http.MethodPost, "/api/v1/nodes/1/inbounds", gin.Params{{Key: "id", Value: "1"}}, map[string]interface{}{"protocol": "vmess", "port": 10086})
	if first.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.Code)
	}
	second := callJSON(t, CreateInbound, http.MethodPost, "/api/v1/nodes/1/inbounds", gin.Params{{Key: "id", Value: "1"}}, map[string]interface{}{"protocol": "vless", "port": 10086})
	if second.Code != http.StatusBadRequest {
		t.Fatalf("duplicate port status = %d, want 400", second.Code)
	}
}

func TestCreateClientRejectsMissingInbound(t *testing.T) {
	oldDB := store.DB
	store.DB = newServerTestDB(t)
	defer func() { store.DB = oldDB }()
	w := callJSON(t, CreateClient, http.MethodPost, "/api/v1/inbounds/99/clients", gin.Params{{Key: "id", Value: "99"}}, map[string]interface{}{"alias": "orphan"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing inbound status = %d, want 404", w.Code)
	}
}

func TestAuthMiddlewareRejectsUnknownSubject(t *testing.T) {
	oldDB, oldSecret := store.DB, config.JWTSecret
	store.DB = newServerTestDB(t)
	config.JWTSecret = []byte("test-secret")
	defer func() { store.DB, config.JWTSecret = oldDB, oldSecret }()
	token, err := auth.IssueJWT("unknown", 0)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)
	AuthMiddleware()(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestClientCRUDAndList(t *testing.T) {
	oldDB := store.DB
	store.DB = newServerTestDB(t)
	defer func() { store.DB = oldDB }()
	in := model.Inbound{NodeID: 1, Protocol: "vmess", Transport: "tcp", Port: 10086, Security: model.SecurityNone, Enabled: true}
	if err := store.DB.Create(&in).Error; err != nil {
		t.Fatal(err)
	}

	bad := callJSON(t, CreateClient, http.MethodPost, "/api/v1/inbounds/1/clients", gin.Params{{Key: "id", Value: fmt.Sprint(in.ID)}}, map[string]interface{}{
		"expire_time": "not-a-time",
	})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid expiry status = %d, want 400", bad.Code)
	}

	created := callJSON(t, CreateClient, http.MethodPost, "/api/v1/inbounds/1/clients", gin.Params{{Key: "id", Value: fmt.Sprint(in.ID)}}, map[string]interface{}{
		"alias": "alice", "traffic_limit_bytes": -10, "expire_time": "2030-01-02T03:04:05Z",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", created.Code, created.Body.String())
	}
	var createdBody struct {
		ID   uint   `json:"id"`
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	if createdBody.ID == 0 || createdBody.UUID == "" {
		t.Fatalf("unexpected create response: %s", created.Body.String())
	}

	listed := callJSON(t, ListClients, http.MethodGet, "/api/v1/inbounds/1/clients", gin.Params{{Key: "id", Value: fmt.Sprint(in.ID)}}, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listed.Code)
	}
	var clients []model.Client
	if err := json.Unmarshal(listed.Body.Bytes(), &clients); err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].Alias != "alice" || clients[0].TrafficLimitBytes != 0 {
		t.Fatalf("unexpected listed clients: %+v", clients)
	}

	updated := callJSON(t, UpdateClient, http.MethodPut, "/api/v1/clients/1", gin.Params{{Key: "id", Value: fmt.Sprint(createdBody.ID)}}, map[string]interface{}{
		"alias": "bob", "enabled": false, "traffic_limit_bytes": 1024, "expire_time": "",
	})
	if updated.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200", updated.Code)
	}
	var after model.Client
	if err := store.DB.First(&after, createdBody.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Alias != "bob" || after.Enabled || after.TrafficLimitBytes != 1024 || !after.ExpireTime.IsZero() {
		t.Fatalf("unexpected updated client: %+v", after)
	}

	deleted := callJSON(t, DeleteClient, http.MethodDelete, "/api/v1/clients/1", gin.Params{{Key: "id", Value: fmt.Sprint(createdBody.ID)}}, nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", deleted.Code)
	}
	if err := store.DB.First(&model.Client{}, createdBody.ID).Error; err == nil {
		t.Fatal("deleted client is still present")
	}
}
