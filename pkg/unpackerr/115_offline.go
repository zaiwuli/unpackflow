package unpackerr

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/julienschmidt/httprouter"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"golift.io/xtractr"
)

const n115OfflineBase = "https://115.com/web/lixian/"

var offlineLinkPattern = regexp.MustCompile(`(?i)(?:ed2k://\|file\|.*?\|/|magnet:\?[^\s]+|(?:https?|ftp)://[^\s]+)`)

type n115OfflineStore struct {
	Path    string                      `json:"-"`
	Batches map[string]*n115OfflineBatch `json:"batches"`
}

type n115OfflineBatch struct {
	ID          string             `json:"id"`
	CreatedAt   time.Time          `json:"created_at"`
	TargetCID   string             `json:"target_cid"`
	TargetName  string             `json:"target_name"`
	AutoExtract bool               `json:"auto_extract"`
	CheckCount  int                `json:"check_count"`
	NextCheck   time.Time          `json:"next_check,omitempty"`
	LastCheck   time.Time          `json:"last_check,omitempty"`
	Tasks       []*n115OfflineTask `json:"tasks"`
}

type n115OfflineTask struct {
	ID          string    `json:"id"`
	Link        string    `json:"link"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	InfoHash    string    `json:"info_hash,omitempty"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ExtractedAt time.Time `json:"extracted_at,omitempty"`
}

func parseOfflineLinks(text string) []string {
	matches := offlineLinkPattern.FindAllString(strings.ReplaceAll(text, "\r", ""), -1)
	seen := make(map[string]struct{}, len(matches))
	result := make([]string, 0, len(matches))
	for _, item := range matches {
		item = strings.TrimSpace(item)
		key := offlineLinkIdentity(item)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result
}

func offlineLinkIdentity(link string) string {
	value := strings.TrimSpace(link)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "ed2k://") {
		parts := strings.Split(value, "|")
		if len(parts) >= 6 {
			return "ed2k:" + strings.ToLower(parts[4]) + ":" + parts[3]
		}
	}
	if strings.HasPrefix(lower, "magnet:?") {
		if parsed, err := url.Parse(value); err == nil {
			for _, xt := range parsed.Query()["xt"] {
				if strings.HasPrefix(strings.ToLower(xt), "urn:btih:") {
					return strings.ToLower(xt)
				}
			}
		}
	}
	sum := sha256.Sum256([]byte(lower))
	return hex.EncodeToString(sum[:])
}

func offlineLinkName(link string) string {
	if strings.HasPrefix(strings.ToLower(link), "ed2k://") {
		parts := strings.Split(link, "|")
		if len(parts) > 2 {
			if value, err := url.QueryUnescape(parts[2]); err == nil && value != "" {
				return value
			}
			return parts[2]
		}
	}
	if parsed, err := url.Parse(link); err == nil {
		if name := parsed.Query().Get("dn"); name != "" {
			return name
		}
		if name := filepath.Base(parsed.Path); name != "." && name != "/" && name != "" {
			return name
		}
	}
	return "离线任务"
}

func offlineLinkKind(link string) string {
	if index := strings.Index(link, ":"); index > 0 {
		return strings.ToLower(link[:index])
	}
	return "unknown"
}

func (u *Unpackerr) load115OfflineStore() error {
	u.offlineMu.Lock()
	defer u.offlineMu.Unlock()
	base := filepath.Dir(u.ConfigFile)
	if base == "." || base == "" {
		base, _ = os.Getwd()
	}
	path := filepath.Join(base, "unpackflow-offline.json")
	store := n115OfflineStore{Path: path, Batches: make(map[string]*n115OfflineBatch)}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &store); err != nil {
			return fmt.Errorf("读取离线记录失败: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	store.Path = path
	if store.Batches == nil {
		store.Batches = make(map[string]*n115OfflineBatch)
	}
	u.offlineStore = store
	return nil
}

func (u *Unpackerr) save115OfflineStoreLocked() error {
	data, err := json.MarshalIndent(struct {
		Batches map[string]*n115OfflineBatch `json:"batches"`
	}{u.offlineStore.Batches}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(u.offlineStore.Path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(u.offlineStore.Path), ".offline-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, u.offlineStore.Path)
}

func (u *Unpackerr) start115OfflineMonitor() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if !u.taskSystemPaused.Load() {
				u.checkDue115OfflineBatches()
			}
		}
	}()
}

func (u *Unpackerr) checkDue115OfflineBatches() {
	if !u.offlineRunning.CompareAndSwap(false, true) {
		return
	}
	defer u.offlineRunning.Store(false)
	now := time.Now()
	u.offlineMu.RLock()
	ids := make([]string, 0)
	for id, batch := range u.offlineStore.Batches {
		if !batch.NextCheck.IsZero() && !batch.NextCheck.After(now) && offlineBatchNeedsCheck(batch) {
			ids = append(ids, id)
		}
	}
	u.offlineMu.RUnlock()
	for _, id := range ids {
		u.refresh115OfflineBatch(id)
	}
}

func offlineBatchNeedsCheck(batch *n115OfflineBatch) bool {
	for _, task := range batch.Tasks {
		if task.Status == "submitted" || task.Status == "downloading" || task.Status == "unknown" || task.Status == "failed" {
			return true
		}
	}
	return false
}

func (u *Unpackerr) n115OfflineListAPI(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	u.offlineMu.RLock()
	defer u.offlineMu.RUnlock()
	batches := make([]*n115OfflineBatch, 0, len(u.offlineStore.Batches))
	for _, batch := range u.offlineStore.Batches {
		batches = append(batches, batch)
	}
	sort.Slice(batches, func(i, j int) bool { return batches[i].CreatedAt.After(batches[j].CreatedAt) })
	u.writeJSON(w, map[string]any{"success": true, "batches": batches})
}

func (u *Unpackerr) n115OfflineImportAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !u.n115OfflineImportAuthorized(r) {
		http.Error(w, "离线导入令牌无效", http.StatusUnauthorized)
		return
	}
	if !u.CloudDrive2.N115Enabled || strings.TrimSpace(u.CloudDrive2.N115Cookie) == "" {
		http.Error(w, "请先启用115并配置Cookie", http.StatusBadRequest)
		return
	}
	parentCID := strings.TrimSpace(u.CloudDrive2.N115OfflineCID)
	if parentCID == "" {
		http.Error(w, "请先配置115离线保存目录CID", http.StatusBadRequest)
		return
	}
	text, autoExtract, err := readOfflineImport(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	links := parseOfflineLinks(text)
	if len(links) == 0 {
		http.Error(w, "没有识别到可用的ED2K、磁力或下载链接", http.StatusBadRequest)
		return
	}
	if len(links) > 1000 {
		http.Error(w, "单次最多导入1000条链接", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	date := time.Now().Format("2006-01-02")
	targetCID, err := u.n115DateFolderCID(ctx, parentCID, date)
	if err != nil {
		http.Error(w, "创建日期子文件夹失败："+err.Error(), http.StatusBadGateway)
		return
	}
	batch := &n115OfflineBatch{ID: time.Now().Format("20060102-150405.000"), CreatedAt: time.Now(), TargetCID: targetCID, TargetName: date, AutoExtract: autoExtract}
	existing := u.offlineIdentities()
	unique := make([]string, 0, len(links))
	for _, link := range links {
		if _, duplicate := existing[offlineLinkIdentity(link)]; !duplicate {
			unique = append(unique, link)
		}
	}
	for start := 0; start < len(unique); start += 10 {
		end := min(start+10, len(unique))
		results, submitErr := u.n115SubmitOfflineBatch(ctx, unique[start:end], targetCID)
		for _, link := range unique[start:end] {
			identity := offlineLinkIdentity(link)
			task := &n115OfflineTask{ID: identity, Link: link, Name: offlineLinkName(link), Kind: offlineLinkKind(link), Status: "submitted", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if submitErr != nil {
				task.Status, task.Error = "submit_failed", submitErr.Error()
			} else {
				task.InfoHash = results[identity]
			}
			batch.Tasks = append(batch.Tasks, task)
			existing[identity] = struct{}{}
		}
		if end < len(unique) {
			select {
			case <-ctx.Done():
				break
			case <-time.After(2 * time.Second):
			}
		}
	}
	if len(batch.Tasks) == 0 {
		http.Error(w, "识别到的链接都已导入过", http.StatusConflict)
		return
	}
	batch.NextCheck = time.Now().Add(time.Minute)
	u.offlineMu.Lock()
	u.offlineStore.Batches[batch.ID] = batch
	err = u.save115OfflineStoreLocked()
	u.offlineMu.Unlock()
	if err != nil {
		http.Error(w, "保存离线记录失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	u.writeJSON(w, map[string]any{"success": true, "batch": batch, "recognized": len(links), "submitted": len(batch.Tasks)})
}

func (u *Unpackerr) n115OfflineImportAuthorized(r *http.Request) bool {
	if referer := r.Referer(); referer != "" {
		if parsed, err := url.Parse(referer); err == nil && strings.EqualFold(parsed.Host, r.Host) { return true }
	}
	expected := strings.TrimSpace(u.CloudDrive2.N115OfflineToken)
	provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if provided == "" { provided = strings.TrimSpace(r.Header.Get("X-UnpackFlow-Token")) }
	return expected != "" && len(expected) == len(provided) && subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

func (u *Unpackerr) n115OfflineShortcutAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Disposition", `attachment; filename="unpackflow-shortcut.json"`)
	scheme := "http"
	if value := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); value != "" { scheme = value }
	endpoint := scheme + "://" + r.Host + strings.TrimSuffix(u.Webserver.URLBase, "/") + "/api/115/offline/import"
	u.writeJSON(w, map[string]any{
		"name": "导入到 UnpackFlow", "endpoint": endpoint, "method": "POST",
		"headers": map[string]string{"Content-Type": "application/json", "Authorization": "Bearer <设置中的离线导入令牌>"},
		"body": map[string]any{"text": "<快捷指令输入；为空时读取剪贴板>", "auto_extract": true},
		"steps": []string{"接收分享表单中的文本、URL和文件", "没有分享输入时获取剪贴板", "TXT文件先读取文本", "获取URL内容：POST JSON", "显示接口返回结果"},
	})
}

func readOfflineImport(r *http.Request) (string, bool, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(6 << 20); err != nil {
			return "", false, fmt.Errorf("上传内容无效")
		}
		text := r.FormValue("text")
		file, _, err := r.FormFile("file")
		if err == nil {
			defer file.Close()
			data, readErr := io.ReadAll(io.LimitReader(file, 5<<20))
			if readErr != nil {
				return "", false, readErr
			}
			text += "\n" + decodeOfflineText(data)
		}
		return text, r.FormValue("auto_extract") != "false", nil
	}
	if strings.Contains(contentType, "application/json") {
		var input struct {
			Text        string `json:"text"`
			AutoExtract *bool  `json:"auto_extract"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 5<<20)).Decode(&input); err != nil {
			return "", false, fmt.Errorf("请求格式错误")
		}
		auto := true
		if input.AutoExtract != nil {
			auto = *input.AutoExtract
		}
		return input.Text, auto, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	return string(data), true, err
}

func decodeOfflineText(data []byte) string {
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	if utf8.Valid(data) {
		return string(data)
	}
	decoded, err := io.ReadAll(transform.NewReader(strings.NewReader(string(data)), simplifiedchinese.GB18030.NewDecoder()))
	if err == nil && utf8.Valid(decoded) {
		return string(decoded)
	}
	return string(data)
}

func (u *Unpackerr) offlineIdentities() map[string]struct{} {
	u.offlineMu.RLock()
	defer u.offlineMu.RUnlock()
	result := make(map[string]struct{})
	for _, batch := range u.offlineStore.Batches {
		for _, task := range batch.Tasks {
			result[task.ID] = struct{}{}
		}
	}
	return result
}

func (u *Unpackerr) n115SubmitOfflineBatch(ctx context.Context, links []string, targetCID string) (map[string]string, error) {
	space, err := u.n115Request(ctx, http.MethodGet, n115OfflineBase, url.Values{"ct": {"lixian"}, "ac": {"space"}})
	if err != nil {
		return nil, err
	}
	sign := fmt.Sprint(space["sign"])
	timestamp := fmt.Sprint(space["time"])
	if data, ok := space["data"].(map[string]any); ok {
		if sign == "" || sign == "<nil>" { sign = fmt.Sprint(data["sign"]) }
		if timestamp == "" || timestamp == "<nil>" { timestamp = fmt.Sprint(data["time"]) }
	}
	if sign == "" || sign == "<nil>" || timestamp == "" || timestamp == "<nil>" {
		return nil, fmt.Errorf("115未返回离线签名")
	}
	form := url.Values{"wp_path_id": {targetCID}, "sign": {sign}, "time": {timestamp}}
	for index, link := range links {
		form.Set(fmt.Sprintf("url[%d]", index), link)
	}
	response, err := u.n115Request(ctx, http.MethodPost, n115OfflineBase+"?ct=lixian&ac=add_task_urls", form)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(links))
	if rows, ok := response["result"].([]any); ok && len(rows) > 0 {
		for index, raw := range rows {
			item, ok := raw.(map[string]any)
			if ok && index < len(links) {
				result[offlineLinkIdentity(links[index])] = fmt.Sprint(item["info_hash"])
			}
		}
	}
	return result, nil
}

func (u *Unpackerr) n115OfflineRefreshAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct{ BatchID string `json:"batch_id"` }
	_ = json.NewDecoder(r.Body).Decode(&input)
	if input.BatchID == "" {
		u.offlineMu.RLock()
		ids := make([]string, 0, len(u.offlineStore.Batches))
		for id := range u.offlineStore.Batches { ids = append(ids, id) }
		u.offlineMu.RUnlock()
		for _, id := range ids { u.refresh115OfflineBatch(id) }
	} else {
		u.refresh115OfflineBatch(input.BatchID)
	}
	u.n115OfflineListAPI(w, r, nil)
}

func (u *Unpackerr) refresh115OfflineBatch(id string) {
	u.offlineMu.RLock()
	batch := u.offlineStore.Batches[id]
	u.offlineMu.RUnlock()
	if batch == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	statuses, err := u.n115OfflineStatuses(ctx)
	now := time.Now()
	u.offlineMu.Lock()
	defer u.offlineMu.Unlock()
	batch = u.offlineStore.Batches[id]
	if batch == nil {
		return
	}
	batch.LastCheck = now
	batch.CheckCount++
	if err != nil {
		for _, task := range batch.Tasks {
			if task.Status == "submitted" || task.Status == "downloading" {
				task.Status, task.Error, task.UpdatedAt = "unknown", err.Error(), now
			}
		}
	} else {
		for _, task := range batch.Tasks {
			status, exists := statuses[strings.ToLower(task.InfoHash)]
			if !exists || task.InfoHash == "" {
				continue
			}
			task.Status, task.Error, task.UpdatedAt = status.Status, status.Error, now
		}
	}
	switch batch.CheckCount {
	case 1:
		batch.NextCheck = now.Add(3 * time.Minute)
	case 2:
		batch.NextCheck = now.Add(5 * time.Minute)
	default:
		interval := u.CloudDrive2.N115OfflineFallback.Duration
		if interval > 0 { batch.NextCheck = now.Add(interval) } else { batch.NextCheck = time.Time{} }
	}
	_ = u.save115OfflineStoreLocked()
	if batch.AutoExtract {
		go u.trigger115OfflineExtraction(batch.ID)
	}
}

type offlineRemoteStatus struct{ Status, Error string }

func (u *Unpackerr) n115OfflineStatuses(ctx context.Context) (map[string]offlineRemoteStatus, error) {
	all := make(map[string]offlineRemoteStatus)
	for page := 1; page <= 10; page++ {
		response, err := u.n115Request(ctx, http.MethodGet, n115OfflineBase, url.Values{"ct": {"lixian"}, "ac": {"task_lists"}, "page": {fmt.Sprint(page)}})
		if err != nil { return nil, err }
		pageStatuses := offlineStatuses(response)
		for key, value := range pageStatuses { all[key] = value }
		pageCount := int(int115Value(response["page_count"]))
		if pageCount == 0 {
			if data, ok := response["data"].(map[string]any); ok { pageCount = int(int115Value(data["page_count"])) }
		}
		if len(pageStatuses) == 0 || (pageCount > 0 && page >= pageCount) || (pageCount == 0 && len(pageStatuses) < 30) { break }
	}
	return all, nil
}

func offlineStatuses(response map[string]any) map[string]offlineRemoteStatus {
	result := make(map[string]offlineRemoteStatus)
	var rows []any
	for _, key := range []string{"tasks", "data", "result"} {
		if values, ok := response[key].([]any); ok {
			rows = values
			break
		}
		if wrapper, ok := response[key].(map[string]any); ok {
			if values, ok := wrapper["tasks"].([]any); ok {
				rows = values
				break
			}
		}
	}
	for _, raw := range rows {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		hash := strings.ToLower(fmt.Sprint(item["info_hash"]))
		statusValue := int115Value(item["status"])
		status := "downloading"
		if statusValue == 2 || fmt.Sprint(item["percentDone"]) == "100" || fmt.Sprint(item["percent_done"]) == "100" {
			status = "success"
		} else if statusValue < 0 {
			status = "failed"
		}
		result[hash] = offlineRemoteStatus{Status: status, Error: fmt.Sprint(item["error_msg"])}
	}
	return result
}

func (u *Unpackerr) trigger115OfflineExtraction(batchID string) {
	u.offlineMu.RLock()
	batch := u.offlineStore.Batches[batchID]
	if batch == nil {
		u.offlineMu.RUnlock()
		return
	}
	targetCID := batch.TargetCID
	ready := false
	for _, task := range batch.Tasks {
		ready = ready || (task.Status == "success" && task.ExtractedAt.IsZero())
	}
	u.offlineMu.RUnlock()
	if !ready {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	files, err := u.n115OfflineArchiveFiles(ctx, targetCID, 5, 5000)
	if err != nil {
		u.Errorf("115离线完成后扫描日期目录失败：%v", err)
		return
	}
	queued := 0
	for _, file := range files {
		if file.FID == "" || file.PickCode == "" || !xtractr.IsArchiveFile(file.Name) {
			continue
		}
		sourceCID := file.CID
		if sourceCID == "" { sourceCID = targetCID }
		version := ProcessedSource{Key: n115FileKey(sourceCID, file), Source: "115离线", Path: file.Name, Size: file.Size, ModifiedNS: file.MTime, SourceCID: sourceCID, CloudFile: &file}
		if u.wasProcessed(version) || u.hasFailedVersion(version) || u.hasPending115Task(version.Key) {
			continue
		}
		if _, loaded := u.n115Running.LoadOrStore(version.Key, struct{}{}); loaded {
			continue
		}
		queued++
		u.update115Transfer(version.Key, file.Name, "离线完成，等待115云解压", func(task *CD2Transfer) { task.Version = version; task.Source = "115离线" })
		go func(file n115File, version ProcessedSource) {
			defer u.n115Running.Delete(version.Key)
			u.n115Queue <- struct{}{}
			defer func() { <-u.n115Queue }()
			u.run115CloudExtract(n115FailureMapping(u.CloudDrive2, version.SourceCID), file, version)
		}(file, version)
	}
	if queued > 0 {
		u.Printf("115离线批次 %s 已发现并提交 %d 个压缩包", batchID, queued)
	}
}

func (u *Unpackerr) n115OfflineArchiveFiles(ctx context.Context, rootCID string, maxDepth, maxFiles int) ([]n115File, error) {
	type folder struct { cid string; depth int }
	queue := []folder{{cid: rootCID}}
	seen := map[string]struct{}{rootCID: {}}
	archives := make([]n115File, 0)
	visitedFiles := 0
	for len(queue) > 0 && visitedFiles < maxFiles {
		current := queue[0]
		queue = queue[1:]
		for offset := 0; visitedFiles < maxFiles; offset += n115ScanPageSize {
			files, err := u.n115ListFilesAt(ctx, current.cid, offset)
			if err != nil { return archives, err }
			visitedFiles += len(files)
			for _, file := range files {
				if file.CID == "" { file.CID = current.cid }
				if xtractr.IsArchiveFile(file.Name) { archives = append(archives, file) }
			}
			if len(files) < n115ScanPageSize { break }
		}
		if current.depth >= maxDepth { continue }
		folders, err := u.n115ChildFolderCIDs(ctx, current.cid)
		if err != nil { return archives, err }
		for _, cid := range folders {
			if _, exists := seen[cid]; exists { continue }
			seen[cid] = struct{}{}
			queue = append(queue, folder{cid: cid, depth: current.depth + 1})
		}
	}
	return archives, nil
}

func (u *Unpackerr) n115OfflineClearAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct{ Scope string `json:"scope"` }
	_ = json.NewDecoder(r.Body).Decode(&input)
	u.offlineMu.Lock()
	cleared := 0
	for id, batch := range u.offlineStore.Batches {
		remove := input.Scope == "all"
		if input.Scope == "completed" {
			remove = true
			for _, task := range batch.Tasks {
				if task.Status != "success" && task.Status != "submit_failed" {
					remove = false
				}
			}
		}
		if remove {
			cleared += len(batch.Tasks)
			delete(u.offlineStore.Batches, id)
		}
	}
	err := u.save115OfflineStoreLocked()
	u.offlineMu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	u.writeJSON(w, map[string]any{"success": true, "cleared": cleared})
}
