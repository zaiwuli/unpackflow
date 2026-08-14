package unpackerr

/* Folder Watching Codez */

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.cloudfoundry.org/bytefmt"
	"github.com/fsnotify/fsnotify"
	"github.com/radovskyb/watcher"
	"golift.io/cnfg"
	"golift.io/xtractr"
)

// defaultPollInterval is the local directory compensation scan interval.
const (
	defaultPollInterval = time.Minute
	minimumPollInterval = 5 * time.Millisecond
	defaultFolderDelete = 10 * time.Minute
)

// FolderConfig defines the input data for a watched folder.
//
//nolint:lll
type FolderConfig struct {
	DeleteOrig       bool           `json:"delete_original"  toml:"delete_original"   xml:"delete_original"   yaml:"delete_original"`
	DeleteFiles      bool           `json:"delete_files"     toml:"delete_files"      xml:"delete_files"      yaml:"delete_files"`
	DisableLog       bool           `json:"disable_log"      toml:"disable_log"       xml:"disable_log"       yaml:"disable_log"`
	MoveBack         bool           `json:"move_back"        toml:"move_back"         xml:"move_back"         yaml:"move_back"`
	DeleteAfter      *cnfg.Duration `json:"delete_after"     toml:"delete_after"      xml:"delete_after"      yaml:"delete_after"`
	ExtractPath      string         `json:"extract_path"     toml:"extract_path"      xml:"extract_path"      yaml:"extract_path"`
	ArchivePath      string         `json:"archive_path"     toml:"archive_path"      xml:"archive_path"      yaml:"archive_path"`
	ExtractISOs      bool           `json:"extract_isos"     toml:"extract_isos"      xml:"extract_isos"      yaml:"extract_isos"`
	DisableRecursion bool           `json:"disableRecursion" toml:"disable_recursion" xml:"disable_recursion" yaml:"disableRecursion"`
	ExcludePaths     []string       `json:"exclude_paths"    toml:"exclude_paths"     xml:"exclude_path"      yaml:"exclude_paths"`
	Path             string         `json:"path"             toml:"path"              xml:"path"              yaml:"path"`
	ExternalOnly     bool           `json:"external_only"    toml:"external_only"     xml:"external_only"     yaml:"external_only"`
}

// Folders holds all known (created) folders in all watch paths.
type Folders struct {
	Logs
	Interval time.Duration
	Config   []*FolderConfig
	Folders  map[string]*Folder
	Events   chan *eventData
	Updates  chan *xtractr.Response
	FSNotify *fsnotify.Watcher
	Watcher  *watcher.Watcher
}

// Logs interface for folders.
type Logs interface {
	Printf(msg string, v ...any)
	Errorf(msg string, v ...any)
	Debugf(msg string, v ...any)
}

// Folder is a "new" watched folder.
type Folder struct {
	updated  time.Time
	status   ExtractStatus
	config   *FolderConfig
	files    []string
	retries  uint
	archives xtractr.ArchiveList
}

type eventData struct {
	cnfg *FolderConfig
	name string
	file string
	op   string
}

func (u *Unpackerr) validateFolders() error {
	for idx := range u.Folders {
		folder := u.Folders[idx]
		for label, dir := range map[string]string{
			"monitor": folder.Path,
			"output":  folder.ExtractPath,
			"archive": folder.ArchivePath,
		} {
			if strings.TrimSpace(dir) == "" {
				continue
			}
			if err := os.MkdirAll(expandHomedir(dir), 0o755); err != nil {
				return fmt.Errorf("creating %s directory %s: %w", label, dir, err)
			}
		}
		if u.Folders[idx].DeleteAfter == nil {
			// If delete after wasn't set, then set it to 10 minutes.
			u.Folders[idx].DeleteAfter = &cnfg.Duration{Duration: defaultFolderDelete}
		}
	}

	return nil
}

func (u *Unpackerr) logFolders() {
	if epath, count := "", len(u.Folders); count == 1 {
		folder := u.Folders[0]
		if folder.ExtractPath != "" {
			epath = ", extract to: " + folder.ExtractPath
		}

		u.Printf(" => Folder Config: 1 path: %s%s; delete_after:%v delete_orig:%v delete_files:%v "+
			"log_file:%v move_back:%v isos:%v event_buffer:%d",
			folder.Path, epath, folder.DeleteAfter, folder.DeleteOrig, folder.DeleteFiles,
			!folder.DisableLog, folder.MoveBack, folder.ExtractISOs, u.Folder.Buffer)
	} else {
		u.Printf(" => Folder Config: %d paths, event_buffer:%d ", count, u.Folder.Buffer)

		for _, folder := range u.Folders {
			if epath = ""; folder.ExtractPath != "" {
				epath = " extract to: " + folder.ExtractPath
			}

			u.Printf(" =>    Path: %s%s; delete_after:%v delete_orig:%v delete_files:%v log_file:%v move_back:%v isos:%v",
				folder.Path, epath, folder.DeleteAfter, folder.DeleteOrig, folder.DeleteFiles,
				!folder.DisableLog, folder.MoveBack, folder.ExtractISOs)
		}
	}
}

// PollFolders begins the routines to watch folders for changes.
// if those changes include the addition of compressed files, they
// are processed for exctraction.
func (u *Unpackerr) PollFolders() {
	var (
		flist []string
		err   error
	)

	if isRunningInDocker() && u.Folder.Interval.Duration == 0 {
		u.Folder.Interval.Duration = defaultPollInterval
	}

	u.Folders, flist = checkFolders(u.Folders, u.Logger)

	u.folders, err = u.Folder.newWatcher(u.Folders, u.Logger)
	if err != nil {
		u.Errorf("Watching Folders: %s", err)
		return
	}
	// do not close either watcher.

	if len(u.Folders) == 0 {
		return
	}

	go u.folders.watchFSNotify()
	u.scanExistingFolderArchives()
	go func() {
		// Close the small gap between the initial scan and watcher snapshot.
		// This matters for bind mounts where the UI can become healthy just
		// before the folder watcher has fully entered its event loop.
		time.Sleep(2 * time.Second)
		u.scanExistingFolderArchives()
	}()

	u.Printf("目录监控已启动：%s", strings.Join(flist, ", "))

	// Setting an interval of any value less than 5 milliseconds
	// (except zero in docker) allows disabling the poller.
	if u.Folder.Interval.Duration < minimumPollInterval {
		return
	}

	go func() {
		if err := u.folders.Watcher.Start(u.Folder.Interval.Duration); err != nil {
			u.Errorf("Folder poller stopped: %v", err)
		}
	}()

	u.Printf("目录扫描已启动：每 %s 扫描 %s", u.Folder.Interval.String(), strings.Join(flist, ", "))
}

// scanExistingFolderArchives submits archives that were already present when
// the service started. Files created while the watcher is initializing may also
// land here, so normal stability checks still decide when extraction begins.
// External-only folders are CD2 cache folders and must only be submitted after
// their copy operation has completed.
func (u *Unpackerr) scanExistingFolderArchives() {
	for _, folder := range u.Folders {
		if folder.ExternalOnly {
			continue
		}
		err := filepath.WalkDir(folder.Path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if folder.isExcludedPath(path) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() || !xtractr.IsArchiveFile(entry.Name()) {
				return nil
			}
			version, versionErr := sourceVersion("local", path)
			if versionErr == nil && u.wasProcessed(version) {
				u.Debugf("本地压缩包已处理，跳过: %s", path)
				return nil
			}
			u.folders.InjectFileEvent(path, "startup scan")
			return nil
		})
		if err != nil {
			u.Errorf("启动扫描失败：%s：%v", folder.Path, err)
		}
	}
}

// checkFolders stats all configured folders and returns only "good" ones.
func checkFolders(folders []*FolderConfig, log Logs) ([]*FolderConfig, []string) {
	var (
		err         error
		goodFolders = folders[:0]
		goodFlist   = []string{}
	)

	for _, folder := range folders {
		folder.Path, err = filepath.Abs(expandHomedir(folder.Path))
		if err != nil {
			log.Errorf("Folder '%s' (bad path): %v", folder.Path, err)
			continue
		}

		if folder.ExtractPath != "" {
			folder.ExtractPath, err = filepath.Abs(expandHomedir(folder.ExtractPath))
			if err != nil {
				log.Errorf("Folder '%s' (bad extract path): %v", folder.ExtractPath, err)
				continue
			}
		}

		folder.ExcludePaths = normalizeFolderExcludePaths(folder.Path, folder.ExcludePaths)

		if stat, err := os.Stat(folder.Path); err != nil {
			log.Errorf("Folder '%s' (cannot watch): %v", folder.Path, err)
			continue
		} else if !stat.IsDir() {
			log.Errorf("Folder '%s' (cannot watch): not a folder", folder.Path)
			continue
		}

		goodFolders = append(goodFolders, folder)
		goodFlist = append(goodFlist, folder.Path)
	}

	return goodFolders, goodFlist
}

func normalizeFolderExcludePaths(basePath string, excludes []string) []string {
	cleaned := make([]string, 0, len(excludes))

	for _, exclude := range excludes {
		exclude = strings.TrimSpace(exclude)
		if exclude == "" {
			continue
		}

		exclude = expandHomedir(exclude)
		if !filepath.IsAbs(exclude) {
			exclude = filepath.Join(basePath, exclude)
		}

		if abs, err := filepath.Abs(exclude); err == nil {
			cleaned = append(cleaned, filepath.Clean(abs))
		}
	}

	return cleaned
}

func (c *FolderConfig) isExcludedPath(path string) bool {
	if len(c.ExcludePaths) == 0 || path == "" {
		return false
	}

	path = filepath.Clean(path)

	for _, exclude := range c.ExcludePaths {
		exclude = filepath.Clean(exclude)
		if path == exclude || strings.HasPrefix(path, exclude+string(os.PathSeparator)) {
			return true
		}
	}

	return false
}

// newWatcher returns a new folder watcher.
// You must call folders.FSNotify.Close() when you're done with it.
func (c FoldersConfig) newWatcher(folderConfig []*FolderConfig, log Logs) (*Folders, error) {
	folders := &Folders{
		Interval: c.Interval.Duration,
		Config:   folderConfig,
		Folders:  make(map[string]*Folder),
		Events:   make(chan *eventData, c.Buffer),
		Updates:  make(chan *xtractr.Response, updateChanBuf),
		Logs:     log,
	}

	if len(folderConfig) == 0 {
		return folders, nil // do not initialize watcher
	}

	folders.Watcher = watcher.New()
	folders.Watcher.FilterOps(watcher.Rename, watcher.Move, watcher.Write, watcher.Create)
	folders.Watcher.IgnoreHiddenFiles(true)

	fsn, err := fsnotify.NewWatcher()
	if err != nil {
		return folders, fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}

	folders.FSNotify = fsn

	for _, folder := range folderConfig {
		if err := folders.Watcher.Add(folder.Path); err != nil {
			log.Errorf("Folder '%s' (cannot poll): %v", folder.Path, err)
		}

		if err := fsn.Add(folder.Path); err != nil {
			log.Errorf("Folder '%s' (cannot watch): %v", folder.Path, err)
		}
	}

	return folders, nil
}

// Add uses either fsnotify or watcher.
func (f *Folders) Add(folder string) error {
	if f.Interval >= minimumPollInterval {
		if err := f.Watcher.Add(folder); err != nil {
			return fmt.Errorf("watcher: %w", err)
		}

		return nil
	}

	if err := f.FSNotify.Add(folder); err != nil {
		return fmt.Errorf("fsnotify: %w", err)
	}

	return nil
}

// Remove uses either fsnotify or watcher.
func (f *Folders) Remove(folder string) {
	if f.Watcher != nil {
		_ = f.Watcher.Remove(folder)
	}

	if f.FSNotify != nil {
		_ = f.FSNotify.Remove(folder)
	}
}

// extractTrackedItem starts an archive or folder's extraction after it hasn't been written to in a while.
func (u *Unpackerr) extractTrackedItem(name string, folder *Folder, now time.Time) {
	u.folders.Remove(name) // stop the fs watcher(s).
	// update status.
	u.folders.Folders[name].updated = now
	u.folders.Folders[name].status = QUEUED

	// Do not extract r00 file if rar file with same name exists.
	if strings.HasSuffix(strings.ToLower(name), ".r00") &&
		xtractr.CheckR00ForRarFile(getFileList(filepath.Dir(name)), filepath.Base(name)) {
		u.Printf("[目录任务] 已移除无需解压的重复条目：%v（已存在 RAR 主卷）", name)
		u.folders.Folders[name].status = EXTRACTEDNOTHING

		return
	}

	// create a queue counter in the main history; add to u.Map and send webhook for a new folder.
	item := u.updateQueueStatus(&newStatus{Name: name, Status: QUEUED}, u.folders.Folders[name].updated, true)
	u.updateHistory(FolderString + ": " + name)

	exclude := folderExcludeSuffixes(name, folder.config)

	// extract it.
	queueSize, err := u.Extract(&xtractr.Xtract{
		Password:         u.getPasswordFromPath(name),
		Passwords:        u.Passwords,
		Name:             name,
		Filter:           xtractr.Filter{Path: name, ExcludeSuffix: exclude},
		TempFolder:       !folder.config.MoveBack,
		ExtractTo:        folder.config.ExtractPath,
		DeleteOrig:       false,
		CBChannel:        u.folders.Updates,
		CBFunction:       nil,
		Progress:         u.progressUpdateCallback(item),
		LogFile:          !folder.config.DisableLog,
		DisableRecursion: folder.config.DisableRecursion,
	})
	if err != nil {
		u.Errorf("[ERROR] %v", err)
		return
	}

	u.Printf("[目录任务] 已排队：%s，队列数量 %d", name, queueSize)
}

// folderExcludeSuffixes returns archive suffixes to ignore when scanning for items to extract.
// For watched archive files with disable_recursion enabled, exclude all archive suffixes so
// extracted nested archives are not picked up by follow-up scans in the extraction library.
func folderExcludeSuffixes(path string, cfg *FolderConfig) []string {
	exclude := []string{}
	if !cfg.ExtractISOs {
		exclude = append(exclude, ".iso")
	}

	if !cfg.DisableRecursion {
		return exclude
	}

	stat, err := os.Stat(path)
	if err != nil || stat.IsDir() || !xtractr.IsArchiveFile(path) {
		return exclude
	}

	return append(exclude, xtractr.SupportedExtensions()...)
}

func getFileList(path string) []os.FileInfo {
	dir, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer dir.Close()

	if stat, err := dir.Stat(); err != nil || !stat.IsDir() {
		return nil
	}

	fileList, err := dir.Readdir(-1)
	if err != nil {
		return nil
	}

	return fileList
}

// folderXtractrCallback is run twice by the xtractr library when the extraction begins, and finishes.
func (u *Unpackerr) folderXtractrCallback(resp *xtractr.Response) {
	folder, ok := u.folders.Folders[resp.X.Name]

	switch item := u.Map[resp.X.Name]; {
	case !ok, item == nil:
		// It doesn't exist? weird. delete it and bail out.
		delete(u.folders.Folders, resp.X.Name)
		delete(u.Map, resp.X.Name)

		return
	case !resp.Done:
		item.XProg.Archives = resp.Archives.Count() + resp.Extras.Count()
		folder.status = EXTRACTING
		u.Printf("[目录任务] 开始解压：%s，已重试 %d 次，队列剩余 %d", resp.X.Name, folder.retries, resp.Queued)
	case errors.Is(resp.Error, xtractr.ErrNoCompressedFiles):
		folder.status = EXTRACTEDNOTHING
		u.Printf("[目录任务] %s：%s：%v", folder.status.Desc(), resp.X.Name, resp.Error)
	case resp.Error != nil:
		folder.archives = resp.Archives
		folder.status = EXTRACTFAILED
		u.Errorf("[目录任务] %s：%s：%v", folder.status.Desc(), resp.X.Name, resp.Error)
		u.updateMetrics(resp, FolderString, folder.config.Path)
	default: // this runs in a go routine
		u.updateMetrics(resp, FolderString, folder.config.Path)
		u.Printf("[目录任务] 解压完成：%s，耗时 %v，压缩文件 %d 个，附加压缩文件 %d 个，输出文件 %d 个，写入 %sB",
			resp.X.Name, resp.Elapsed.Round(time.Second), resp.Archives.Count(),
			resp.Extras.Count(), len(resp.NewFiles), bytefmt.ByteSize(resp.Size))

		folder.archives = resp.Archives
		folder.status = EXTRACTED
		folder.files = resp.NewFiles
		var cd2Sources []string
		if pending, ok := u.pendingCD2ForPath(resp.X.Name); ok {
			cd2Sources = append(cd2Sources, pending.Files...)
		}
		if sources, cached := u.cd2Cache.Load(filepath.Clean(resp.X.Name)); cached {
			if mapped, ok := sources.([]string); ok && len(mapped) > 0 {
				cd2Sources = append([]string(nil), mapped...)
			}
			if pending, ok := u.pendingCD2ForPath(resp.X.Name); ok && pending.Version.Key != "" {
				u.markProcessed(pending.Version)
			} else if version, err := sourceGroupVersion("cd2", cd2Sources); err == nil {
				u.markProcessed(version)
			}
			u.removePendingCD2(filepath.Clean(resp.X.Name))
			u.cd2Resume.Delete(filepath.Clean(resp.X.Name))
			go u.deleteCachedSource(resp.X.Name, cd2Sources)
		} else if version, err := sourceVersion("local", resp.X.Name); err == nil {
			u.markProcessed(version)
		}
	}

	folder.updated = resp.Started.Add(resp.Elapsed)
	u.updateQueueStatus(&newStatus{Name: resp.X.Name, Resp: resp, Status: folder.status}, folder.updated, true)
}

// watchFSNotify reads file system events from a channel and processes them.
// This runs in its own go routine, and eventually sends the event back into the main routine.
func (f *Folders) watchFSNotify() {
	defer log.Println("Folder watcher routine exited. No longer watching any folders.")

	for {
		select {
		case err := <-f.Watcher.Error:
			f.Errorf("watcher: %v", err)
		case err := <-f.FSNotify.Errors:
			f.Errorf("fsnotify: %v", err)
		case event, ok := <-f.FSNotify.Events:
			if !ok {
				return
			}

			f.handleFileEvent(event.Name, "f "+event.Op.String())
		case event := <-f.Watcher.Event:
			f.handleFileEvent(event.Path, "w "+event.Op.String())
		case <-f.Watcher.Closed:
			return
		}
	}
}

// handleFileEvent takes formatted events from fsnotify or fswatcher, does minimal
// (thread safe) validation before sending the re-formatted event to the main go routine.
func (f *Folders) handleFileEvent(name, operation string) {
	if strings.HasSuffix(name, suffix) {
		return
	}

	// Send this event to processEvent().
	for _, cnfg := range f.Config {
		// Do not handle events on the watched folder itself.
		if name == cnfg.Path {
			return
		}

		// cnfg.Path: "/Users/Documents/watched_folder"
		// event.Name: "/Users/Documents/watched_folder/new_folder/file.rar"
		// eventData.name: "new_folder"
		if !strings.HasPrefix(name, cnfg.Path) {
			continue // Not the configured folder for the event we just got.
		}
		if cnfg.ExternalOnly && (strings.HasPrefix(operation, "f ") || strings.HasPrefix(operation, "w ")) {
			continue // CD2 cache folders are submitted only after a complete copy.
		}

		if cnfg.isExcludedPath(name) {
			f.Debugf("Folder: Ignored event from excluded path: %v", name)
			continue
		}

		// processEvent (below) handles events sent to f.Events.
		if dir := filepath.Dir(name); dir == cnfg.Path {
			f.Events <- &eventData{name: filepath.Base(name), cnfg: cnfg, file: name, op: operation}
		} else {
			f.Events <- &eventData{name: filepath.Base(dir), cnfg: cnfg, file: name, op: operation}
		}

		return
	}

	f.Debugf("Folder: Ignored event from non-configured path: %v", name)
}

// InjectFileEvent feeds an externally sourced filesystem change into the same
// path filtering and stability tracking used by fsnotify and the poller.
func (f *Folders) InjectFileEvent(name, operation string) {
	f.handleFileEvent(name, operation)
}

// processEvent is here to process the event in the `*Unpackerr` scope before sending it back to the `*Folders` scope.
func (u *Unpackerr) processEvent(event *eventData, now time.Time) {
	// Do not watch our own log file.
	if event.file == u.LogFile || event.file == u.Webserver.LogFile {
		return
	}
	if event.cnfg != nil && !event.cnfg.ExternalOnly {
		// Match the identity recorded after extraction. Root-level archives are
		// processed as files; archives below a child directory are processed as
		// that directory. Using the raw event file for both made nested archives
		// bypass their successful history entry.
		identityPath := event.file
		if filepath.Dir(event.file) != event.cnfg.Path {
			identityPath = filepath.Dir(event.file)
		}
		version, err := sourceVersion("local", identityPath)
		if err == nil && u.wasProcessed(version) {
			u.Debugf("本地压缩包已处理，忽略重复事件: %s", event.file)
			return
		}
	}

	u.folders.processEvent(event, now)
}

// processEvent processes the event that was received.
func (f *Folders) processEvent(event *eventData, now time.Time) {
	dirPath := filepath.Join(event.cnfg.Path, event.name)

	if event.cnfg.isExcludedPath(event.file) || event.cnfg.isExcludedPath(dirPath) {
		f.Debugf("Folder: Ignored File Event (%s) '%s' (excluded path)", event.op, event.file)
		return
	}

	stat, err := os.Stat(dirPath)
	if err != nil {
		// Item is unusable (probably deleted), remove it from history.
		if _, ok := f.Folders[dirPath]; ok {
			f.Debugf("Folder: Removing Tracked Item: %v", dirPath)
			delete(f.Folders, dirPath)
			f.Remove(dirPath)
		}

		f.Debugf("Folder: Ignored File Event (%s) '%s' (unreadable): %v", event.op, event.file, err)

		return
	}

	if !stat.IsDir() && !xtractr.IsArchiveFile(event.name) {
		f.Debugf("Folder: Ignored File Event (%s) '%s' (not archive or dir): %v", event.op, event.file, err)
		return
	}

	f.saveEvent(event, dirPath, now)
}

func (f *Folders) saveEvent(event *eventData, dirPath string, now time.Time) {
	if _, ok := f.Folders[dirPath]; ok {
		// f.Debugf("Item Updated: %v", event.file)
		f.Folders[dirPath].updated = now
		return
	}

	if err := f.Add(dirPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			f.Errorf("Folder: Tracking New Item: %v (event: %s): %v ", dirPath, event.op, err)
		}

		return
	}

	f.Printf("[目录任务] 发现新压缩包：%v（事件：%s）", dirPath, event.op)

	f.Folders[dirPath] = &Folder{
		updated: now,
		status:  WAITING,
		config:  event.cnfg,
	}
}

// checkFolderStats runs at an interval to see if any folders need work done on them.
// This runs on an interval ticker in the main go routine.
func (u *Unpackerr) checkFolderStats(now time.Time) {
	for name, folder := range u.folders.Folders {
		switch elapsed := now.Sub(folder.updated); {
		case WAITING == folder.status && elapsed >= u.StartDelay.Duration:
			// The folder hasn't been written to in a while, extract it.
			u.extractTrackedItem(name, folder, now)
		case EXTRACTEDNOTHING == folder.status:
			// Wait until this item hasn't been touched for a while, so it doesn't re-queue.
			if now.Sub(folder.updated) > u.StartDelay.Duration {
				// Ignore "no compressed files" errors for folders.
				delete(u.Map, name)
				delete(u.folders.Folders, name)
			}
		case EXTRACTFAILED == folder.status && elapsed >= u.RetryDelay.Duration &&
			(u.MaxRetries == 0 || folder.retries < u.MaxRetries):
			u.Retries++
			folder.retries++
			folder.updated = now
			folder.status = WAITING
			u.Printf("[目录任务] 重新尝试解压：%s（%d/%d，上次失败于 %v 前）",
				folder.config.Path, folder.retries, u.MaxRetries, elapsed.Round(time.Second))
		case EXTRACTFAILED == folder.status && folder.retries < u.MaxRetries:
			// This empty block is to avoid deleting an item that needs more retries.
		case EXTRACTFAILED == folder.status && u.MaxRetries > 0 && folder.retries >= u.MaxRetries:
			// Retries exhausted — clean up to prevent the item from staying in the map forever.
			u.updateQueueStatus(&newStatus{Name: name, Status: DELETED, Resp: nil}, now, true)
			delete(u.folders.Folders, name)
			u.Printf("[目录任务] 重试次数已用完（%d/%d），停止处理：%s", folder.retries, u.MaxRetries, name)
		case folder.status > EXTRACTING && folder.config.DeleteAfter.Duration <= 0:
			if folder.config.ArchivePath != "" {
				u.deleteAfterReached(name, now, folder)
				continue
			}
			// if DeleteAfter is 0 we don't delete anything. we are done.
			u.updateQueueStatus(&newStatus{Name: name, Status: DELETED, Resp: nil}, now, false)
			delete(u.folders.Folders, name)
		case EXTRACTED == folder.status && elapsed >= folder.config.DeleteAfter.Duration:
			u.deleteAfterReached(name, now, folder)
		}
	}
}

//nolint:wsl_v5
func (u *Unpackerr) deleteAfterReached(name string, now time.Time, folder *Folder) {
	var webhook bool
	if folder.config.ArchivePath != "" {
		if err := archiveFolderSources(name, folder); err != nil {
			folder.updated = now
			u.Errorf("本地原包归档失败，将稍后重试：%s：%v", name, err)
			return
		}
		u.Printf("本地原包已归档：%s -> %s", name, folder.config.ArchivePath)
		webhook = true
	}
	// Folder reached delete delay (after extraction), nuke it.
	if folder.config.DeleteFiles && !folder.config.MoveBack {
		u.delChan <- &fileDeleteReq{Paths: []string{strings.TrimRight(name, `/\`) + suffix}}
		webhook = true
	} else if folder.config.DeleteFiles && len(folder.files) > 0 {
		u.delChan <- &fileDeleteReq{Paths: folder.files}
		webhook = true
	}

	if folder.config.DeleteOrig && !folder.config.MoveBack {
		u.delChan <- &fileDeleteReq{Paths: []string{name}}
		webhook = true
	} else if folder.config.DeleteOrig && len(folder.archives) > 0 {
		u.delChan <- &fileDeleteReq{Paths: folder.archives.List()}
		webhook = true
	}

	u.updateQueueStatus(&newStatus{Name: name, Status: DELETED, Resp: nil}, now, webhook)
	// Folder reached delete delay (after extraction), nuke it.
	delete(u.folders.Folders, name)
}

func archiveFolderSources(name string, folder *Folder) error {
	files := folder.archives.List()
	if len(files) == 0 {
		if info, err := os.Stat(name); err == nil && !info.IsDir() {
			files = []string{name}
		}
	}
	for _, source := range files {
		if err := archiveSourceFile(source, folder.config.Path, folder.config.ArchivePath); err != nil {
			return err
		}
	}
	return nil
}

func archiveSourceFile(source, watchRoot, archiveRoot string) error {
	relative, err := filepath.Rel(watchRoot, source)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		relative = filepath.Base(source)
	}
	target := filepath.Join(archiveRoot, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		target = uniqueArchiveTarget(target)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	if err := copyStableFile(source, target); err != nil {
		return err
	}
	return os.Remove(source)
}

func uniqueArchiveTarget(target string) string {
	extension := filepath.Ext(target)
	base := strings.TrimSuffix(target, extension)
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, index, extension)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

type newStatus struct {
	Name   string
	Status ExtractStatus
	Resp   *xtractr.Response
}

// updateQueueStatus for an on-going tracked extraction.
// This is called from a channel callback to update status in a single go routine.
// This is used by apps and Folders in a few other places as well.
func (u *Unpackerr) updateQueueStatus(data *newStatus, now time.Time, sendHook bool) *Extract {
	if _, ok := u.Map[data.Name]; !ok {
		// This is a new Folder being queued for extraction.
		// Arr apps do not land here. They create their own queued items in u.Map.
		u.Map[data.Name] = &Extract{
			Path:    data.Name,
			App:     FolderString,
			Status:  QUEUED,
			Updated: now,
			IDs:     map[string]any{"title": data.Name}, // required or webhook may break.
		}

		u.Map[data.Name].XProg = &ExtractProgress{Extract: u.Map[data.Name]}
		u.clearCD2TransferForCachedPath(data.Name)

		if sendHook {
			u.runAllHooks(u.Map[data.Name])
			if _, alreadyNotified := u.cd2Notice.LoadAndDelete(filepath.Clean(data.Name)); !alreadyNotified {
				u.notifyUI(u.Map[data.Name].Status, u.Map[data.Name])
			}
		}

		return u.Map[data.Name]
	}

	if data.Resp != nil {
		u.Map[data.Name].Resp = data.Resp
	}

	u.Map[data.Name].Status = data.Status
	u.Map[data.Name].Updated = now

	if sendHook {
		u.runAllHooks(u.Map[data.Name])
		u.notifyUI(u.Map[data.Name].Status, u.Map[data.Name])
	}

	return u.Map[data.Name]
}
