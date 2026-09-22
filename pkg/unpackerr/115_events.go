package unpackerr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Unpackerr/unpackerr/pkg/clouddrive"
)

const (
	n115APIBase   = "https://webapi.115.com"
	n115FilesBase = "https://aps.115.com"
)

// N115Mapping maps a monitored 115 folder to its local-extraction fallback.
// The two-part legacy format (source CID => CD2 path) remains accepted.
type N115Mapping struct {
	SourceCID   string
	FallbackCID string
	CD2Path     string
}

type N115DownloadMapping struct {
	CID      string
	CD2Path  string
	Approval bool
}

type n115APIError struct {
	Status int
	Detail string
}

func (e *n115APIError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("115 接口 HTTP %d：%s", e.Status, e.Detail)
	}
	return "115 接口返回失败：" + e.Detail
}

type n115File struct {
	FID      string `json:"fid"`
	CID      string `json:"cid"`
	PickCode string `json:"pc"`
	Name     string `json:"n"`
	Size     int64  `json:"s"`
	MTime    int64  `json:"te"`
}

type n115ExtractEntry struct {
	Name     string `json:"file_name"`
	Category int    `json:"file_category"`
}

func parse115Mappings(values []string) []N115Mapping {
	mappings := make([]N115Mapping, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, "=>")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		var item N115Mapping
		switch len(parts) {
		case 2:
			item = N115Mapping{SourceCID: parts[0], CD2Path: parts[1]}
		case 3:
			item = N115Mapping{SourceCID: parts[0], FallbackCID: parts[1], CD2Path: parts[2]}
		default:
			continue
		}
		if item.SourceCID != "" && item.CD2Path != "" {
			mappings = append(mappings, item)
		}
	}
	return mappings
}

func (m N115Mapping) fallbackEnabled() bool { return m.FallbackCID != "" && m.CD2Path != "" }

func clean115CIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func parse115DownloadMappings(values []string) []N115DownloadMapping {
	result := make([]N115DownloadMapping, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, "=>")
		for index := range parts {
			parts[index] = strings.TrimSpace(parts[index])
		}
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		mapping := N115DownloadMapping{CID: parts[0], CD2Path: parts[1]}
		if len(parts) >= 3 {
			mapping.Approval = strings.EqualFold(parts[2], "approval") || strings.EqualFold(parts[2], "manual")
		}
		result = append(result, mapping)
	}
	return result
}

func migrate115CloudSettings(cfg *CloudDriveConfig) {
	if cfg == nil {
		return
	}
	legacy := parse115Mappings(cfg.N115Mappings)
	if len(cfg.N115SourceCIDs) == 0 {
		for _, mapping := range legacy {
			cfg.N115SourceCIDs = append(cfg.N115SourceCIDs, mapping.SourceCID)
		}
		cfg.N115SourceCIDs = clean115CIDs(cfg.N115SourceCIDs)
	}
	if cfg.N115FailureCID == "" || cfg.N115FailureCD2Path == "" {
		for _, mapping := range legacy {
			if !mapping.fallbackEnabled() {
				continue
			}
			if cfg.N115FailureCID == "" {
				cfg.N115FailureCID = mapping.FallbackCID
			}
			if cfg.N115FailureCD2Path == "" {
				cfg.N115FailureCD2Path = mapping.CD2Path
			}
			break
		}
	}
}

func n115SourceCIDs(cfg CloudDriveConfig) []string {
	migrate115CloudSettings(&cfg)
	return clean115CIDs(cfg.N115SourceCIDs)
}

func n115FailureMapping(cfg CloudDriveConfig, sourceCID string) N115Mapping {
	migrate115CloudSettings(&cfg)
	return N115Mapping{SourceCID: sourceCID, FallbackCID: strings.TrimSpace(cfg.N115FailureCID), CD2Path: strings.TrimSpace(cfg.N115FailureCD2Path)}
}

func validate115CloudSettings(settings UIOverrides) error {
	if settings.N115Enabled == nil || !*settings.N115Enabled {
		return nil
	}
	sources := clean115CIDs(settings.N115SourceCIDs)
	downloads := parse115DownloadMappings(settings.N115Downloads)
	failureCID := strings.TrimSpace(settings.N115FailureCID)
	failurePath := strings.TrimSpace(settings.N115FailureCD2Path)
	if len(sources) > 0 && (failureCID == "" || failurePath == "") {
		return fmt.Errorf("配置云解压来源后，必须填写失败归档 CID 和对应 CD2 路径")
	}
	roles := make(map[string]string)
	addCID := func(cid, role string) error {
		cid = strings.TrimSpace(cid)
		if cid == "" {
			return nil
		}
		if previous, exists := roles[cid]; exists {
			return fmt.Errorf("CID %s 同时用于%s和%s", cid, previous, role)
		}
		roles[cid] = role
		return nil
	}
	for _, cid := range sources {
		if err := addCID(cid, "云解压来源"); err != nil {
			return err
		}
	}
	if err := addCID(failureCID, "失败归档"); err != nil {
		return err
	}
	if settings.N115SuccessAction == "archive" {
		if err := addCID(settings.N115ArchiveCID, "成功归档"); err != nil {
			return err
		}
	}
	paths := make([]string, 0, len(downloads)+1)
	pathRoles := make([]string, 0, len(downloads)+1)
	if failurePath != "" {
		paths = append(paths, failurePath)
		pathRoles = append(pathRoles, "失败归档")
	}
	for _, mapping := range downloads {
		if err := addCID(mapping.CID, "日常本地下载"); err != nil {
			return err
		}
		for index, existing := range paths {
			if cloudPathOverlap(existing, mapping.CD2Path) {
				return fmt.Errorf("%s目录与日常本地下载目录存在路径重叠：%s", pathRoles[index], mapping.CD2Path)
			}
		}
		paths = append(paths, mapping.CD2Path)
		pathRoles = append(pathRoles, "日常本地下载")
	}
	return nil
}

func cloudPathOverlap(first, second string) bool {
	first = path.Clean("/" + strings.TrimLeft(strings.TrimSpace(first), "/"))
	second = path.Clean("/" + strings.TrimLeft(strings.TrimSpace(second), "/"))
	return first == second || strings.HasPrefix(first, second+"/") || strings.HasPrefix(second, first+"/")
}

func (u *Unpackerr) start115Events() {
	cfg := u.CloudDrive2
	if !cfg.N115Enabled || !cfg.N115EventEnabled || strings.TrimSpace(cfg.N115Cookie) == "" {
		return
	}
	if len(n115SourceCIDs(cfg)) == 0 && len(parse115DownloadMappings(cfg.N115DownloadMappings)) == 0 {
		u.Errorf("115 生活事件已启用，但尚未配置云解压来源或本地下载文件夹")
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
	u.Printf("115 云解压监控已启用，间隔 %s", interval)
}

// Recent operations provides a cheap change signal. File metadata is always
// read from the configured source folders, so unrelated 115 activity is never
// submitted as a task.
func (u *Unpackerr) poll115RecentOperations() {
	u.n115SyncMu.Lock()
	defer u.n115SyncMu.Unlock()
	if err := u.validate115Cookie(); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := u.n115Request(ctx, http.MethodGet, "https://life.115.com/api/1.0/web/1.0/life/recent_operations", nil); err != nil {
		u.Debugf("115 最近操作同步失败，将继续检查指定目录：%v", err)
	}
	for _, sourceCID := range n115SourceCIDs(u.CloudDrive2) {
		mapping := n115FailureMapping(u.CloudDrive2, sourceCID)
		files, err := u.n115ListFiles(ctx, sourceCID)
		if err != nil {
			u.Errorf("115 读取目录 %s 失败：%v", sourceCID, err)
			continue
		}
		for _, file := range files {
			if file.FID == "" || file.PickCode == "" || !isCloudDriveArchiveEvent(file.Name) {
				continue
			}
			version := ProcessedSource{Key: n115FileKey(mapping.SourceCID, file), Source: "115", Path: file.Name, Size: file.Size, ModifiedNS: file.MTime}
			if u.wasProcessed(version) {
				continue
			}
			if _, loaded := u.n115Running.LoadOrStore(version.Key, struct{}{}); loaded {
				continue
			}
			u.update115Transfer(version.Key, file.Name, "等待 115 云解压", nil)
			go func(mapping N115Mapping, file n115File, version ProcessedSource) {
				defer u.n115Running.Delete(version.Key)
				u.n115Queue <- struct{}{}
				defer func() { <-u.n115Queue }()
				u.run115CloudExtract(mapping, file, version)
			}(mapping, file, version)
		}
	}
	for _, mapping := range parse115DownloadMappings(u.CloudDrive2.N115DownloadMappings) {
		files, err := u.n115ListFiles(ctx, mapping.CID)
		if err != nil {
			u.Errorf("115 读取本地下载目录 %s 失败：%v", mapping.CID, err)
			continue
		}
		for _, file := range files {
			if file.FID == "" || !isCloudDriveArchiveEvent(file.Name) {
				continue
			}
			version := ProcessedSource{Key: n115DownloadFileKey(mapping.CID, file), Source: "115 本地下载", Path: file.Name, Size: file.Size, ModifiedNS: file.MTime}
			if u.wasProcessed(version) || u.hasPending115Task(version.Key) {
				continue
			}
			if _, loaded := u.n115Running.LoadOrStore(version.Key, struct{}{}); loaded {
				continue
			}
			u.queue115LocalDownload(mapping, file, version)
			u.n115Running.Delete(version.Key)
		}
	}
}

func n115FileKey(sourceCID string, file n115File) string {
	return fmt.Sprintf("115|%s|%s|%d", sourceCID, file.FID, file.Size)
}

func n115DownloadFileKey(sourceCID string, file n115File) string {
	return fmt.Sprintf("115-download|%s|%s|%d", sourceCID, file.FID, file.Size)
}

func (u *Unpackerr) hasPending115Task(taskKey string) bool {
	if u.state == nil || taskKey == "" {
		return false
	}
	u.state.mu.RLock()
	defer u.state.mu.RUnlock()
	for _, pending := range u.state.Fallback115 {
		if pending.TaskKey == taskKey {
			return true
		}
	}
	return false
}

func (u *Unpackerr) queue115LocalDownload(mapping N115DownloadMapping, file n115File, version ProcessedSource) {
	fallback := N115Mapping{SourceCID: mapping.CID, FallbackCID: mapping.CID, CD2Path: mapping.CD2Path}
	u.save115DownloadTask(version.Key, "manual_download", mapping.Approval, fallback, file)
	if mapping.Approval {
		u.update115Transfer(version.Key, file.Name, "等待批准本地下载", func(task *CD2Transfer) {
			task.CanFallback = true
		})
		return
	}
	u.update115Transfer(version.Key, file.Name, "正在刷新本地下载目录", func(task *CD2Transfer) {
		task.CanFallback = false
	})
	u.refresh115Fallback(fallback, file)
}

func (u *Unpackerr) n115Request(ctx context.Context, method, endpoint string, form url.Values) (map[string]any, error) {
	var body io.Reader
	if form != nil && method == http.MethodGet {
		separator := "?"
		if strings.Contains(endpoint, "?") {
			separator = "&"
		}
		endpoint += separator + form.Encode()
	} else if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", u.CloudDrive2.N115Cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://115.com/")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &n115APIError{Status: res.StatusCode, Detail: n115ResponseSummary(raw)}
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("响应不是 JSON：%w", err)
	}
	if state, exists := response["state"].(bool); exists && !state {
		return nil, &n115APIError{Detail: n115ResponseSummary(raw)}
	}
	return response, nil
}

func n115ServiceFailure(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *n115APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden || apiErr.Status == http.StatusTooManyRequests || apiErr.Status >= 500 {
			return true
		}
		detail := strings.ToLower(apiErr.Detail)
		for _, marker := range []string{"未登录", "登录失效", "cookie", "token", "频繁", "限流", "服务异常", "系统繁忙", "维护", "unauthorized", "forbidden", "too many"} {
			if strings.Contains(detail, marker) {
				return true
			}
		}
	}
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// n115ResponseSummary preserves the server's useful error message without
// dumping a potentially huge response or any request credentials into logs.
func n115ResponseSummary(raw []byte) string {
	var response map[string]any
	if json.Unmarshal(raw, &response) == nil {
		parts := make([]string, 0, 3)
		for _, key := range []string{"error", "message", "msg", "errno", "code"} {
			if value, ok := response[key]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
				parts = append(parts, key+"="+fmt.Sprint(value))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "，")
		}
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "无返回内容"
	}
	if len(value) > 240 {
		value = value[:240] + "…"
	}
	return strconv.Quote(value)
}

func (u *Unpackerr) n115ListFiles(ctx context.Context, cid string) ([]n115File, error) {
	response, err := u.n115Request(ctx, http.MethodGet, n115FilesBase+"/natsort/files.php", url.Values{
		"cid": {cid}, "aid": {"1"}, "o": {"user_ptime"}, "asc": {"0"}, "offset": {"0"},
		"show_dir": {"1"}, "limit": {"115"}, "type": {"5"}, "natsort": {"1"}, "format": {"json"},
	})
	if err != nil {
		return nil, err
	}
	rawFiles, _ := response["data"].([]any)
	files := make([]n115File, 0, len(rawFiles))
	for _, raw := range rawFiles {
		file, ok := decode115File(raw)
		if ok && file.Name != "" {
			files = append(files, file)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].MTime > files[j].MTime })
	return files, nil
}

func decode115File(raw any) (n115File, bool) {
	item, ok := raw.(map[string]any)
	if !ok {
		return n115File{}, false
	}
	file := n115File{
		FID: string115Value(item["fid"]), CID: string115Value(item["cid"]),
		PickCode: string115Value(item["pc"]), Name: string115Value(item["n"]),
		Size: int115Value(item["s"]), MTime: int115Value(item["te"]),
	}
	return file, file.Name != ""
}

func string115Value(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case float64:
		return fmt.Sprintf("%.0f", value)
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func int115Value(value any) int64 {
	switch value := value.(type) {
	case float64:
		return int64(value)
	case json.Number:
		result, _ := value.Int64()
		return result
	case string:
		var result int64
		_, _ = fmt.Sscanf(value, "%d", &result)
		return result
	default:
		return 0
	}
}

func (u *Unpackerr) run115CloudExtract(mapping N115Mapping, file n115File, version ProcessedSource) {
	retries := u.CloudDrive2.N115RetryCount
	if retries == 0 {
		retries = 3
	}
	delay := u.CloudDrive2.N115RetryDelay.Duration
	if delay <= 0 {
		delay = 2 * time.Minute
	}
	var err error
	for attempt := uint(1); attempt <= retries; attempt++ {
		u.update115Transfer(version.Key, file.Name, "115 云端解压中", func(task *CD2Transfer) {
			task.Error = ""
		})
		u.Systemf("115 云解压开始（第 %d/%d 次）：%s", attempt, retries, file.Name)
		status, extractErr := u.n115SeparateExtract(file, mapping.SourceCID)
		if extractErr == nil && status == "success" {
			u.markProcessed(version)
			u.cd2Tasks.Delete(version.Key)
			u.handle115SuccessSource(mapping, file)
			u.notifyEvent(notifyComplete, "✅", "115 云解压完成", "115 云端", file.Name)
			u.Printf("115 云解压完成：%s", file.Name)
			return
		}
		if extractErr == nil {
			extractErr = fmt.Errorf("云解压状态：%s", status)
		}
		err = extractErr
		if attempt < retries {
			u.Systemf("115 云解压第 %d/%d 次失败，%s 后重试：%s：%v", attempt, retries, delay, file.Name, err)
			u.update115Transfer(version.Key, file.Name, fmt.Sprintf("115 云解压重试中（%d/%d）", attempt, retries), func(task *CD2Transfer) { task.Error = err.Error() })
			time.Sleep(delay)
		}
	}
	u.Errorf("115 云解压最终失败（已重试 %d 次）：%s：%v", retries, file.Name, err)
	if n115ServiceFailure(err) {
		u.update115Transfer(version.Key, file.Name, "115 服务异常，等待下次同步", func(task *CD2Transfer) { task.Error = err.Error() })
		u.notifyEvent(notifyComplete, "❌", "115 服务异常", "115 云端", file.Name)
		return
	}
	u.notifyEvent(notifyComplete, "❌", "115 云解压失败", "115 云端", file.Name)
	if !mapping.fallbackEnabled() {
		u.update115Transfer(version.Key, file.Name, "115 云解压失败", func(task *CD2Transfer) { task.Error = err.Error() })
		return
	}
	u.update115Transfer(version.Key, file.Name, "正在移动到失败归档目录", nil)
	if err := u.n115MoveToFallback(file.FID, mapping.FallbackCID); err != nil {
		u.Errorf("115 失败文件移动到失败归档目录失败：%s：%v", file.Name, err)
		u.update115Transfer(version.Key, file.Name, "移动到失败归档目录失败", func(task *CD2Transfer) { task.Error = err.Error() })
		return
	}
	u.Printf("115 云解压失败，已移动到失败归档目录：%s", file.Name)
	// The file now lives in FallbackCID. Persist that identity before deciding
	// whether to start a local copy, so a restart never loses the manual path.
	u.save115DownloadTask(version.Key, "cloud_failure", !u.CloudDrive2.N115AutoFallback, mapping, file)
	if u.CloudDrive2.N115AutoFallback {
		u.update115Transfer(version.Key, file.Name, "等待下载到本地解压", func(task *CD2Transfer) { task.CanFallback = false })
		u.refresh115Fallback(mapping, file)
		return
	}
	u.update115Transfer(version.Key, file.Name, "云解压失败，等待手动本地解压", func(task *CD2Transfer) {
		task.Error = err.Error()
		task.CanFallback = true
	})
}

func (u *Unpackerr) handle115SuccessSource(mapping N115Mapping, file n115File) {
	u.handle115SuccessFile(mapping.SourceCID, file.FID, file.Name)
}

func (u *Unpackerr) handle115SuccessFile(sourceCID, fid, fileName string) {
	switch strings.ToLower(strings.TrimSpace(u.CloudDrive2.N115SuccessAction)) {
	case "", "keep":
		return
	case "delete":
		if err := u.n115DeleteFile(sourceCID, fid); err != nil {
			u.Errorf("115 解压成功后删除原包失败：%s：%v", fileName, err)
		} else {
			u.Printf("115 解压成功后已删除原包：%s", fileName)
		}
	case "archive":
		archiveCID := strings.TrimSpace(u.CloudDrive2.N115ArchiveCID)
		if archiveCID == "" {
			u.Errorf("115 解压成功后归档原包失败：未填写归档 CID")
			return
		}
		if err := u.n115MoveToFallback(fid, archiveCID); err != nil {
			u.Errorf("115 解压成功后归档原包失败：%s：%v", fileName, err)
		} else {
			u.Printf("115 解压成功后已归档原包：%s", fileName)
		}
	}
}

func (u *Unpackerr) handle115FallbackLocalSuccess(pending PendingCD2) {
	if pending.N115TaskKey != "" {
		u.markProcessed(ProcessedSource{Key: pending.N115TaskKey, Source: "115 本地下载", Path: pending.N115FileName, Size: pending.N115Size, ModifiedNS: pending.N115MTime})
	}
	// Files routed through the failure archive or a daily download folder stay
	// where the user placed them. Successful cloud extraction has its own
	// delete/archive policy and is handled separately.
	u.removePending115FallbackForFile(pending.N115SourceCID, pending.N115FID)
}

func (u *Unpackerr) update115Transfer(key, fileName, state string, update func(*CD2Transfer)) {
	u.updateCD2Transfer(key, fileName, state, func(task *CD2Transfer) {
		task.Source = "115 云端"
		if update != nil {
			update(task)
		}
	})
}

func (u *Unpackerr) n115SeparateExtract(file n115File, targetCID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	info, err := u.n115Request(ctx, http.MethodGet, n115APIBase+"/files/extract_info", url.Values{
		"pick_code": {file.PickCode}, "file_name": {""}, "page_count": {"999"}, "paths": {"文件"},
	})
	if err != nil {
		return "", err
	}
	entries, paths := n115ExtractEntries(info)
	if len(entries) == 0 {
		return "failed", fmt.Errorf("无法读取压缩包内容")
	}
	// "Separate extraction" means each archive receives its own target folder.
	// This is deliberately created before submitting the extraction task, rather
	// than relying on the archive's internal top-level directory.
	outputCID, err := u.n115CreateFolder(ctx, targetCID, n115ExtractFolderName(file.Name))
	if err != nil {
		return "", err
	}
	form := url.Values{"pick_code": {file.PickCode}, "to_pid": {outputCID}, "paths": {paths}}
	for _, entry := range entries {
		if entry.Category == 0 {
			form.Add("extract_dir[]", entry.Name)
		} else {
			form.Add("extract_file[]", entry.Name)
		}
	}
	if _, err := u.n115Request(ctx, http.MethodPost, n115APIBase+"/files/add_extract_file", form); err != nil {
		return "", err
	}
	passwords := append([]string{""}, u.uiPasswords()...)
	for _, password := range passwords {
		status, err := u.n115WaitForExtract(ctx, file.PickCode, password)
		if err != nil {
			return "", err
		}
		if status != "password" {
			return status, nil
		}
	}
	return "failed", fmt.Errorf("需要密码或密码不正确")
}

func n115ExtractFolderName(name string) string {
	base := strings.TrimSpace(name)
	for _, suffix := range []string{".tar.gz", ".tar.bz2", ".tar.xz"} {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			return strings.TrimSpace(base[:len(base)-len(suffix)])
		}
	}
	if extension := path.Ext(base); extension != "" {
		base = strings.TrimSuffix(base, extension)
	}
	if base == "" {
		return "解压文件"
	}
	return base
}

func (u *Unpackerr) n115CreateFolder(ctx context.Context, parentCID, name string) (string, error) {
	response, err := u.n115Request(ctx, http.MethodPost, n115APIBase+"/files/add", url.Values{"pid": {parentCID}, "cname": {name}})
	if err != nil {
		return "", err
	}
	data, _ := response["data"].(map[string]any)
	for _, key := range []string{"cid", "file_id"} {
		if value := string115Value(data[key]); value != "" {
			return value, nil
		}
	}
	if value := string115Value(response["cid"]); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("115 创建解压目录未返回 CID")
}

func n115ExtractEntries(response map[string]any) ([]n115ExtractEntry, string) {
	data, _ := response["data"].(map[string]any)
	rawEntries, _ := data["list"].([]any)
	entries := make([]n115ExtractEntry, 0, len(rawEntries))
	for _, raw := range rawEntries {
		encoded, _ := json.Marshal(raw)
		var entry n115ExtractEntry
		if json.Unmarshal(encoded, &entry) == nil && entry.Name != "" {
			entries = append(entries, entry)
		}
	}
	parts := make([]string, 0)
	if rawPaths, ok := data["paths"].([]any); ok {
		for _, raw := range rawPaths {
			if value, ok := raw.(map[string]any)["file_name"].(string); ok && value != "" {
				parts = append(parts, value)
			}
		}
	}
	return entries, strings.Join(parts, "/")
}

func (u *Unpackerr) n115WaitForExtract(ctx context.Context, pickCode, password string) (string, error) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	timeout := time.NewTimer(9 * time.Minute)
	defer timeout.Stop()
	for {
		form := url.Values{"pick_code": {pickCode}}
		if password != "" {
			form.Set("secret", password)
		}
		response, err := u.n115Request(ctx, http.MethodPost, n115APIBase+"/files/push_extract", form)
		if err != nil {
			return "", err
		}
		switch n115ExtractStatus(response) {
		case -1, 0, 1:
		case 2, 3, 7:
			return "failed", nil
		case 6:
			return "password", nil
		default:
			return "success", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", fmt.Errorf("云解压等待超时")
		case <-ticker.C:
		}
	}
}

func n115ExtractStatus(response map[string]any) int {
	data, _ := response["data"].(map[string]any)
	value, exists := data["unzip_status"]
	if !exists {
		return -1
	}
	switch status := value.(type) {
	case float64:
		return int(status)
	case string:
		var result int
		_, _ = fmt.Sscanf(status, "%d", &result)
		return result
	default:
		return -1
	}
}

func (u *Unpackerr) n115MoveToFallback(fid, fallbackCID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := u.n115Request(ctx, http.MethodPost, n115APIBase+"/files/move", url.Values{"fid": {fid}, "pid": {fallbackCID}})
	return err
}

func (u *Unpackerr) n115DeleteFile(sourceCID, fid string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := u.n115Request(ctx, http.MethodPost, n115APIBase+"/rb/delete", url.Values{
		"pid": {sourceCID}, "fid": {fid}, "ignore_warn": {"1"},
	})
	return err
}

func (u *Unpackerr) refresh115Fallback(mapping N115Mapping, file n115File) {
	remoteFile := path.Join(mapping.CD2Path, file.Name)
	paths := clouddrive.MapCloudPathWithOverrides(remoteFile, nil, u.CloudDrive2.PathOverrides)
	u.cd2Mu.RLock()
	client := u.cd2Client
	u.cd2Mu.RUnlock()
	if client == nil {
		u.Errorf("115 本地下载任务已创建，但 CloudDrive2 未连接：%s", remoteFile)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := client.ForceRefresh(ctx, mapping.CD2Path); err != nil {
		u.Errorf("115 本地下载路径刷新失败：%s：%v", mapping.CD2Path, err)
	} else {
		u.Systemf("115 本地下载路径已刷新：%s", mapping.CD2Path)
	}
	go u.handleCloudDriveChange(client, clouddrive.Change{Path: remoteFile}, paths)
}

func (u *Unpackerr) save115DownloadTask(taskKey, kind string, approval bool, mapping N115Mapping, file n115File) {
	remoteFile := path.Join(mapping.CD2Path, file.Name)
	paths := clouddrive.MapCloudPathWithOverrides(remoteFile, nil, u.CloudDrive2.PathOverrides)
	fallback := Pending115{TaskKey: taskKey, SourceCID: mapping.FallbackCID, FallbackCID: mapping.FallbackCID, CD2Path: mapping.CD2Path, FID: file.FID, FileName: file.Name, Kind: kind, Approval: approval, Size: file.Size, MTime: file.MTime}
	for _, key := range n115FallbackKeys(paths, remoteFile) {
		fallback.Key = key
		u.savePending115Fallback(fallback)
	}
}

func n115FallbackKeys(paths []string, remoteFile string) []string {
	keys := []string{"remote|" + strings.ToLower(path.Clean("/"+strings.TrimLeft(remoteFile, "/")))}
	for _, item := range paths {
		if isCloudDriveArchiveEvent(item) {
			keys = append(keys, cloudDriveTaskKey(item))
		}
	}
	return keys
}

func (u *Unpackerr) validate115Cookie() error {
	if strings.TrimSpace(u.CloudDrive2.N115Cookie) == "" {
		return fmt.Errorf("115 Cookie 未配置")
	}
	return nil
}
