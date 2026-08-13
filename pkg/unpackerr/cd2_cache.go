package unpackerr

import (
	"context"
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

type CD2Transfer struct {
	Key       string    `json:"key"`
	Path      string    `json:"path"`
	State     string    `json:"state"`
	Bytes     int64     `json:"bytes"`
	Total     int64     `json:"total"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Speed     int64     `json:"speed"`
	ETA       int64     `json:"eta_seconds"`
	Error     string    `json:"error,omitempty"`
}

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
	if cfg.CopyTimeout.Duration <= 0 {
		cfg.CopyTimeout.Duration = 24 * time.Hour
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
	if err := validateVolumeSequence(files); err != nil {
		return nil, "", err
	}
	return files, filepath.Join(dir, key), nil
}

func validateVolumeSequence(files []string) error {
	if len(files) == 0 {
		return nil
	}
	sevenPattern := regexp.MustCompile(`(?i)\.7z\.(\d{3})$`)
	partPattern := regexp.MustCompile(`(?i)\.part(\d+)\.rar$`)
	oldPattern := regexp.MustCompile(`(?i)\.r(\d{2})$`)
	if hasVolumePattern(files, sevenPattern) {
		return validateNumberedVolumes(files, sevenPattern, 1)
	}
	if hasVolumePattern(files, partPattern) {
		return validateNumberedVolumes(files, partPattern, 1)
	}
	if hasVolumePattern(files, oldPattern) {
		hasPrimary := false
		oldFiles := make([]string, 0, len(files))
		for _, file := range files {
			name := strings.ToLower(filepath.Base(file))
			if strings.HasSuffix(name, ".rar") && !partPattern.MatchString(name) {
				hasPrimary = true
			}
			if oldPattern.MatchString(name) {
				oldFiles = append(oldFiles, file)
			}
		}
		if !hasPrimary {
			return fmt.Errorf("压缩包缺少首卷：.rar")
		}
		return validateNumberedVolumes(oldFiles, oldPattern, 0)
	}
	return nil
}

func hasVolumePattern(files []string, pattern *regexp.Regexp) bool {
	for _, file := range files {
		if pattern.MatchString(filepath.Base(file)) {
			return true
		}
	}
	return false
}

func validateNumberedVolumes(files []string, pattern *regexp.Regexp, start int) error {
	numbers := make([]int, 0, len(files))
	for _, file := range files {
		match := pattern.FindStringSubmatch(filepath.Base(file))
		if len(match) != 2 {
			continue
		}
		number := 0
		for _, digit := range match[1] {
			number = number*10 + int(digit-'0')
		}
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	for index, number := range numbers {
		if number != start+index {
			return fmt.Errorf("压缩包缺少分卷：期望编号 %d，实际编号 %d", start+index, number)
		}
	}
	return nil
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
	staging := filepath.Join(u.CloudDrive2.CacheDir+".staging", taskID)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), u.CloudDrive2.CopyTimeout.Duration)
	defer cancel()
	startedAt := time.Now()
	totalBytes, existingBytes, err := cacheGroupSpace(files, staging)
	if err != nil {
		return err
	}
	if free, spaceErr := freeSpace(staging); spaceErr == nil && free < uint64(totalBytes-existingBytes) {
		return fmt.Errorf("缓存空间不足：还需 %d 字节，可用 %d 字节", totalBytes-existingBytes, free)
	}
	u.cd2Tasks.Store(key, &CD2Transfer{Key: key, Path: files[0], State: "正在缓存", Bytes: existingBytes, Total: totalBytes, StartedAt: startedAt, UpdatedAt: startedAt})
	defer u.cd2Tasks.Delete(key)

	completedBefore := int64(0)
	newBytesBefore := int64(0)
	for _, source := range files {
		target := filepath.Join(staging, filepath.Base(source))
		initialDone := int64(0)
		if info, statErr := os.Stat(target); statErr == nil {
			initialDone = info.Size()
		}
		if err := copyStableFileContext(ctx, source, target, func(done, total int64) {
			copied := completedBefore + done
			elapsed := time.Since(startedAt).Seconds()
			speed, eta := int64(0), int64(0)
			if elapsed > 0 {
				speed = int64(float64(newBytesBefore+max(done-initialDone, 0)) / elapsed)
			}
			if speed > 0 {
				eta = (totalBytes - copied) / speed
			}
			u.cd2Tasks.Store(key, &CD2Transfer{Key: key, Path: source, State: "正在缓存", Bytes: copied, Total: totalBytes, StartedAt: startedAt, UpdatedAt: time.Now(), Speed: speed, ETA: eta})
		}); err != nil {
			return err
		}
		if info, statErr := os.Stat(source); statErr == nil {
			completedBefore += info.Size()
			newBytesBefore += max(info.Size()-initialDone, 0)
		}
	}
	u.cd2Tasks.Store(key, &CD2Transfer{Key: key, Path: files[0], State: "正在校验", Bytes: totalBytes, Total: totalBytes, StartedAt: startedAt, UpdatedAt: time.Now()})
	if err := verifyCopiedGroup(files, staging); err != nil {
		return err
	}
	for _, source := range files {
		name := filepath.Base(source)
		target := filepath.Join(finalDir, name)
		staged := filepath.Join(staging, name)
		if _, err := os.Stat(staged); err == nil {
			_ = os.Remove(target)
			if err := os.Rename(staged, target); err != nil {
				return err
			}
		} else if ok, verifyErr := sameFileMetadata(source, target); verifyErr != nil || !ok {
			return fmt.Errorf("缓存最终落盘不完整：%s", name)
		}
	}
	_ = os.Remove(staging)
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

func cacheGroupSpace(sources []string, staging string) (total, existing int64, err error) {
	for _, source := range sources {
		info, statErr := os.Stat(source)
		if statErr != nil {
			return 0, 0, statErr
		}
		total += info.Size()
		if cached, cachedErr := os.Stat(filepath.Join(staging, filepath.Base(source))); cachedErr == nil {
			candidate := min(cached.Size(), info.Size())
			if candidate > 0 {
				match, matchErr := samePrefix(source, filepath.Join(staging, filepath.Base(source)), candidate)
				if matchErr != nil {
					return 0, 0, matchErr
				}
				if match {
					existing += candidate
				}
			}
		}
	}
	return total, existing, nil
}

func copyStableFileContext(ctx context.Context, source, target string, progress func(int64, int64)) error {
	before, err := os.Stat(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := out.Seek(0, io.SeekEnd); err != nil {
		_ = out.Close()
		return err
	}
	done, _ := out.Seek(0, io.SeekCurrent)
	if done > before.Size() {
		done = 0
	}
	if done > 0 {
		match, matchErr := samePrefix(source, target, done)
		if matchErr != nil {
			_ = out.Close()
			return matchErr
		}
		if !match {
			done = 0
		}
	}
	if done == 0 {
		if err := out.Truncate(0); err != nil {
			_ = out.Close()
			return err
		}
	}
	_, _ = in.Seek(done, io.SeekStart)
	_, _ = out.Seek(done, io.SeekStart)
	buf := make([]byte, 1024*1024)
	for done < before.Size() {
		select {
		case <-ctx.Done():
			_ = out.Close()
			return ctx.Err()
		default:
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				_ = out.Close()
				return err
			}
			done += int64(n)
			if progress != nil {
				progress(done, before.Size())
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = out.Close()
			return readErr
		}
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	after, err := os.Stat(source)
	if err != nil {
		return err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("源文件复制期间发生变化：%s", source)
	}
	if err := os.Chtimes(target, before.ModTime(), before.ModTime()); err != nil {
		return err
	}
	return nil
}

func samePrefix(source, target string, size int64) (bool, error) {
	sourceFile, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer sourceFile.Close()
	targetFile, err := os.Open(target)
	if err != nil {
		return false, err
	}
	defer targetFile.Close()
	sourceHash, targetHash := sha256.New(), sha256.New()
	if _, err := io.CopyN(sourceHash, sourceFile, size); err != nil {
		return false, err
	}
	if _, err := io.CopyN(targetHash, targetFile, size); err != nil {
		return false, err
	}
	return string(sourceHash.Sum(nil)) == string(targetHash.Sum(nil)), nil
}

func verifyCopiedGroup(sources []string, staging string) error {
	for _, source := range sources {
		ok, err := sameFileMetadata(source, filepath.Join(staging, filepath.Base(source)))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("缓存校验失败：%s", filepath.Base(source))
		}
	}
	return nil
}

func sameFileMetadata(source, target string) (bool, error) {
	s, err := os.Stat(source)
	if err != nil {
		return false, err
	}
	t, err := os.Stat(target)
	if err != nil {
		return false, err
	}
	return s.Size() == t.Size() && s.ModTime().Equal(t.ModTime()), nil
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
