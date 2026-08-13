package unpackerr

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	homedir "github.com/mitchellh/go-homedir"
	"golift.io/rotatorr"
	"golift.io/rotatorr/timerotator"
)

// satisfy gomnd.
const (
	callDepth    = 2 // log the line that called us.
	megabyte     = 1024 * 1024
	logsDirMode  = 0o755
	starrLogPfx  = " =>    Server: "
	starrLogLine = "%s, apikey:%v, timeout:%v, verify_ssl:%v, protos:%s, " +
		"syncthing:%v, delete_orig:%v, delete_delay:%v, paths:%q"
)

// ExtractStatus is our enum for an extract's status.
type ExtractStatus uint8

// Extract Statuses.
const (
	WAITING = ExtractStatus(iota)
	QUEUED
	EXTRACTING
	EXTRACTFAILED
	EXTRACTED
	IMPORTED
	DELETING
	DELETEFAILED // unused
	DELETED
	EXTRACTEDNOTHING
)

// Desc makes ExtractStatus human readable.
func (status ExtractStatus) Desc() string {
	if status > EXTRACTEDNOTHING {
		return "Unknown"
	}

	return []string{
		// The order must not be faulty.
		"等待处理",
		"排队中",
		"正在解压",
		"解压失败",
		"解压完成，等待整理",
		"已整理",
		"正在清理",
		"清理失败",
		"已完成",
		"未发现可解压文件",
	}[status]
}

// MarshalText turns a status into a word, for a json identifier.
func (status ExtractStatus) MarshalText() ([]byte, error) {
	return []byte(status.String()), nil
}

// String turns a status into a short string.
func (status ExtractStatus) String() string {
	if status > EXTRACTEDNOTHING {
		return "unknown"
	}

	return []string{
		// The order must not be faulty.
		"waiting",
		"queued",
		"extracting",
		"extractfailed",
		"extracted",
		"imported",
		"deleting",
		"deletefailed",
		"deleted",
		"extractednothing",
	}[status]
}

// Debugf writes Debug log lines... to stdout and/or a file.
func (l *Logger) Debugf(msg string, v ...any) {
	err := l.Debug.Output(callDepth, fmt.Sprintf(msg, v...))
	if err != nil {
		fmt.Println("Logger Error:", err) //nolint:forbidigo
	}
}

// Printf writes log lines... to stdout and/or a file.
func (l *Logger) Printf(msg string, v ...any) {
	err := l.Info.Output(callDepth, fmt.Sprintf(msg, v...))
	if err != nil {
		fmt.Println("Logger Error:", err) //nolint:forbidigo
	}
}

// Errorf writes log errors... to stdout and/or a file.
func (l *Logger) Errorf(msg string, v ...any) {
	err := l.Error.Output(callDepth, fmt.Sprintf(msg, v...))
	if err != nil {
		fmt.Println("Logger Error:", err) //nolint:forbidigo
	}
}

// logCurrentQueue prints the number of things happening.
func (u *Unpackerr) logCurrentQueue(now time.Time) {
	stats := u.stats()
	_ = now
	u.Printf("任务概览：等待 %d，排队 %d，解压中 %d，已完成 %d，失败 %d",
		stats.Waiting, stats.Queued, stats.Extracting, stats.Extracted, stats.Failed)
	u.updateTray(stats, uint(len(u.folders.Events)+len(u.updates)+len(u.folders.Updates)+len(u.delChan)+len(u.hookChan)))
}

// setupLogging splits log write into a file and/or stdout.
func (u *Unpackerr) setupLogging() {
	if u.Config.Debug {
		u.Info.SetFlags(log.Lshortfile | log.Lmicroseconds | log.Ldate)
		u.Error.SetFlags(log.Lshortfile | log.Lmicroseconds | log.Ldate)
	}

	u.LogFile = getLogFilePath(u.LogFile, "unpackerr.log")
	fileMode, _ := strconv.ParseUint(u.LogFileMode, bits8, base32)
	rotate := &rotatorr.Config{
		Filepath: u.LogFile,                     // log file name.
		FileSize: int64(u.LogFileMb) * megabyte, // megabytes
		Rotatorr: &timerotator.Layout{
			FileCount:  u.LogFiles,
			PostRotate: u.postLogRotate,
		}, // number of files to keep.
		DirMode:  logsDirMode,
		FileMode: os.FileMode(fileMode),
	}

	if u.LogFile != "" {
		var err error

		u.rotatorr, err = rotatorr.New(rotate)
		if err != nil {
			// Fall back to stdout so we don't hammer the filesystem with failed open attempts.
			u.rotatorr = nil
			_, _ = os.Stdout.WriteString("[Unpackerr] Log file unavailable (check path and permissions!!), " +
				"logging to stdout only: " + err.Error() + "\n")
		}
	}

	stderr := os.Stdout
	if u.ErrorStdErr {
		stderr = os.Stderr
	}

	useLogFile := u.LogFile != "" && u.rotatorr != nil

	switch { // only use MultiWriter if we have > 1 writer.
	case !u.Quiet && useLogFile:
		u.updateLogOutput(io.MultiWriter(u.rotatorr, os.Stdout), io.MultiWriter(u.rotatorr, stderr))
	case !u.Quiet && !useLogFile:
		u.updateLogOutput(os.Stdout, stderr)
	case !useLogFile:
		u.updateLogOutput(io.Discard, io.Discard) // default is "nothing"
	default:
		u.updateLogOutput(u.rotatorr, u.rotatorr)
	}
}

// getLogFilePath takes in a path and a base name. In case the path is a directory, they are joined.
func getLogFilePath(logFile, base string) string {
	if expanded, err := homedir.Expand(logFile); err == nil {
		logFile = expanded
	}

	if stat, err := os.Stat(logFile); err == nil && stat.IsDir() {
		return filepath.Join(logFile, base)
	}

	return logFile
}

func (u *Unpackerr) updateLogOutput(writer io.Writer, errors io.Writer) {
	if u.Webserver != nil && u.Webserver.LogFile != "" {
		u.setupHTTPLogging()
	} else {
		u.HTTP.SetOutput(writer)
	}

	if u.Config.Debug {
		u.Logger.Debug.SetOutput(writer)
	}

	log.SetOutput(errors) // catch out-of-scope garbage
	u.Info.SetOutput(writer)
	u.Error.SetOutput(errors)
	u.postLogRotate("", "")
}

func (u *Unpackerr) setupHTTPLogging() {
	u.Webserver.LogFile = getLogFilePath(u.Webserver.LogFile, "http.log")
	rotate := &rotatorr.Config{
		Filepath: u.Webserver.LogFile,                     // log file name.
		FileSize: int64(u.Webserver.LogFileMb) * megabyte, // megabytes
		Rotatorr: &timerotator.Layout{FileCount: u.Webserver.LogFiles},
		DirMode:  logsDirMode,
	}

	switch { // only use MultiWriter if we have > 1 writer.
	case !u.Quiet && u.Webserver.LogFile != "":
		u.HTTP.SetOutput(io.MultiWriter(rotatorr.NewMust(rotate), os.Stdout))
	case !u.Quiet && u.Webserver.LogFile == "":
		u.HTTP.SetOutput(os.Stdout)
	case u.Quiet && u.Webserver.LogFile == "":
		u.HTTP.SetOutput(io.Discard)
	default: // u.Config.Quiet && u.Webserver.LogFile != ""
		u.HTTP.SetOutput(rotatorr.NewMust(rotate))
	}
}

func (u *Unpackerr) postLogRotate(_, newFile string) {
	if newFile != "" {
		go u.Printf("Rotated log file to: %s", newFile)
	}

	if u.rotatorr != nil && u.rotatorr.File != nil {
		redirectStderr(u.rotatorr.File) // Log panics.
	}
}

// logStartupInfo prints info about our startup config.
func (u *Unpackerr) logStartupInfo(msg string, externalFiles map[string]string) {
	u.Printf("UnpackFlow 已启动：%s", msg)
	u.Printf("任务配置：并发 %d，密码 %d 个，轮询间隔 %s", u.Parallel, len(u.Passwords), u.Interval.String())
	if len(externalFiles) > 0 {
		u.Printf("配置文件：已加载 %d 个扩展配置", len(externalFiles))
	}
	u.Printf("WebUI：已启动，监听 %s", u.Webserver.ListenAddr)
}
