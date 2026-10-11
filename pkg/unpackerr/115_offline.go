package unpackerr

import (
	"context"
	"crypto/sha256"
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
const n115OfflineSpace = "https://115.com/"

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
	HandoffChecks int              `json:"handoff_checks,omitempty"`
	Tasks       []*n115OfflineTask `json:"tasks"`
}

type n115OfflineTask struct {
	ID          string    `json:"id"`
	Link        string    `json:"link"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	InfoHash    string    `json:"info_hash,omitempty"`
	Size           int64     `json:"size,omitempty"`
	ArchiveTaskKey string    `json:"archive_task_key,omitempty"`
	ArchiveName    string    `json:"archive_name,omitempty"`
	ArchiveCID     string    `json:"archive_cid,omitempty"`
	ArchiveFID     string    `json:"archive_fid,omitempty"`
	PipelineStatus string    `json:"pipeline_status,omitempty"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ExtractedAt time.Time `json:"extracted_at,omitempty"`
	RetryCount  int       `json:"retry_count,omitempty"`
	NotifiedStatus string `json:"notified_status,omitempty"`
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

func offlineLinkHash(link string) string {
	value := strings.TrimSpace(link)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "ed2k://") {
		parts := strings.Split(value, "|")
		if len(parts) >= 6 {
			return strings.ToLower(strings.TrimSpace(parts[4]))
		}
	}
	if strings.HasPrefix(lower, "magnet:?") {
		if parsed, err := url.Parse(value); err == nil {
			for _, xt := range parsed.Query()["xt"] {
				const prefix = "urn:btih:"
				if strings.HasPrefix(strings.ToLower(xt), prefix) {
					return strings.ToLower(strings.TrimSpace(xt[len(prefix):]))
				}
			}
		}
	}
	return ""
}

func offlineLinkSize(link string) int64 {
	value := strings.TrimSpace(link)
	if !strings.HasPrefix(strings.ToLower(value), "ed2k://") { return 0 }
	parts := strings.Split(value, "|")
	if len(parts) < 6 { return 0 }
	return int115Value(strings.TrimSpace(parts[3]))
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
		ticker := time.NewTicker(5 * time.Second)
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
		if task.Status == "submitted" || task.Status == "downloading" || task.Status == "unknown" || task.Status == "failed" || (task.Status == "success" && task.ExtractedAt.IsZero()) {
			return true
		}
	}
	return false
}

func (u *Unpackerr) triggerPending115OfflineExtractions() {
	u.offlineMu.RLock()
	ids := make([]string, 0)
	for id, batch := range u.offlineStore.Batches {
		if batch.AutoExtract && offlineBatchNeedsCheck(batch) { ids = append(ids, id) }
	}
	u.offlineMu.RUnlock()
	for _, id := range ids { u.trigger115OfflineExtraction(id) }
}

func (u *Unpackerr) n115OfflineListAPI(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	u.offlineMu.RLock()
	batches := make([]*n115OfflineBatch, 0, len(u.offlineStore.Batches))
	for _, batch := range u.offlineStore.Batches {
		copyBatch := *batch
		copyBatch.Tasks = make([]*n115OfflineTask, 0, len(batch.Tasks))
		for _, task := range batch.Tasks {
			copyTask := *task
			copyBatch.Tasks = append(copyBatch.Tasks, &copyTask)
		}
		batches = append(batches, &copyBatch)
	}
	u.offlineMu.RUnlock()
	for _, batch := range batches {
		for _, task := range batch.Tasks { task.PipelineStatus = u.offlinePipelineStatus(task) }
	}
	sort.Slice(batches, func(i, j int) bool { return batches[i].CreatedAt.After(batches[j].CreatedAt) })
	u.writeJSON(w, map[string]any{"success": true, "batches": batches})
}

func (u *Unpackerr) n115OfflineImportAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !u.CloudDrive2.N115Enabled || strings.TrimSpace(u.CloudDrive2.N115Cookie) == "" {
		http.Error(w, "请先启用115并配置Cookie", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(u.CloudDrive2.N115OfflineCID) == "" {
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
	result, err := u.import115OfflineLinks(ctx, links, autoExtract)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	u.writeJSON(w, map[string]any{"success": true, "message": fmt.Sprintf("本次导入 %d 条", result.Submitted)})
}

type offlineImportResult struct {
	BatchID   string
	Recognized int
	Submitted int
	Duplicates int
	Failed    int
}

func (u *Unpackerr) import115OfflineLinks(ctx context.Context, links []string, autoExtract bool) (offlineImportResult, error) {
	u.offlineImportMu.Lock()
	defer u.offlineImportMu.Unlock()
	result := offlineImportResult{Recognized: len(links)}
	parentCID := strings.TrimSpace(u.CloudDrive2.N115OfflineCID)
	if parentCID == "" { return result, fmt.Errorf("请先配置115离线保存目录CID") }
	date := time.Now().Format("2006-01-02")
	targetCID, err := u.n115DateFolderCID(ctx, parentCID, date)
	if err != nil {
		return result, fmt.Errorf("创建日期子文件夹失败：%w", err)
	}
	batch := &n115OfflineBatch{ID: time.Now().Format("20060102-150405.000"), CreatedAt: time.Now(), TargetCID: targetCID, TargetName: date, AutoExtract: autoExtract}
	result.BatchID = batch.ID
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
			task := &n115OfflineTask{ID: identity, Link: link, Name: offlineLinkName(link), Kind: offlineLinkKind(link), Size: offlineLinkSize(link), Status: "submitted", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if submitErr != nil {
				task.Status, task.Error = "submit_failed", submitErr.Error()
			} else {
				task.InfoHash = results[identity]
				if task.InfoHash == "" { task.InfoHash = offlineLinkHash(link) }
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
		u.Systemf("115离线导入：本次导入 0 条，重复跳过 %d 条", len(links))
		u.notifyOfflineSummary(batch.ID, 0, len(links), 0)
		result.Duplicates = len(links)
		return result, nil
	}
	submitted, failed := 0, 0
	for _, task := range batch.Tasks {
		if task.Status == "submitted" { submitted++ }
		if task.Status == "submit_failed" { failed++ }
	}
	if submitted > 0 {
		batch.NextCheck = time.Now().Add(15 * time.Second)
	}
	u.offlineMu.Lock()
	u.offlineStore.Batches[batch.ID] = batch
	err = u.save115OfflineStoreLocked()
	u.offlineMu.Unlock()
	if err != nil {
		return result, fmt.Errorf("保存离线记录失败：%w", err)
	}
	duplicates := len(links) - len(unique)
	result.Submitted, result.Duplicates, result.Failed = submitted, duplicates, failed
	u.Systemf("115离线导入 %s：提交 %d 条，重复 %d 条，失败 %d 条", batch.ID, submitted, duplicates, failed)
	u.notifyOfflineSummary(batch.ID, submitted, duplicates, failed)
	return result, nil
}

func (u *Unpackerr) notifyOfflineSummary(id string, submitted, duplicates, failed int) {
	settings := u.notificationSettings()
	if !settings.Enabled || settings.URL == "" || !notificationStageEnabled(settings, notifyOffline) {
		return
	}
	u.sendNotification(settings, "📥", "115 离线导入", "115离线", fmt.Sprintf("批次 %s：提交 %d，重复 %d，失败 %d", id, submitted, duplicates, failed))
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
	space, err := u.n115Request(ctx, http.MethodGet, n115OfflineSpace, url.Values{"ct": {"offline"}, "ac": {"space"}, "_": {fmt.Sprint(time.Now().UnixMilli())}})
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
	uid := n115CookieUID(u.CloudDrive2.N115Cookie)
	if uid == "" {
		return nil, fmt.Errorf("115 Cookie 中缺少 UID")
	}
	form := url.Values{"wp_path_id": {targetCID}, "savepath": {""}, "uid": {uid}, "sign": {sign}, "time": {timestamp}}
	for index, link := range links {
		form.Set(fmt.Sprintf("url[%d]", index), link)
	}
	response, err := u.n115Request(ctx, http.MethodPost, n115OfflineBase+"?ct=lixian&ac=add_task_urls", form)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(links))
	rows := offlineResponseRows(response)
	if len(rows) > 0 {
		for index, raw := range rows {
			item, ok := raw.(map[string]any)
			if ok && index < len(links) {
				result[offlineLinkIdentity(links[index])] = offlineStringValue(item, "info_hash", "infoHash", "hash")
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

func (u *Unpackerr) n115OfflineRetryAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct {
		BatchID string `json:"batch_id"`
		TaskID  string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.BatchID == "" || input.TaskID == "" {
		http.Error(w, "缺少批次或任务 ID", http.StatusBadRequest)
		return
	}
	if err := u.retry115OfflineTask(r.Context(), input.BatchID, input.TaskID); err != nil {
		status := http.StatusBadGateway
		if err.Error() == "只能重试失败的离线任务" || err.Error() == "离线记录已清空" { status = http.StatusConflict }
		http.Error(w, err.Error(), status)
		return
	}
	u.notifyOfflineSummary(input.BatchID, 1, 0, 0)
	u.writeJSON(w, map[string]any{"success": true, "message": "已重新提交"})
}

func (u *Unpackerr) retry115OfflineTask(parent context.Context, batchID, taskID string) error {
	u.offlineMu.Lock()
	batch := u.offlineStore.Batches[batchID]
	var task *n115OfflineTask
	if batch != nil {
		for _, item := range batch.Tasks {
			if item.ID == taskID { task = item; break }
		}
	}
	if task == nil || (task.Status != "failed" && task.Status != "submit_failed") {
		u.offlineMu.Unlock()
		return fmt.Errorf("只能重试失败的离线任务")
	}
	previous := task.Status
	task.Status = "submitting"
	task.UpdatedAt = time.Now()
	link, targetCID := task.Link, batch.TargetCID
	u.offlineMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	result, retryErr := u.n115SubmitOfflineBatch(ctx, []string{link}, targetCID)
	hash := result[offlineLinkIdentity(link)]
	u.offlineMu.Lock()
	if u.offlineStore.Batches[batchID] != batch {
		u.offlineMu.Unlock()
		return fmt.Errorf("离线记录已清空")
	}
	task.RetryCount++
	task.UpdatedAt = time.Now()
	if retryErr != nil {
		task.Status, task.Error = previous, retryErr.Error()
	} else {
		if hash == "" { hash = offlineLinkHash(link) }
		task.Status, task.Error, task.InfoHash = "submitted", "", hash
		task.NotifiedStatus = ""
		batch.NextCheck = time.Now().Add(15 * time.Second)
	}
	saveErr := u.save115OfflineStoreLocked()
	u.offlineMu.Unlock()
	if saveErr != nil {
		return saveErr
	}
	if retryErr != nil {
		u.Errorf("115离线重试失败：%s：%v", task.Name, retryErr)
		return retryErr
	}
	u.Systemf("115离线重试已提交：%s", task.Name)
	return nil
}

func (u *Unpackerr) n115OfflineRetryAllAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	type target struct{ batchID, taskID string }
	u.offlineMu.RLock()
	targets := make([]target, 0)
	for batchID, batch := range u.offlineStore.Batches {
		for _, task := range batch.Tasks {
			if task.Status == "failed" || task.Status == "submit_failed" { targets = append(targets, target{batchID, task.ID}) }
		}
	}
	u.offlineMu.RUnlock()
	succeeded, failed := 0, 0
	for _, item := range targets {
		if err := u.retry115OfflineTask(r.Context(), item.batchID, item.taskID); err != nil { failed++ } else { succeeded++ }
	}
	u.Systemf("115离线一键重试：成功提交 %d 条，仍失败 %d 条", succeeded, failed)
	u.notifyOfflineSummary("一键重试", succeeded, 0, failed)
	u.writeJSON(w, map[string]any{"success": failed == 0, "retried": len(targets), "submitted": succeeded, "failed": failed, "message": fmt.Sprintf("本次重试 %d 条，成功提交 %d 条，仍失败 %d 条", len(targets), succeeded, failed)})
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
			status, exists := statuses.byHash[strings.ToLower(strings.TrimSpace(task.InfoHash))]
			if !exists { status, exists = statuses.byLink[offlineLinkIdentity(task.Link)] }
			if !exists { status, exists = statuses.byName[strings.ToLower(strings.TrimSpace(task.Name))] }
			if !exists {
				continue
			}
			before := task.Status
			task.Status, task.Error, task.UpdatedAt = status.Status, status.Error, now
			if task.Status == "success" && !xtractr.IsArchiveFile(task.Name) { task.ExtractedAt = now }
			if before != "success" && task.Status == "success" { batch.HandoffChecks = 0 }
			if before != task.Status && (task.Status == "success" || task.Status == "failed") {
				u.Systemf("115离线%s：%s %s", map[bool]string{true:"完成", false:"失败"}[task.Status == "success"], task.Name, task.Error)
				settings := u.notificationSettings()
				if task.NotifiedStatus != task.Status && settings.Enabled && settings.URL != "" && notificationStageEnabled(settings, notifyOffline) {
					icon, title := "✅", "115 离线成功"
					if task.Status == "failed" { icon, title = "❌", "115 离线失败" }
					detail := task.Name
					if task.Status == "failed" && task.Error != "" { detail += "：" + task.Error }
					u.sendNotification(settings, icon, title, "115离线", detail)
					task.NotifiedStatus = task.Status
				}
			}
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
type offlineRemoteStatuses struct {
	byHash map[string]offlineRemoteStatus
	byLink map[string]offlineRemoteStatus
	byName map[string]offlineRemoteStatus
}

func (u *Unpackerr) n115OfflineStatuses(ctx context.Context) (offlineRemoteStatuses, error) {
	empty := newOfflineRemoteStatuses()
	space, err := u.n115Request(ctx, http.MethodGet, n115OfflineSpace, url.Values{"ct": {"offline"}, "ac": {"space"}, "_": {fmt.Sprint(time.Now().UnixMilli())}})
	if err != nil { return empty, err }
	sign, timestamp := fmt.Sprint(space["sign"]), fmt.Sprint(space["time"])
	if data, ok := space["data"].(map[string]any); ok {
		if sign == "" || sign == "<nil>" { sign = fmt.Sprint(data["sign"]) }
		if timestamp == "" || timestamp == "<nil>" { timestamp = fmt.Sprint(data["time"]) }
	}
	uid := n115CookieUID(u.CloudDrive2.N115Cookie)
	if uid == "" || sign == "" || sign == "<nil>" { return empty, fmt.Errorf("无法取得115离线查询凭据") }
	all := newOfflineRemoteStatuses()
	for page := 1; page <= 10; page++ {
		response, err := u.n115Request(ctx, http.MethodPost, n115OfflineBase+"?ct=lixian&ac=task_lists", url.Values{"page": {fmt.Sprint(page)}, "uid": {uid}, "sign": {sign}, "time": {timestamp}})
		if err != nil { return empty, err }
		pageStatuses := offlineStatuses(response)
		for key, value := range pageStatuses.byHash { all.byHash[key] = value }
		for key, value := range pageStatuses.byLink { all.byLink[key] = value }
		for key, value := range pageStatuses.byName { all.byName[key] = value }
		pageCount := int(int115Value(response["page_count"]))
		if pageCount == 0 {
			if data, ok := response["data"].(map[string]any); ok { pageCount = int(int115Value(data["page_count"])) }
		}
		if pageStatuses.count() == 0 || (pageCount > 0 && page >= pageCount) || (pageCount == 0 && pageStatuses.count() < 30) { break }
	}
	return all, nil
}

func n115CookieUID(cookie string) string {
	for _, field := range strings.Split(cookie, ";") {
		parts := strings.SplitN(strings.TrimSpace(field), "=", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "UID") { continue }
		return strings.SplitN(parts[1], "_", 2)[0]
	}
	return ""
}

func offlineStatuses(response map[string]any) offlineRemoteStatuses {
	result := newOfflineRemoteStatuses()
	rows := offlineResponseRows(response)
	for _, raw := range rows {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		hash := strings.ToLower(offlineStringValue(item, "info_hash", "infoHash", "hash"))
		statusValue := int115Value(item["status"])
		status := "downloading"
		if statusValue == 2 || fmt.Sprint(item["percentDone"]) == "100" || fmt.Sprint(item["percent_done"]) == "100" {
			status = "success"
		} else if statusValue < 0 {
			status = "failed"
		}
		errorText := ""
		if value := item["error_msg"]; value != nil {
			errorText = strings.TrimSpace(fmt.Sprint(value))
			if errorText == "<nil>" { errorText = "" }
		}
		remote := offlineRemoteStatus{Status: status, Error: errorText}
		if hash != "" { result.byHash[hash] = remote }
		if link := offlineStringValue(item, "url", "source_url", "task_url"); link != "" { result.byLink[offlineLinkIdentity(link)] = remote }
		if name := offlineStringValue(item, "name", "file_name", "title"); name != "" { result.byName[strings.ToLower(name)] = remote }
	}
	return result
}

func newOfflineRemoteStatuses() offlineRemoteStatuses {
	return offlineRemoteStatuses{byHash: make(map[string]offlineRemoteStatus), byLink: make(map[string]offlineRemoteStatus), byName: make(map[string]offlineRemoteStatus)}
}

func (s offlineRemoteStatuses) count() int { return max(len(s.byHash), len(s.byLink), len(s.byName)) }

func cleanOfflineValue(value string) string {
	return strings.TrimSpace(value)
}

func offlineStringValue(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, exists := item[key]; exists && value != nil {
			if text := cleanOfflineValue(fmt.Sprint(value)); text != "" { return text }
		}
	}
	return ""
}

func offlineResponseRows(response map[string]any) []any {
	for _, key := range []string{"tasks", "result", "data"} {
		if values, ok := response[key].([]any); ok { return values }
		if wrapper, ok := response[key].(map[string]any); ok {
			for _, nested := range []string{"tasks", "result", "data"} {
				if values, ok := wrapper[nested].([]any); ok { return values }
			}
		}
	}
	return nil
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
	files, visibleFiles, err := u.n115OfflineArchiveFiles(ctx, targetCID, 5, 5000)
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
		u.bind115OfflineArchive(batchID, file, version.Key)
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
			u.run115CloudExtract(n115OfflineMapping(u.CloudDrive2, version.SourceCID), file, version)
		}(file, version)
	}
	if queued > 0 {
		u.Printf("115离线批次 %s 已发现并提交 %d 个压缩包", batchID, queued)
		u.notifyEvent(notifyCloud115, "📦", "离线压缩包已交给云解压", "115离线", fmt.Sprintf("批次 %s：%d 个压缩包", batchID, queued))
	}
	u.offlineMu.Lock()
	if current := u.offlineStore.Batches[batchID]; current != nil { current.HandoffChecks++; _ = u.save115OfflineStoreLocked() }
	u.offlineMu.Unlock()
	if visibleFiles > 0 {
		if len(files) == 0 { u.Systemf("115离线批次 %s 已成功，未发现压缩包，跳过云解压", batchID) }
	} else {
		u.Systemf("115离线批次 %s 已成功，文件暂未可见，下次轮询继续检查", batchID)
	}
}

func (u *Unpackerr) bind115OfflineArchive(batchID string, file n115File, taskKey string) {
	u.offlineMu.Lock()
	defer u.offlineMu.Unlock()
	batch := u.offlineStore.Batches[batchID]
	if batch == nil { return }
	for _, task := range batch.Tasks {
		if task.Status != "success" || task.ArchiveTaskKey != "" { continue }
		if !strings.EqualFold(strings.TrimSpace(task.Name), strings.TrimSpace(file.Name)) { continue }
		if task.Size <= 0 || file.Size <= 0 || task.Size != file.Size { continue }
		task.ArchiveTaskKey, task.ArchiveName = taskKey, file.Name
		task.ArchiveCID, task.ArchiveFID = file.CID, file.FID
		task.ExtractedAt, task.UpdatedAt = time.Now(), time.Now()
		u.Systemf("115离线任务已关联云解压：%s → %s", task.Name, file.Name)
		_ = u.save115OfflineStoreLocked()
		return
	}
}

func (u *Unpackerr) offlinePipelineStatus(task *n115OfflineTask) string {
	if task.ArchiveTaskKey == "" {
		if task.Status == "success" && xtractr.IsArchiveFile(task.Name) { return "离线成功，等待发现压缩包" }
		return ""
	}
	if value, ok := u.cd2Tasks.Load(task.ArchiveTaskKey); ok {
		if transfer, valid := value.(*CD2Transfer); valid && transfer != nil { return transfer.State }
	}
	if u.state != nil {
		u.state.mu.RLock()
		defer u.state.mu.RUnlock()
		if item, ok := u.state.Failed[task.ArchiveTaskKey]; ok {
			if item.Error != "" { return "处理失败：" + item.Error }
			return "处理失败"
		}
		if _, ok := u.state.Processed[task.ArchiveTaskKey]; ok { return "云解压已完成" }
		for _, item := range u.state.Fallback115 {
			if item.TaskKey == task.ArchiveTaskKey {
				if item.Approval { return "云解压失败，等待批准本地下载" }
				return "云解压失败，等待本地处理"
			}
		}
	}
	return "已交给115云解压"
}

func (u *Unpackerr) n115OfflineArchiveFiles(ctx context.Context, rootCID string, maxDepth, maxFiles int) ([]n115File, int, error) {
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
			if err != nil { return archives, visitedFiles, err }
			visitedFiles += len(files)
			for _, file := range files {
				if file.CID == "" { file.CID = current.cid }
				if xtractr.IsArchiveFile(file.Name) { archives = append(archives, file) }
			}
			if len(files) < n115ScanPageSize { break }
		}
		if current.depth >= maxDepth { continue }
		folders, err := u.n115ChildFolderCIDs(ctx, current.cid)
		if err != nil { return archives, visitedFiles, err }
		for _, cid := range folders {
			if _, exists := seen[cid]; exists { continue }
			seen[cid] = struct{}{}
			queue = append(queue, folder{cid: cid, depth: current.depth + 1})
		}
	}
	return archives, visitedFiles, nil
}

func (u *Unpackerr) n115OfflineClearAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct{ Scope string `json:"scope"`; BatchID string `json:"batch_id"`; TaskID string `json:"task_id"` }
	_ = json.NewDecoder(r.Body).Decode(&input)
	u.offlineMu.Lock()
	cleared := 0
	if input.BatchID != "" && input.TaskID != "" {
		batch := u.offlineStore.Batches[input.BatchID]
		if batch == nil { u.offlineMu.Unlock(); http.Error(w, "离线批次不存在", http.StatusNotFound); return }
		kept := batch.Tasks[:0]
		for _, task := range batch.Tasks { if task.ID == input.TaskID { cleared++ } else { kept = append(kept, task) } }
		batch.Tasks = kept
		if len(batch.Tasks) == 0 { delete(u.offlineStore.Batches, input.BatchID) }
		err := u.save115OfflineStoreLocked()
		u.offlineMu.Unlock()
		if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
		u.writeJSON(w, map[string]any{"success": true, "cleared": cleared})
		return
	}
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
