package unpackerr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// start115Events starts a conservative 115 recent-operations probe. The
// response is intentionally not submitted as an archive task yet: 115 has
// multiple response shapes in the wild and the next stage must first map a
// confirmed file to its configured CID/CD2 path.
func (u *Unpackerr) start115Events() {
	cfg := u.CloudDrive2
	if !cfg.N115Enabled || !cfg.N115EventEnabled || strings.TrimSpace(cfg.N115Cookie) == "" {
		return
	}
	interval := cfg.N115EventInterval.Duration
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		u.poll115RecentOperations()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			u.poll115RecentOperations()
		}
	}()
	u.Printf("115 最近操作监控已启用，间隔 %s", interval)
}

func (u *Unpackerr) poll115RecentOperations() {
	cfg := u.CloudDrive2
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://life.115.com/api/1.0/web/1.0/life/recent_operations", nil)
	if err != nil {
		u.Errorf("115 最近操作请求创建失败：%v", err)
		return
	}
	req.Header.Set("Cookie", cfg.N115Cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://115.com/")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		u.Errorf("115 最近操作请求失败：%v", err)
		return
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		u.Errorf("115 最近操作读取失败：%v", err)
		return
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		u.Errorf("115 最近操作返回 HTTP %d", res.StatusCode)
		return
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		u.Errorf("115 最近操作响应不是 JSON：%v", err)
		return
	}
	u.Debugf("115 最近操作已同步（响应大小 %d 字节）", len(body))
}

func (u *Unpackerr) validate115Cookie() error {
	if strings.TrimSpace(u.CloudDrive2.N115Cookie) == "" {
		return fmt.Errorf("115 Cookie 未配置")
	}
	return nil
}
