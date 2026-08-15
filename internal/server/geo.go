package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// geoClient 带超时，避免外部 IP 归属查询挂起拖慢请求
var geoClient = &http.Client{Timeout: 6 * time.Second}

// geo 缓存：IP 基本固定，识别结果按 IP 缓存，避免重复调用外部免费服务
// （免费服务有速率限制，频繁查询易 429）。缓存无过期时间——节点 IP 极少变化，
// 若地址变更会走新 IP 重新识别。
type geoCacheEntry struct {
	code, name, city, region string
}

var (
	geoCacheMu sync.Mutex
	geoCache   = map[string]geoCacheEntry{}
)

// cachedGeoLookup 先查缓存，未命中则调外部服务并把成功结果写入缓存。
func cachedGeoLookup(ip string) (code, name, city, region string) {
	geoCacheMu.Lock()
	e, ok := geoCache[ip]
	geoCacheMu.Unlock()
	if ok {
		return e.code, e.name, e.city, e.region
	}
	code, name, city, region = geoLookup(ip)
	if code != "" {
		geoCacheMu.Lock()
		geoCache[ip] = geoCacheEntry{code, name, city, region}
		geoCacheMu.Unlock()
	}
	return code, name, city, region
}

// GeoLookup 根据节点地址（ip:port 或域名）查询所属国家（ISO 两位码 + 国家名 + 城市）。
// 前端在"自动识别区域"时调用，成功后把 country/city 写入节点（region 存国家名供旗帜，city 存城市更精确）。
func GeoLookup(c *gin.Context) {
	host := strings.TrimSpace(c.Query("host"))
	if host == "" {
		c.JSON(400, gin.H{"error": "host 不能为空"})
		return
	}
	host = hostOf(host)
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			c.JSON(404, gin.H{"error": "无法解析主机名 " + host})
			return
		}
		for _, v := range ips {
			if v4 := v.To4(); v4 != nil {
				ip = v4
				break
			}
		}
		if ip == nil {
			ip = ips[0]
		}
	}
	code, name, city, region := cachedGeoLookup(ip.String())
	if code == "" {
		c.JSON(502, gin.H{"error": "IP 归属查询失败，请稍后重试"})
		return
	}
	c.JSON(200, gin.H{"country_code": code, "country": name, "city": city, "region": region})
}

// geoLookup 依次尝试多个免费 IP 归属服务，返回第一个成功结果（国家码/国家名/城市/省州）。
func geoLookup(ip string) (code, name, city, region string) {
	// url/字段名/成功判定字段（statusKey+statusOK 为空则只取 codeKey）
	type probe struct {
		url, codeKey, nameKey, cityKey, regionKey, statusKey, statusOK string
	}
	probes := []probe{
		{"http://ip-api.com/json/%s?lang=zh-CN", "countryCode", "country", "city", "regionName", "status", "success"},
		{"https://ipwho.is/%s", "country_code", "country", "city", "region", "success", "true"},
		{"https://ipapi.co/%s/json/", "country_code", "country_name", "city", "region", "", ""},
	}
	for _, p := range probes {
		u := fmt.Sprintf(p.url, ip)
		resp, err := geoClient.Get(u)
		if err != nil {
			continue
		}
		var d map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&d)
		resp.Body.Close()
		if err != nil {
			continue
		}
		codeV, _ := d[p.codeKey].(string)
		if codeV == "" {
			continue
		}
		if p.statusKey != "" {
			switch v := d[p.statusKey].(type) {
			case string:
				if v != p.statusOK {
					continue
				}
			case bool:
				if !v {
					continue
				}
			default:
				continue
			}
		}
		nameV, _ := d[p.nameKey].(string)
		cityV, _ := d[p.cityKey].(string)
		regionV, _ := d[p.regionKey].(string)
		return strings.ToUpper(codeV), nameV, cityV, regionV
	}
	return "", "", "", ""
}
