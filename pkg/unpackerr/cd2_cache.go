package unpackerr

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golift.io/cnfg"
	"golift.io/xtractr"
)

var (
	rawRARVolume   = regexp.MustCompile(`(?i)^(.*?)(?:\.part\d+\.rar|\.rar|\.r\d{2})$`)
	sevenZipVolume = regexp.MustCompile(`(?i)^(.*?\.7z)\.\d{3}$`)
)

// validateCloudDriveCache creates an internal watched folder for cached CD2
// archives. Regular watched folders exclude this directory, preventing cache
// files from being submitted twice by the local scanner.
func (u *Unpackerr) validateCloudDriveCache() error {
	cfg := &u.CloudDrive2
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.CacheDir) == "" && len(u.Folders) > 0 {
		cfg.CacheDir = u.Folders[0].Path
	}
	if strings.TrimSpace(cfg.CacheDir) == "" {
		return fmt.Errorf("CloudDrive2 cache_dir is required when no folder is configured")
	}
	cacheDir, err := filepath.Abs(expandHomedir(cfg.CacheDir))
	if err != nil {
		return fmt.Errorf("CloudDrive2 cache_dir: %w", err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("CloudDrive2 cache_dir: %w", err)
	}
	if err := os.MkdirAll(cacheDir+".staging", 0o755); err != nil {
		return fmt.Errorf("CloudDrive2 cache staging: %w", err)
	}
	cfg.CacheDir = cacheDir
	if cfg.CacheExtractPath == "" {
		cfg.CacheExtractPath = "/output"
	}
	extractPath, err := filepath.Abs(expandHomedir(cfg.CacheExtractPath))
	if err != nil {
		return fmt.Errorf("CloudDrive2 cache_extract_path: %w", err)
	}
	cfg.CacheExtractPath = extractPath
	if cfg.CacheDeleteDelay.Duration <= 0 {
		cfg.CacheDeleteDelay = cnfg.Duration{Duration: time.Minute}
	}

	for _, folder := range u.Folders {
		folderPath, _ := filepath.Abs(expandHomedir(folder.Path))
		if sameOrChild(cacheDir, folderPath) {
			folder.ExcludePaths = append(folder.ExcludePaths, cacheDir)
		}
	}
	cacheFolder := &FolderConfig{
		Path:         cacheDir,
		ExtractPath:  extractPath,
		DeleteOrig:   !cfg.KeepCache,
		DeleteAfter:  &cfg.CacheDeleteDelay,
		ExternalOnly: true,
	}
	for _, folder := range u.Folders {
		folderPath, _ := filepath.Abs(expandHomedir(folder.Path))
		if filepath.Clean(folderPath) == cacheDir {
			folder.Path = cacheDir
			folder.ExtractPath = extractPath
			folder.DeleteOrig = !cfg.KeepCache
			folder.DeleteAfter = &cfg.CacheDeleteDelay
			folder.ExternalOnly = true
			return nil
		}
	}
	u.Folders = append(u.Folders, cacheFolder)
	return nil
}

func sameOrChild(child, parent string) bool {
	child, parent = filepath.Clean(child), filepath.Clean(parent)
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// cacheCloudDrivePaths copies a complete archive group off the mounted CloudDrive
// filesystem before sending it to Unpackerr's native folder pipeline.
func (u *Unpackerr) cacheCloudDrivePaths(paths []string) {
	if len(paths) == 0 {
		return
	}
	for _, source := range paths {
		if !xtractr.IsArchiveFile(filepath.Base(source)) {
			u.Debugf("CloudDrive2 补扫跳过非压缩文件：%s", source)
			continue
		}
		files, groupKey, err := archiveVolumeGroup(source)
		if err != nil {
			u.Errorf("CloudDrive2 cache scan failed for %s: %v", source, err)
			continue
		}
		version, err := sourceGroupVersion("cd2", files)
		if err != nil {
			// CloudDrive2 FUSE may reject stat temporarily while the remote file
			// is being materialized. Do not discard the event; copyStableFile
			// will perform the authoritative stability check.
			u.Errorf("CloudDrive2 文件状态读取失败，继续尝试复制：%v", err)
		}
		if version.Key != "" && u.wasProcessed(version) {
			u.Debugf("CloudDrive2 压缩包已有成功记录，跳过：%s", source)
			continue
		}
		if u.hasPendingCD2Files(files) {
			u.Debugf("CloudDrive2 压缩包正在处理，跳过重复事件: %s", source)
			continue
		}
		if _, loaded := u.cd2Copy.LoadOrStore(groupKey, struct{}{}); loaded {
			u.Debugf("CloudDrive2 已有相同复制任务运行：%s", source)
			continue
		}
		u.Printf("CloudDrive2 已提交复制任务：%d 个文件，来源 %s", len(files), source)
		go func(files []string, groupKey string) {
			defer u.cd2Copy.Delete(groupKey)
			if err := u.cacheCloudDriveGroup(files, groupKey); err != nil {
				u.Errorf("CloudDrive2 复制失败，将进入重试：%v", err)
				u.queueCD2Retry(groupKey, files, err)
			}
		}(files, groupKey)
	}
}

func archiveVolumeGroup(source string) ([]string, string, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return nil, "", err
	}
	name, dir := filepath.Base(source), filepath.Dir(source)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	key := strings.ToLower(name)
	match := func(candidate string) bool { return candidate == name }
	if groups := sevenZipVolume.FindStringSubmatch(name); len(groups) > 0 {
		key = strings.ToLower(groups[1])
		prefix := strings.ToLower(groups[1]) + "."
		match = func(candidate string) bool {
			candidate = strings.ToLower(candidate)
			if !strings.HasPrefix(candidate, prefix) {
				return false
			}
			suffix := strings.TrimPrefix(candidate, prefix)
			return len(suffix) == 3 && isDigits(suffix)
		}
	} else if groups := rawRARVolume.FindStringSubmatch(name); len(groups) > 0 {
		key = strings.ToLower(groups[1])
		prefix := strings.ToLower(groups[1])
		match = func(candidate string) bool {
			candidate = strings.ToLower(candidate)
			if candidate == prefix+".rar" || strings.HasPrefix(candidate, prefix+".part") && strings.HasSuffix(candidate, ".rar") {
				return true
			}
			return strings.HasPrefix(candidate, prefix+".r") && len(candidate) == len(prefix)+4 && isDigits(candidate[len(prefix)+2:])
		}
	}
	files := make([]string, 0, 4)
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !match(entry.Name()) {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	if len(files) == 0 {
		return nil, "", fmt.Errorf("archive source is unavailable: %s", source)
	}
	sort.Strings(files)
	return files, filepath.Join(dir, key), nil
}

func isDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func (u *Unpackerr) cacheCloudDriveGroup(files []string, key string) error {
	sum := sha256.Sum256([]byte(key))
	taskID := fmt.Sprintf("%x", sum[:8])
	finalDir := u.CloudDrive2.CacheDir
	staging := filepath.Join(u.CloudDrive2.CacheDir+".staging", taskID+fmt.Sprintf("-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(staging)
		}
	}()

	for _, source := range files {
		if err := copyStableFile(source, filepath.Join(staging, filepath.Base(source))); err != nil {
			return err
		}
	}
	for _, source := range files {
		name := filepath.Base(source)
		if err := os.Rename(filepath.Join(staging, name), filepath.Join(finalDir, name)); err != nil {
			return err
		}
	}
	_ = os.Remove(staging)
	success = true
	primary := archivePrimary(files)
	for _, source := range files {
		cached := filepath.Join(finalDir, filepath.Base(source))
		u.cd2Cache.Store(filepath.Clean(cached), append([]string(nil), files...))
	}
	primaryPath := filepath.Join(finalDir, filepath.Base(primary))
	u.cd2Cache.Store(filepath.Clean(primaryPath), append([]string(nil), files...))
	version, _ := sourceGroupVersion("cd2", files)
	u.savePendingCD2(PendingCD2{Key: filepath.Clean(primaryPath), Files: append([]string(nil), files...), CachedPrimary: primaryPath, Version: version})
	u.cd2Resume.Store(filepath.Clean(primaryPath), struct{}{})
	u.folders.InjectFileEvent(primaryPath, "cd2 cache ready")
	u.Printf("CloudDrive2 cache ready: %d file(s) copied to %s", len(files), finalDir)
	return nil
}

func (u *Unpackerr) queueCD2Retry(groupKey string, files []string, copyErr error) {
	key := "copy|" + filepath.Clean(groupKey)
	attempts := 1
	for _, item := range u.pendingCD2() {
		if item.Key == key {
			attempts = item.Attempts + 1
			break
		}
	}
	delay := time.Duration(1<<min(attempts-1, 6)) * 30 * time.Second
	u.savePendingCD2(PendingCD2{Key: key, Files: append([]string(nil), files...), Attempts: attempts, NextAttempt: time.Now().Add(delay), LastError: copyErr.Error()})
}

func (u *Unpackerr) resumeCD2Pending() {
	for _, pending := range u.pendingCD2() {
		pending := pending
		if pending.CachedPrimary != "" {
			if _, err := os.Stat(pending.CachedPrimary); err == nil {
				if _, loaded := u.cd2Resume.LoadOrStore(filepath.Clean(pending.CachedPrimary), struct{}{}); loaded {
					continue
				}
				u.cd2Cache.Store(filepath.Clean(pending.CachedPrimary), append([]string(nil), pending.Files...))
				u.folders.InjectFileEvent(pending.CachedPrimary, "cd2 resume cached task")
				continue
			}
		}
		if pending.NextAttempt.After(time.Now()) || len(pending.Files) == 0 {
			continue
		}
		groupKey := strings.TrimPrefix(pending.Key, "copy|")
		if _, loaded := u.cd2Copy.LoadOrStore(groupKey, struct{}{}); loaded {
			continue
		}
		go func() {
			defer u.cd2Copy.Delete(groupKey)
			if err := u.cacheCloudDriveGroup(pending.Files, groupKey); err != nil {
				u.queueCD2Retry(groupKey, pending.Files, err)
				return
			}
			u.removePendingCD2(pending.Key)
		}()
	}
}

func archivePrimary(files []string) string {
	for _, file := range files {
		name := strings.ToLower(filepath.Base(file))
		if strings.HasSuffix(name, ".7z.001") || strings.HasSuffix(name, ".part1.rar") || strings.HasSuffix(name, ".part01.rar") || strings.HasSuffix(name, ".rar") {
			return file
		}
	}
	return files[0]
}

func copyStableFile(source, target string) error {
	before, err := os.Stat(source)
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	after, err := os.Stat(source)
	if err != nil {
		return err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("source changed while copying: %s", source)
	}
	return nil
}

func (u *Unpackerr) deleteCachedSource(cachePath string) {
	value, ok := u.cd2Cache.LoadAndDelete(filepath.Clean(cachePath))
	if !ok || !u.CloudDrive2.DeleteSource {
		return
	}
	for _, source := range value.([]string) {
		if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
			u.Errorf("CloudDrive2 source delete failed for %s: %v", source, err)
		}
	}
}
